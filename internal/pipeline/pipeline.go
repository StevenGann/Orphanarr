// Package pipeline wires scan -> classify -> layout -> plan.
//
// It owns the ordering and nothing else: every decision it makes is
// delegated to a package that can be tested without it.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/StevenGann/Orphanarr/internal/api"
	"github.com/StevenGann/Orphanarr/internal/classify"
	"github.com/StevenGann/Orphanarr/internal/client"
	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/layout"
	"github.com/StevenGann/Orphanarr/internal/media"
	"github.com/StevenGann/Orphanarr/internal/pathmap"
	"github.com/StevenGann/Orphanarr/internal/probe"
	"github.com/StevenGann/Orphanarr/internal/scan"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// ClientEntry is one configured, constructed client.
type ClientEntry struct {
	Cfg       client.Config
	Client    client.DownloadClient
	Mapper    *pathmap.Mapper
	Info      client.Info
	Err       error
	Scannable bool
	Refusal   string
}

// Pipeline runs scans.
type Pipeline struct {
	db     *store.DB
	fs     fsx.FS
	guard  *fsx.Guard
	prober *probe.Prober
	ndjson *ndjson
	cfg    config.Config

	mu      sync.RWMutex
	clients []*ClientEntry
	libs    []layout.Library

	// Cached library writability, refreshed by Reload. Never computed on a
	// status request.
	writable map[string]bool

	// One scan at a time, and one execution at a time. DESIGN §2.5
	// specifies a single serialized executor; without these the periodic
	// ticker and POST /api/v1/scan can run concurrently, and two
	// executions of one plan race on the same destinations.
	scanMu sync.Mutex
	execMu sync.Mutex
}

func New(db *store.DB, g *fsx.Guard, cfg config.Config) *Pipeline {
	return &Pipeline{
		db: db, fs: g, guard: g, cfg: cfg,
		prober: probe.New(g),
		ndjson: newNDJSON(cfg.ConfigDir),
	}
}

// SetConfig replaces the live configuration after a settings save, so a
// change takes effect without a restart wherever it safely can.
func (p *Pipeline) SetConfig(cfg config.Config) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
	p.guard.SetDryRun(cfg.DryRun)
}

func (p *Pipeline) Config() config.Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg
}

func clientID(s string) client.ExternalID { return client.ExternalID(s) }

// Reload rebuilds clients and libraries from the database and re-registers
// every root with the filesystem guard.
//
// Registration order matters and is not cosmetic: source roots are added
// first, and the guard checks them first, so a library root misconfigured
// to nest inside a download root still cannot unlock writes to it.
func (p *Pipeline) Reload(ctx context.Context) error {
	dbClients, err := p.db.ListClients(ctx)
	if err != nil {
		return err
	}
	dbLibs, err := p.db.ListLibraries(ctx)
	if err != nil {
		return err
	}

	var entries []*ClientEntry
	for _, c := range dbClients {
		if !c.Enabled {
			continue
		}
		cfg := client.Config{
			ID: c.ID, Name: c.Name, Kind: c.Kind, BaseURL: c.BaseURL,
			Username: c.Username, Password: c.Password, APIKey: c.APIKey,
		}
		impl, err := client.New(cfg)
		if err != nil {
			entries = append(entries, &ClientEntry{Cfg: cfg, Err: err, Refusal: err.Error()})
			continue
		}
		rules := make([]pathmap.Rule, 0, len(c.Mappings))
		for _, m := range c.Mappings {
			rules = append(rules, pathmap.Rule{Remote: m.Remote, Local: m.Local})
			p.guard.AddSourceRoot(m.Local)
		}
		mapper := pathmap.Identity()
		if len(rules) > 0 {
			mapper = pathmap.New(rules)
		}
		// Under IDENTITY mapping there are no rules to register, and that
		// is the documented common deployment — so the guard held ZERO
		// source roots and I1's check could never fire, in exactly the
		// configuration the README recommends. Every save path the client
		// reports is registered instead, at scan time (registerSourceRoots
		// below), and any root the user did name is registered here.
		entries = append(entries, &ClientEntry{Cfg: cfg, Client: impl, Mapper: mapper})
	}

	// Library roots are REPLACED, not appended. Appending left a root the
	// user deleted, or corrected after a typo, writable for the life of
	// the process.
	p.guard.ResetLibraryRoots()

	var libs []layout.Library
	for _, l := range dbLibs {
		if !l.Enabled {
			continue
		}
		lib := layout.Library{
			Type:        media.Type(l.MediaType),
			Root:        l.Root,
			Opts:        layout.Options{StrictNames: l.StrictNames},
			OneshotsDir: l.OneshotsDir,
		}
		if l.AcceptedExts != "" {
			lib.AcceptedExts = map[string]bool{}
			for _, e := range strings.Split(l.AcceptedExts, ",") {
				if e = strings.TrimSpace(strings.ToLower(e)); e != "" {
					if !strings.HasPrefix(e, ".") {
						e = "." + e
					}
					lib.AcceptedExts[e] = true
				}
			}
		}
		libs = append(libs, lib)
		p.guard.AddLibraryRoot(l.Root)
	}

	p.SetClients(ctx, entries)
	p.SetLibraries(libs)
	p.probeAllPairs(ctx)
	return nil
}

