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
	cfg    config.Config

	mu      sync.RWMutex
	clients []*ClientEntry
	libs    []layout.Library
	first   map[string]time.Time
}

func New(db *store.DB, g *fsx.Guard, cfg config.Config) *Pipeline {
	return &Pipeline{
		db: db, fs: g, guard: g, cfg: cfg,
		prober: probe.New(),
		first:  map[string]time.Time{},
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
		entries = append(entries, &ClientEntry{Cfg: cfg, Client: impl, Mapper: mapper})
	}

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
func (p *Pipeline) probeAllPairs(ctx context.Context) {
	p.mu.RLock()
	entries := append([]*ClientEntry(nil), p.clients...)
	libs := append([]layout.Library(nil), p.libs...)
	p.mu.RUnlock()

	srcRoots := map[string]bool{}
	for _, e := range entries {
		if e.Mapper == nil {
			continue
		}
		for _, r := range e.Mapper.Rules() {
			srcRoots[r.Local] = true
		}
	}

	for src := range srcRoots {
		for _, l := range libs {
			res := p.prober.Probe(src, l.Root)
			if res.Outcome != probe.Available {
				p.db.LogEvent(ctx, store.Event{
					Code:    "HARDLINK_UNAVAILABLE",
					Message: fmt.Sprintf("%s -> %s: %s. %s", src, l.Root, res.Outcome, res.Detail),
				})
			}
		}
	}
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
	sum := api.Summary{
		Skipped:    map[string]int{},
		Classified: map[string]int{},
	}

	p.mu.RLock()
	clients := append([]*ClientEntry(nil), p.clients...)
	p.mu.RUnlock()

	settings := scan.Settings{
		SettleWindow: p.cfg.SettleWindow,
		Exclusions: scan.Exclusions{
			Tags: []string{"orphanarr-ignore", "orphanarr-filed"},
		},
	}
	rules := classify.DefaultRules()
	rules.AutoThreshold = p.cfg.AutoThreshold
	rules.ReviewThreshold = p.cfg.ReviewThresh
	rules.AmbiguityMargin = p.cfg.Ambiguity
	switch p.cfg.PDFDefault {
	case "ebook":
		rules.PDFDefault = media.Ebook
	case "comic":
		rules.PDFDefault = media.Comic
	default:
		rules.PDFDefault = media.Unknown
	}

	now := time.Now()
	var candidates []scan.Candidate

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
			// Cheap rejections before spending a request on the manifest.
			if it.Category != nil && strings.TrimSpace(*it.Category) != "" {
				sum.Skipped["SKIP_CATEGORIZED"]++
				continue
			}
			if !it.Complete {
				sum.Skipped["SKIP_INCOMPLETE"]++
				continue
			}

			files, err := e.Client.ListFiles(ctx, it.ID)
			if err != nil {
				sum.Skipped["SKIP_NO_METADATA"]++
				continue
			}

			key := e.Cfg.Name + "/" + string(it.ID)
			p.mu.Lock()
			seen, ok := p.first[key]
			if !ok {
				seen = now
				p.first[key] = seen
			}
			p.mu.Unlock()

			localPaths, _ := scan.ResolveLocal(e.Mapper, it, files)
			c := scan.Candidate{
				Client: e.Client, Item: it, Files: files,
				LocalPaths:  localPaths,
				Fingerprint: scan.Fingerprint(files),
				FirstSeen:   seen,
			}

			d := scan.Evaluate(c, settings, now)
			if !d.Keep {
				sum.Skipped[d.Code]++
				continue
			}
			candidates = append(candidates, c)
		}
	}

	// The cross-seed gate. Built over every candidate from every client,
	// because a cross-seed is by definition the same bytes reached twice.
	ov := scan.NewOverlap()
	for i, c := range candidates {
		ov.Add(i, c, p.fs)
	}

	claimed := map[int]bool{}
	for i, c := range candidates {
		if claimed[i] {
			continue
		}
		for _, peer := range ov.Peers(i, c) {
			// Overlaps among uncategorised candidates COLLAPSE into one
			// unit of work rather than producing two library entries
			// pointing at one payload (I4).
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
		// Probe this pair now, lazily. Reload cannot do it under identity
		// path-mapping: there are no rules to enumerate, and the source
		// root is only known once a scan has read a real save path.
		if len(fs.Files) > 0 && fs.Files[0].AbsPath != "" {
			p.prober.EnsureProbed(path.Dir(fs.Files[0].AbsPath), lib.Root)
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

		planID, err := p.savePlan(ctx, orphanID, cl.Type, res, needsReview, reason)
		if err != nil {
			sum.Errors = append(sum.Errors, err.Error())
			continue
		}

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
	res layout.Result, needsReview bool, reason string) (int64, error) {

	layout.SortPlanned(res.Files)

	pl := store.Plan{
		OrphanID: orphanID, MediaType: string(t), Status: "draft",
		NeedsReview: needsReview, ReviewReason: reason,
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
			Method: "copy", Bytes: f.Src.Size, SrcSize: f.Src.Size,
			Status: "pending",
		}
		if step.SrcPath == "" {
			step.SrcPath = f.Src.RelPath
		}
		if fi, err := p.fs.Stat(step.SrcPath); err == nil {
			step.SrcDev, step.SrcIno = fi.Dev, fi.Ino
			step.SrcSize, step.Bytes = fi.Size, fi.Size
			step.SrcMtime = fi.ModTime.UTC().Format(time.RFC3339Nano)
		}
		pl.Steps = append(pl.Steps, step)
		pl.CopyBytes += step.Bytes
		seq++
	}

	return p.db.SavePlan(ctx, pl)
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
		ok, _ := probe.Writable(l.Root)
		ls.Writable = ok

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
