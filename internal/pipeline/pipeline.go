// Package pipeline wires scan -> classify -> layout -> plan.
//
// It owns the ordering and nothing else: every decision it makes is
// delegated to a package that can be tested without it.
package pipeline

import (
	"context"
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
	db  *store.DB
	fs  fsx.FS
	cfg config.Config

	mu      sync.RWMutex
	clients []*ClientEntry
	libs    []layout.Library
	first   map[string]time.Time
}

func New(db *store.DB, f fsx.FS, cfg config.Config) *Pipeline {
	return &Pipeline{db: db, fs: f, cfg: cfg, first: map[string]time.Time{}}
}

// SetClients installs the constructed clients and probes each one.
//
// The probe is where I14 is enforced: a client that cannot express "no
// category" is marked unscannable here, at configure time, rather than
// being discovered mid-scan when it has already selected everything.
func (p *Pipeline) SetClients(ctx context.Context, entries []*ClientEntry) {
	for _, e := range entries {
		info, err := e.Client.Probe(ctx)
		e.Info, e.Err = info, err
		if err != nil {
			e.Scannable = false
			e.Refusal = err.Error()
			continue
		}
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

		if cl.Type == media.Unknown {
			p.db.LogEvent(ctx, store.Event{
				Code:    "CLASSIFY_UNKNOWN",
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
				Level: "warn", Code: "LAYOUT_REFUSED",
				Message: c.Item.Name + ": " + err.Error(),
			})
			continue
		}

		// I9: below the auto threshold, or multi/mixed, or rootless, or
		// the layout itself could not resolve — all route to review rather
		// than to a destination.
		needsReview := res.NeedsReview ||
			cl.Score < rules.AutoThreshold ||
			parsed.Confidence < rules.AutoThreshold ||
			cl.Cardinality != media.Single
		if needsReview {
			p.db.LogEvent(ctx, store.Event{
				Code: "PLAN_NEEDS_REVIEW",
				Message: fmt.Sprintf("%s -> %s (score %.2f, parse %.2f) %s",
					c.Item.Name, cl.Type, cl.Score, parsed.Confidence, res.ReviewReason),
			})
			continue
		}

		p.db.LogEvent(ctx, store.Event{
			Code:    "PLAN_READY",
			Message: fmt.Sprintf("%s -> %s (%d files)", c.Item.Name, cl.Type, len(res.Files)),
		})
	}

	p.db.LogEvent(ctx, store.Event{
		Code: "SCAN_COMPLETED",
		Message: fmt.Sprintf("%d items, %d orphans, %s",
			sum.Items, sum.Orphans, humanBytes(sum.Bytes)),
	})
	return sum, nil
}

// toFileSet materialises the classifier's input. Work units come from the
// client's manifest, never from a directory walk (I3).
func toFileSet(c scan.Candidate) media.FileSet {
	fs := media.FileSet{Name: c.Item.Name}
	for _, f := range c.Files {
		if !f.Wanted {
			continue
		}
		fs.Files = append(fs.Files, media.SourceFile{
			RelPath: f.RelPath,
			Size:    f.Size,
			Ext:     strings.ToLower(path.Ext(f.RelPath)),
		})
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
			ls.Writable = true
		}
		// Reported as three outcomes, not a boolean. Without the
		// read-only case the remediation banner tells a user running :ro
		// mounts to do what they have already done.
		ls.Hardlinks = "copy only — not probed"
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