// probeAllPairs runs a real link(2) for every (download root, library root)
// combination. st_dev is never the gate: it reports "fine" in exactly the
// two-bind-mount layout that fails.
// probeAllPairs runs a real link(2) for every (download root, library root)
// pair, and caches library writability.
//
// Three rules, each of which was wrong before:
//
//   - It is keyed on a REGISTERED SOURCE ROOT, never on a torrent's own
//     subdirectory. Probing path.Dir(firstFile) wrote a file inside every
//     seeding torrent's data directory, one per orphan per scan, and made
//     the cache unbounded.
//   - It is called only from Reload — which is startup or a configuration
//     save, i.e. user-initiated, which is what I10's carve-out actually
//     permits. It was previously called from inside the scan loop, so a
//     scheduled scan wrote probe files with dry-run engaged.
//   - It skips entirely in dry-run. The carve-out covers a user asking
//     "can you hardlink?"; it does not cover background polling.
func (p *Pipeline) probeAllPairs(ctx context.Context) {
	p.mu.RLock()
	libs := append([]layout.Library(nil), p.libs...)
	dry := p.cfg.DryRun
	p.mu.RUnlock()

	srcRoots := p.guard.SourceRoots()

	writable := map[string]bool{}
	for _, l := range libs {
		if dry {
			continue
		}
		ok, _ := p.prober.Writable(l.Root)
		writable[l.Root] = ok

		for _, src := range srcRoots {
			res := p.prober.Probe(src, l.Root)
			if res.Outcome != probe.Available {
				p.db.LogEvent(ctx, store.Event{
					Code:    "HARDLINK_UNAVAILABLE",
					Message: fmt.Sprintf("%s -> %s: %s. %s", src, l.Root, res.Outcome, res.Detail),
				})
			}
		}
	}

	p.mu.Lock()
	p.writable = writable
	p.mu.Unlock()
}

func (p *Pipeline) libraryForName(t string) (layout.Library, bool) {
	return p.libraryFor(media.Type(t))
}

// SetClients installs the constructed clients and probes each one.
//
// The probe is where I14 is enforced: a client that cannot express "no
// category" is marked unscannable here, at configure time, rather than
// being discovered mid-scan when it has already selected everything.
func (p *Pipeline) SetClients(ctx context.Context, entries []*ClientEntry) {
	for _, e := range entries {
		if e.Client == nil {
			// The adapter failed to construct — a bad URL, an unknown
			// kind. Reload records the reason; probing a nil interface
			// would panic on the one path a user reaches by typo.
			e.Scannable = false
			continue
		}
		info, err := e.Client.Probe(ctx)
		e.Info, e.Err = info, err
		if err != nil {
			e.Scannable = false
			e.Refusal = err.Error()
			p.db.SetClientStatus(ctx, e.Cfg.ID, nil, err.Error())
			continue
		}
		p.db.SetClientStatus(ctx, e.Cfg.ID, map[string]bool{
			"categories": info.Caps.Categories,
			"tags":       info.Caps.Tags,
			"file_list":  info.Caps.FileList,
		}, "")
		if err := client.CanScan(info.Caps); err != nil {
			e.Scannable = false
			e.Refusal = err.Error()
			p.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "CLIENT_REFUSED",
				Message: e.Cfg.Name + ": " + err.Error(),
			})
			continue
		}
		e.Scannable = true
	}
	p.mu.Lock()
	p.clients = entries
	p.mu.Unlock()
}

