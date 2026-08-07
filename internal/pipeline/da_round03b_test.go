package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/client"
	"github.com/StevenGann/Orphanarr/internal/layout"
	"github.com/StevenGann/Orphanarr/internal/pathmap"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// oneOrphan wires a pipeline holding exactly one uncategorised, complete
// torrent whose payload lives in its own subfolder — the ordinary shape.
func oneOrphan(t *testing.T, p *Pipeline, db *store.DB, g interface{ AddLibraryRoot(string) }, base string) (dl, lib, src string) {
	t.Helper()
	ctx := context.Background()

	dl = filepath.Join(base, "dl")
	src = filepath.Join(dl, "The.Matrix.1999.1080p.BluRay.x264-AMIABLE", "the.matrix.1999.mkv")
	lib = filepath.Join(base, "media", "movies")
	mustWrite(t, src, "payload")
	mustMkdir(t, lib)
	g.AddLibraryRoot(lib)

	cid, err := db.SaveClient(ctx, store.Client{Name: "qb", BaseURL: "http://x", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	p.SetClients(ctx, []*ClientEntry{{
		Cfg:    client.Config{ID: cid, Name: "qb"},
		Mapper: pathmap.Identity(),
		Client: &fakeClient{
			id:   itoa(cid),
			caps: client.Capabilities{Categories: true, Tags: true, FileList: true},
			items: []client.Item{{
				ID: "h1", Name: "The.Matrix.1999.1080p.BluRay.x264-AMIABLE",
				Category: &empty, Complete: true,
				SavePath: dl, SizeBytes: 7,
			}},
			files: map[client.ExternalID][]client.File{
				"h1": {{RelPath: "The.Matrix.1999.1080p.BluRay.x264-AMIABLE/the.matrix.1999.mkv",
					Size: 7, Wanted: true}},
			},
		},
	}})
	p.SetLibraries([]layout.Library{movieLibrary(lib)})
	return dl, lib, src
}

// DA-10: savePlan and Execute derive the prober cache key differently, so
// a plan that promises a hardlink is always executed as a copy.
//
// The V2 fix moved savePlan onto Guard.SourceRootFor(...) — the registered
// download ROOT — which is correct. Execute still asks
// prober.Get(path.Dir(steps[0].Src), ...) — the torrent's own subfolder.
// Those keys differ for every torrent that has its own folder, i.e. very
// nearly all of them, so the cache lookup misses, AllowLink stays false,
// and runStep's `AllowLink && Method == MethodLink` gate cannot open.
//
// Same user-visible outcome as B3: a full copy where link(2) was provably
// available. Worse in one respect — the stored plan now says "hardlink"
// and reports LinkBytes, so the UI's "0 bytes to copy" is a promise the
// executor silently breaks.
func TestDA10_PlanSaysHardlinkAndExecuteAlwaysCopies(t *testing.T) {
	p, db, g, base := newPipeline(t)
	ctx := context.Background()

	cfg := p.Config()
	cfg.Mode = "link_or_copy"
	cfg.DryRun = false
	p.SetConfig(cfg)

	_, _, src := oneOrphan(t, p, db, g, base)

	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	plans, err := db.ListPlans(ctx, "", 10)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%d err=%v", len(plans), err)
	}
	full, err := db.GetPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Steps[0].Method != "hardlink" {
		t.Skipf("the probe did not report Available in this environment "+
			"(method=%q); the key mismatch is only observable when it does",
			full.Steps[0].Method)
	}

	if err := p.Execute(ctx, full.ID); err != nil {
		t.Fatalf("execute: %v", err)
	}

	after, err := db.GetPlan(ctx, full.ID)
	if err != nil {
		t.Fatal(err)
	}
	si, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(after.Steps[0].DstPath)
	if err != nil {
		t.Fatalf("destination missing: %v", err)
	}

	if !os.SameFile(si, di) {
		t.Errorf("DA-10 CONFIRMED: the plan recorded method=%q and the executor "+
			"recorded method_actual=%q — a full copy. savePlan cached the probe "+
			"under the registered root; Execute looks it up under "+
			"path.Dir(steps[0].Src), the torrent's own subfolder, so the lookup "+
			"always misses and AllowLink is never true.",
			after.Steps[0].Method, after.Steps[0].MethodActual)
	}
}

// DA-11: restricting OpenPlanFor to 'draft' fixed V1 and reopened NB-4 for
// the one population that hits it.
//
// An orphan whose plan FAILED — DESIGN §10.3 calls ENOSPC mid-copy "now the
// primary failure path, not an edge case" — is no longer matched, so every
// scan mints a fresh draft. At the 15-minute default that is 96 new
// executable plans a day for a single item, each of which restarts at step
// 0 because its own steps are all `pending`: under collision: suffix,
// executing one duplicates every file the failed attempt already placed,
// which is exactly the harm the resume filter was added to prevent.
func TestDA11_AFailedPlanMintsANewDraftOnEveryScan(t *testing.T) {
	p, db, g, base := newPipeline(t)
	ctx := context.Background()

	oneOrphan(t, p, db, g, base)

	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	plans, _ := db.ListPlans(ctx, "", 10)
	if len(plans) != 1 {
		t.Fatalf("setup: %d plans", len(plans))
	}
	// The plan is executed and fails part-way, as ENOSPC does.
	if err := db.SetPlanStatus(ctx, plans[0].ID, "failed", "ENOSPC"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := db.ListPlans(ctx, "", 10)
	if len(after) != 1 {
		t.Errorf("DA-11 CONFIRMED: %d plans for one orphan after a single further "+
			"scan. A failed plan is no longer reusable (correctly — reuse erased "+
			"its undo record) but nothing suppresses a duplicate either, so the "+
			"review queue refills with the same item every scan interval.",
			len(after))
	}
}

// DA-12: a dry-run scan poisons the prober cache, and turning dry-run off
// does not clear it.
//
// ProbeWrite now refuses in dry-run — correct. But probePair treats the
// refusal as a probe RESULT: outcome SeparateMnt, with the detail that
// tells the user to consolidate their -v mounts. Probe() caches it, nothing
// invalidates it, and ApplyConfig installs the new settings without
// re-probing. So the natural configuration order — set link_or_copy, review
// plans in dry-run, then turn dry-run off — leaves hardlinking disabled and
// the UI blaming the user's mount topology for a refusal Orphanarr issued
// to itself. DESIGN §6.7: failing closed with a wrong explanation is not
// failing closed.
func TestDA12_DryRunPoisonsTheProbeCache(t *testing.T) {
	p, db, g, base := newPipeline(t)
	ctx := context.Background()

	cfg := p.Config()
	cfg.Mode = "link_or_copy"
	cfg.DryRun = true
	p.SetConfig(cfg)

	oneOrphan(t, p, db, g, base)

	if _, err := p.ScanNow(ctx); err != nil { // caches the refusal
		t.Fatal(err)
	}

	// The user turns dry-run off in Settings; ApplyConfig installs it.
	cfg.DryRun = false
	p.SetConfig(cfg)

	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	plans, _ := db.ListPlans(ctx, "", 10)
	full, err := db.GetPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if full.Steps[0].Method != "hardlink" {
		t.Errorf("DA-12 CONFIRMED: method is %q after dry-run was turned off. "+
			"The cached outcome is the dry-run refusal, reported to the user as "+
			"\"copy only — separate mounts\" with advice to change their mounts.",
			full.Steps[0].Method)
	}
}