func (p *Pipeline) SetLibraries(libs []layout.Library) {
	p.mu.Lock()
	p.libs = libs
	p.mu.Unlock()
}

func (p *Pipeline) libraryFor(t media.Type) (layout.Library, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, l := range p.libs {
		if l.Type == t {
			return l, true
		}
	}
	return layout.Library{}, false
}

// ScanNow performs one full pass and returns what it found.
//
// It produces PLANS, never placements. Nothing here writes to the media
// filesystem: execution is a separate, explicitly-approved step, and
// dry-run is on by default anyway.
func (p *Pipeline) ScanNow(ctx context.Context) (api.Summary, error) {
	p.scanMu.Lock()
	defer p.scanMu.Unlock()

	sum := api.Summary{
		Skipped:    map[string]int{},
		Classified: map[string]int{},
	}

	p.mu.RLock()
	clients := append([]*ClientEntry(nil), p.clients...)
	libRoots := make([]string, 0, len(p.libs))
	for _, l := range p.libs {
		libRoots = append(libRoots, l.Root)
	}
	cfg := p.cfg
	p.mu.RUnlock()

	// Sticky ignores are keyed on the content fingerprint, so they survive
	// the same payload being re-added under a different infohash.
	ignored, _ := p.db.IgnoredFingerprints(ctx)

	settings := scan.Settings{
		SettleWindow: cfg.SettleWindow,
		LibraryRoots: libRoots,
		Exclusions: scan.Exclusions{
			Tags:         []string{"orphanarr-ignore", "orphanarr-filed"},
			Fingerprints: ignored,
		},
	}
	rules := classify.DefaultRules()
	rules.AutoThreshold = cfg.AutoThreshold
	rules.ReviewThreshold = cfg.ReviewThresh
	rules.AmbiguityMargin = cfg.Ambiguity
	switch cfg.PDFDefault {
	case "ebook":
		rules.PDFDefault = media.Ebook
	case "comic":
		rules.PDFDefault = media.Comic
	default:
		rules.PDFDefault = media.Unknown
	}

	now := time.Now()
	var candidates []scan.Candidate
	var categorisedPeers []scan.Candidate

	for _, e := range clients {
		if !e.Scannable {
			// One dead or refused client must never stall the others.
			sum.Errors = append(sum.Errors,
				fmt.Sprintf("%s: %s", e.Cfg.Name, e.Refusal))
			continue
		}
		items, err := e.Client.ListItems(ctx)
		if err != nil {
			sum.Errors = append(sum.Errors, fmt.Sprintf("%s: %v", e.Cfg.Name, err))
			p.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "CLIENT_UNREACHABLE",
				Message: e.Cfg.Name + ": " + err.Error(),
			})
			continue
		}
		sum.Items += len(items)

		for _, it := range items {
			categorised := it.Category != nil && strings.TrimSpace(*it.Category) != ""

			if !it.Complete {
				sum.Skipped["SKIP_INCOMPLETE"]++
				continue
			}

			files, err := e.Client.ListFiles(ctx, it.ID)
			if err != nil {
				sum.Skipped["SKIP_NO_METADATA"]++
				continue
			}

			// The settle window is measured from the PERSISTED
			// first_seen_at, not an in-memory map. An in-memory map resets
			// on every restart, so a container in a restart loop means
			// nothing ever settles — and completion_on is -1 on migrated
			// clients, which is exactly the population this column exists
			// for.
			seen, ok := p.db.FirstSeen(ctx, e.Cfg.ID, string(it.ID))
			if !ok {
				seen = now
			}

			localPaths, resolveErr := scan.ResolveLocal(e.Mapper, it, files)
			if resolveErr != nil {
				// An unmapped path, or a manifest entry that escapes its
				// own save path. Either is a refusal, never a guess.
				sum.Skipped["SKIP_UNMAPPED"]++
				p.db.LogEvent(ctx, store.Event{
					Level: "warn", Code: "SKIP_UNMAPPED",
					Message: it.Name + ": " + resolveErr.Error(),
				})
				continue
			}

			// Register the resolved save path as a source root. This is
			// what arms I1 under identity mapping: the guard now refuses
			// every mutation beneath a directory a client actually
			// reported, whether or not the user configured a mapping.
			if base, err := e.Mapper.ToLocal(it.SavePath); err == nil {
				p.guard.AddSourceRoot(base)
			}
			c := scan.Candidate{
				Client: e.Client, Item: it, Files: files,
				LocalPaths:  localPaths,
				Fingerprint: scan.Fingerprint(files),
				FirstSeen:   seen,
			}

			// A CATEGORISED item is not a candidate, but its paths must
			// still enter the overlap index — that is the entire point of
			// fetching filter=all. O10 requires "no resolved path overlaps
			// a categorized torrent on any client", and it is §3.4 FP-1:
			// Sonarr grabbing as tv-sonarr while the user's cross-seed of
			// the identical content carries no category. Dropping these
			// before the index left that class completely unguarded.
			if categorised {
				categorisedPeers = append(categorisedPeers, c)
				sum.Skipped["SKIP_CATEGORIZED"]++
				continue
			}

			d := scan.Evaluate(c, settings, now)
			if !d.Keep {
				sum.Skipped[d.Code]++
				continue
			}
			candidates = append(candidates, c)
		}
	}

	// The cross-seed gate, over BOTH populations. A cross-seed is by
	// definition the same bytes reached twice, and the dangerous case is
	// the pairing of an uncategorised copy with a categorised one.
	ov := scan.NewOverlap()
	for i, c := range candidates {
		ov.Add(i, c, p.fs)
	}
	blocked := ov.AddPeers(categorisedPeers, p.fs)

	claimed := map[int]bool{}
	plansMade := 0
	for i, c := range candidates {
		if claimed[i] {
			continue
		}
		if blocked[i] {
			// An *arr already owns this payload. Filing it would produce a
			// duplicate library entry and, under copy-only, a full second
			// copy of content that is already imported.
			//
			// The block is closed TRANSITIVELY, which can over-block: a
			// season pack sharing one file with a pack that shares one file
			// with an *arr-owned episode is refused for content no *arr
			// owns. That trade is deliberate and priced — an over-block
			// costs an orphan sitting in review, an under-block costs a
			// full duplicate copy, and §3.4 ranks the latter highest for
			// damage. But it must be VISIBLE: this is surfaced with the
			// overlap chain named, not silently skipped.
			chain := overlapChain(ov, i, candidates)
			sum.Skipped["CROSS_SEED_BLOCKED"]++
			orphanID, _ := p.db.UpsertOrphan(ctx, store.Orphan{
				ClientID: clientRowID(c), ExternalID: string(c.Item.ID),
				Name: c.Item.Name, SavePath: c.Item.SavePath,
				Fingerprint: c.Fingerprint, State: "blocked",
				MediaType: "", Reason: "cross_seed:" + chain,
			})
			p.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "CROSS_SEED_BLOCKED", OrphanID: &orphanID,
				Message: c.Item.Name + ": shares content with a categorised torrent " +
					"an *arr already manages (" + chain + ")",
			})
			continue
		}
		// Overlaps among uncategorised candidates COLLAPSE into one unit
		// of work rather than producing two library entries pointing at
		// one payload (I4). Transitively: claim the peers' peers too, or a
		// chain A~B~C leaves C planned separately.
		for _, peer := range ov.Transitive(i) {
			claimed[peer] = true
			sum.Skipped["SKIP_OVERLAP_COLLAPSED"]++
		}

		fs := toFileSet(c)
		cl, parsed := classify.Classify(fs, rules)
		sum.Classified[string(cl.Type)]++
		sum.Orphans++
		sum.Bytes += fs.TotalSize()

		// Persist the orphan WITH its evidence, not just its verdict. This
		// is what makes "why did it think that was a comic" answerable
		// after the fact, without reproducing the scan.
		sigJSON, _ := json.Marshal(cl.Signals)
		parsedJSON, _ := json.Marshal(parsed)
		filesJSON, _ := json.Marshal(fs.Files)

		state := "discovered"
		if cl.Type == media.Unknown {
			state = "unknown"
		}
		orphanID, err := p.db.UpsertOrphan(ctx, store.Orphan{
			ClientID: clientRowID(c), ExternalID: string(c.Item.ID),
			Name: c.Item.Name, SavePath: c.Item.SavePath,
			Fingerprint: c.Fingerprint, SizeBytes: fs.TotalSize(),
			State: state, MediaType: string(cl.Type),
			Score: cl.Score, ParseConf: parsed.Confidence,
			Cardinality: string(cl.Cardinality), Reason: cl.Reason,
			Signals: sigJSON, Parsed: parsedJSON, Files: filesJSON,
		})
		if err != nil {
			sum.Errors = append(sum.Errors, err.Error())
			continue
		}

		if cl.Type == media.Unknown {
			p.db.LogEvent(ctx, store.Event{
				Code: "CLASSIFY_UNKNOWN", OrphanID: &orphanID,
				Message: c.Item.Name + ": " + cl.Reason,
			})
			continue
		}

		lib, ok := p.libraryFor(cl.Type)
		if !ok {
			sum.Skipped["SKIP_NO_LIBRARY"]++
			continue
		}
		res, err := layout.Build(cl.Type, parsed, fs.Files, lib)
		if err != nil {
			p.db.LogEvent(ctx, store.Event{
				Level: "warn", Code: "LAYOUT_REFUSED", OrphanID: &orphanID,
				Message: c.Item.Name + ": " + err.Error(),
			})
			continue
		}

		// I9: below the auto threshold, or multi/mixed, or rootless, or
		// the layout itself could not resolve — all route to review rather
		// than to a destination. The plan is still BUILT and stored: the
		// user needs to see what would happen in order to approve it.
		needsReview := res.NeedsReview ||
			cl.Score < rules.AutoThreshold ||
			parsed.Confidence < rules.AutoThreshold ||
			cl.Cardinality != media.Single ||
			fs.Rootless ||
			!p.cfg.AutoFile

		reason := res.ReviewReason
		if reason == "" {
			switch {
			case fs.Rootless:
				reason = "the torrent has no common root folder, so its payload " +
					"shares a directory with every other download there"
			case cl.Cardinality != media.Single:
				reason = "this looks like " + string(cl.Cardinality) +
					" content: one destination folder would be wrong"
			case cl.Score < rules.AutoThreshold:
				reason = fmt.Sprintf("classification score %.2f is below the %.2f auto threshold",
					cl.Score, rules.AutoThreshold)
			case parsed.Confidence < rules.AutoThreshold:
				reason = fmt.Sprintf("parse confidence %.2f is below the %.2f auto threshold",
					parsed.Confidence, rules.AutoThreshold)
			case !p.cfg.AutoFile:
				reason = "auto-file is off, so every plan is reviewed before it runs"
			}
		}

		// max_plans_per_run is a real cap, not a validated-and-ignored
		// setting. It is byte-blind — 25 plans can be 25 GB or 25 TB — so
		// the free-space preflight is what actually bounds the damage; this
		// bounds how much a misconfiguration can queue up in one pass.
		if cfg.MaxPlansPerRun > 0 && plansMade >= cfg.MaxPlansPerRun {
			sum.Skipped["SKIP_PLAN_LIMIT"]++
			continue
		}

		planID, err := p.savePlan(ctx, orphanID, cl.Type, res, needsReview, reason, lib)
		if err != nil {
			sum.Errors = append(sum.Errors, err.Error())
			continue
		}

		plansMade++
		code := "PLAN_READY"
		if needsReview {
			code = "PLAN_NEEDS_REVIEW"
		}
		p.db.LogEvent(ctx, store.Event{
			Code: code, OrphanID: &orphanID, PlanID: &planID,
			Message: fmt.Sprintf("%s -> %s (%d files) %s",
				c.Item.Name, cl.Type, len(res.Files), reason),
		})
	}

	// The dashboard's "why isn't it doing anything" panel is built from the
	// event vocabulary, and no SKIP_* code was ever logged — so the panel
	// was structurally always empty, which is the one question §9 says it
	// exists to answer.
	for code, n := range sum.Skipped {
		p.db.LogEvent(ctx, store.Event{
			Code:    code,
			Message: fmt.Sprintf("%d item(s) this scan", n),
		})
	}

	p.db.LogEvent(ctx, store.Event{
		Code: "SCAN_COMPLETED",
		Message: fmt.Sprintf("%d items, %d orphans, %s",
			sum.Items, sum.Orphans, humanBytes(sum.Bytes)),
	})
	return sum, nil
}

// savePlan stores a plan and one step per placement.
//
// The source identity is captured HERE, at plan time, because I13 asserts
// it again immediately before publish — and a plan sits in the review queue
// for hours or days by design, which is exactly the window in which a
// source can change.
func (p *Pipeline) savePlan(ctx context.Context, orphanID int64, t media.Type,
	res layout.Result, needsReview bool, reason string, lib layout.Library) (int64, error) {

	layout.SortPlanned(res.Files)

	pl := store.Plan{
		OrphanID: orphanID, MediaType: string(t), Status: "draft",
		NeedsReview: needsReview, ReviewReason: reason,
	}
	// Reuse an open plan for this orphan instead of minting a new one on
	// every scan: at the 15-minute default that was 96 duplicate drafts a
	// day for a single unfiled item, in a review list that caps at 200.
	if existing, ok := p.db.OpenPlanFor(ctx, orphanID); ok {
		pl.ID = existing
	}

	// The method is decided HERE, from the configured mode and the
	// per-pair probe. It used to be hardcoded to "copy", which made
	// ops__mode, Options.AllowLink, the probe's Available outcome and the
	// whole three-valued badge inert — the machinery shipped and the wire
	// did not.
	method := "copy"
	if p.Config().Mode != "copy" {
		// Probe the registered download ROOT, not path.Dir(firstFile).
		// Keying on a torrent's own subdirectory writes a probe file
		// inside someone's seeding data, once per orphan, and grows the
		// cache without bound — which is the write the guard now refuses
		// outright.
		for _, f := range res.Files {
			if f.Skip || f.Src.AbsPath == "" {
				continue
			}
			root, ok := p.guard.SourceRootFor(f.Src.AbsPath)
			if !ok {
				break
			}
			if r := p.prober.EnsureProbed(root, lib.Root); r.Outcome == probe.Available {
				method = "hardlink"
			}
			break
		}
	}
	warns := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		warns = append(warns, w.String())
	}
	pl.Warnings, _ = json.Marshal(warns)

	seq := 0
	for _, f := range res.Files {
		if f.Skip {
			continue
		}
		step := store.PlanStep{
			Seq: seq, SrcPath: f.Src.AbsPath, DstPath: f.Dst,
			Method: method, Bytes: f.Src.Size, SrcSize: f.Src.Size,
			Status: "pending",
		}
		if step.SrcPath == "" {
			step.SrcPath = f.Src.RelPath
		}
		// A step with no recorded identity cannot be verified before
		// publish, so I13 would degrade to a silent no-op for exactly this
		// step. Fail the plan instead.
		fi, err := p.fs.Stat(step.SrcPath)
		if err != nil {
			return 0, fmt.Errorf("cannot stat source %s: %w", step.SrcPath, err)
		}
		step.SrcDev, step.SrcIno = fi.Dev, fi.Ino
		step.SrcSize, step.Bytes = fi.Size, fi.Size
		step.SrcMtime = fi.ModTime.UTC().Format(time.RFC3339Nano)
		pl.Steps = append(pl.Steps, step)
		if method == "hardlink" {
			pl.LinkBytes += step.Bytes
		} else {
			pl.CopyBytes += step.Bytes
		}
		seq++
	}

	return p.db.SavePlan(ctx, pl)
}

// overlapChain names the peers that caused a block, so an over-block is
// something the user can see and argue with rather than an item that
// silently never appears.
func overlapChain(ov *scan.Overlap, idx int, all []scan.Candidate) string {
	peers := ov.Transitive(idx)
	if len(peers) == 0 {
		return "direct overlap"
	}
	names := make([]string, 0, len(peers))
	for _, p := range peers {
		if p < len(all) {
			names = append(names, all[p].Item.Name)
		}
	}
	if len(names) == 0 {
		return "direct overlap"
	}
	return "via " + strings.Join(names, ", ")
}

func clientRowID(c scan.Candidate) int64 {
	// The candidate carries the adapter, whose ID() is the database row.
	var id int64
	fmt.Sscanf(c.Client.ID(), "%d", &id)
	return id
}

// toFileSet materialises the classifier's input. Work units come from the
// client's manifest, never from a directory walk (I3).
func toFileSet(c scan.Candidate) media.FileSet {
	fs := media.FileSet{Name: c.Item.Name}
	// LocalPaths is built from the SAME filtered-and-ordered walk of
	// c.Files, so the indexes line up. Deriving AbsPath by re-joining here
	// would duplicate the path-mapping logic and let the two drift.
	i := 0
	for _, f := range c.Files {
		if !f.Wanted {
			continue
		}
		sf := media.SourceFile{
			RelPath: f.RelPath,
			Size:    f.Size,
			Ext:     strings.ToLower(path.Ext(f.RelPath)),
		}
		if i < len(c.LocalPaths) {
			sf.AbsPath = c.LocalPaths[i]
		}
		i++
		fs.Files = append(fs.Files, sf)
	}
	// A torrent whose files share no common root makes content_path the
	// entire save path, which is the trap I3 exists for.
	fs.Rootless = !sharesRoot(fs.Files)
	return fs
}

func sharesRoot(files []media.SourceFile) bool {
	if len(files) <= 1 {
		return true
	}
	root := ""
	for _, f := range files {
		i := strings.IndexByte(f.RelPath, '/')
		if i < 0 {
			return false
		}
		r := f.RelPath[:i]
		if root == "" {
			root = r
		} else if root != r {
			return false
		}
	}
	return true
}

// Status reports what a monitoring check should watch.
func (p *Pipeline) Status(ctx context.Context) api.Status {
	p.mu.RLock()
	clients := append([]*ClientEntry(nil), p.clients...)
	libs := append([]layout.Library(nil), p.libs...)
	p.mu.RUnlock()

	p.mu.RLock()
	writable := p.writable
	p.mu.RUnlock()

	st := api.Status{Settings: map[string]string{}}
	for _, e := range clients {
		cs := api.ClientStatus{
			ID: e.Cfg.ID, Name: e.Cfg.Name, Kind: e.Cfg.Kind,
			Reachable: e.Err == nil, Scannable: e.Scannable, Refusal: e.Refusal,
		}
		if e.Err != nil {
			cs.Error = e.Err.Error()
		} else {
			cs.AppVersion = e.Info.AppVersion
		}
		st.Clients = append(st.Clients, cs)
	}

	for _, l := range libs {
		ls := api.LibraryStatus{Type: string(l.Type), Root: l.Root, Enabled: true}
		if info, err := p.fs.Statfs(l.Root); err == nil {
			ls.FreeBytes = info.Avail
			ls.TotalBytes = info.Total
		}
		// Read the cached value. Probing here wrote a file into every
		// library root on every /system/status request — and the UI polls
		// that every 30 seconds, forever, in dry-run too.
		// In dry-run no probe runs, so this is UNKNOWN rather than false.
		// Reporting false would be a false negative in the shipped default.
		w, probed := writable[l.Root]
		ls.Writable = w
		ls.WritableKnown = probed

		// Reported as three outcomes, not a boolean. Without the
		// read-only case the remediation banner tells a user running :ro
		// mounts to do what they have already done.
		ls.Hardlinks = string(probe.Unknown)
		if r, found := p.prober.BestFor(l.Root); found {
			ls.Hardlinks = string(r.Outcome)
			ls.HardlinkDetail = r.Detail
		}
		st.Libraries = append(st.Libraries, ls)
	}
	return st
}

func humanBytes(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
