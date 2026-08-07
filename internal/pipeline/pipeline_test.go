package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/client"
	"github.com/StevenGann/Orphanarr/internal/config"
	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/layout"
	"github.com/StevenGann/Orphanarr/internal/media"
	"github.com/StevenGann/Orphanarr/internal/pathmap"
	"github.com/StevenGann/Orphanarr/internal/scan"
	"github.com/StevenGann/Orphanarr/internal/store"
)

// fakeClient serves a fixed set of items, so the composition can be tested
// without HTTP. The adapter itself is covered by its own tests and by the
// end-to-end round trip.
type fakeClient struct {
	id    string
	items []client.Item
	files map[client.ExternalID][]client.File
	caps  client.Capabilities
}

func (f *fakeClient) ID() string   { return f.id }
func (f *fakeClient) Name() string { return "fake" }
func (f *fakeClient) Probe(context.Context) (client.Info, error) {
	return client.Info{AppVersion: "v5.0.0", Caps: f.caps}, nil
}
func (f *fakeClient) ListItems(context.Context) ([]client.Item, error) { return f.items, nil }
func (f *fakeClient) ListFiles(_ context.Context, id client.ExternalID) ([]client.File, error) {
	return f.files[id], nil
}
func (f *fakeClient) MarkFiled(context.Context, client.ExternalID, string) error { return nil }

func newPipeline(t *testing.T) (*Pipeline, *store.DB, *fsx.Guard, string) {
	t.Helper()
	base := t.TempDir()
	cfgDir := filepath.Join(base, "config")

	db, err := store.Open(context.Background(), filepath.Join(base, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	g := fsx.NewGuarded(fsx.NewOS())
	g.SetConfigRoot(cfgDir)

	c := config.Defaults()
	c.ConfigDir = cfgDir
	c.SettleWindow = 0
	return New(db, g, c), db, g, base
}

// I3's rootless detection. A torrent whose files share no common root makes
// content_path the entire save path, so operating on it touches every other
// download in the directory — the finding DESIGN §0 calls the first of the
// two the design turns on.
func TestSharesRoot(t *testing.T) {
	cases := []struct {
		name  string
		files []media.SourceFile
		want  bool
	}{
		{"single file", []media.SourceFile{{RelPath: "movie.mkv"}}, true},
		{"one common root", []media.SourceFile{
			{RelPath: "Release/a.mkv"}, {RelPath: "Release/Subs/b.srt"},
		}, true},
		{"two roots is ROOTLESS", []media.SourceFile{
			{RelPath: "A/a.mkv"}, {RelPath: "B/b.mkv"},
		}, false},
		{"a bare file beside a directory is ROOTLESS", []media.SourceFile{
			{RelPath: "a.mkv"}, {RelPath: "B/b.mkv"},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sharesRoot(c.files); got != c.want {
				t.Errorf("sharesRoot = %v, want %v", got, c.want)
			}
		})
	}
}

// toFileSet must keep c.Files and c.LocalPaths aligned. They are filtered
// by the same Wanted predicate over the same slice in the same order, and a
// misalignment would give a file the WRONG absolute path — which the
// executor would then copy.
func TestToFileSetAlignsAbsolutePaths(t *testing.T) {
	c := scan.Candidate{
		Item: client.Item{Name: "Release"},
		Files: []client.File{
			{RelPath: "Release/a.mkv", Size: 10, Wanted: true},
			{RelPath: "Release/skip.mkv", Size: 20, Wanted: false},
			{RelPath: "Release/b.mkv", Size: 30, Wanted: true},
		},
		// Built by ResolveLocal from the SAME filtered walk.
		LocalPaths: []string{"/data/t/Release/a.mkv", "/data/t/Release/b.mkv"},
	}

	fs := toFileSet(c)
	if len(fs.Files) != 2 {
		t.Fatalf("got %d files, want 2 (deselected files must be dropped)", len(fs.Files))
	}
	if fs.Files[0].AbsPath != "/data/t/Release/a.mkv" || fs.Files[0].Size != 10 {
		t.Errorf("file 0 = %+v", fs.Files[0])
	}
	if fs.Files[1].AbsPath != "/data/t/Release/b.mkv" || fs.Files[1].Size != 30 {
		t.Errorf("file 1 = %+v; a misalignment here copies the wrong bytes to the "+
			"right name", fs.Files[1])
	}
}

// A short LocalPaths slice must fail CLOSED to an empty AbsPath, never to
// an out-of-range panic or a neighbouring file's path.
func TestToFileSetFailsClosedOnShortLocalPaths(t *testing.T) {
	c := scan.Candidate{
		Item:       client.Item{Name: "R"},
		Files:      []client.File{{RelPath: "R/a.mkv", Wanted: true}, {RelPath: "R/b.mkv", Wanted: true}},
		LocalPaths: []string{"/data/R/a.mkv"},
	}
	fs := toFileSet(c)
	if fs.Files[1].AbsPath != "" {
		t.Errorf("AbsPath = %q, want empty — a missing mapping must not borrow "+
			"a neighbour's path", fs.Files[1].AbsPath)
	}
}

// A full scan: one uncategorised complete torrent becomes one orphan, one
// plan, and nothing on disk.
func TestScanProducesAPlanAndTouchesNothing(t *testing.T) {
	p, db, g, base := newPipeline(t)
	ctx := context.Background()

	dl := filepath.Join(base, "dl", "The.Matrix.1999.1080p.BluRay.x264-AMIABLE")
	lib := filepath.Join(base, "media", "movies")
	mustWrite(t, filepath.Join(dl, "the.matrix.1999.mkv"), "payload")
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
				SavePath: filepath.Join(base, "dl"), SizeBytes: 7,
			}},
			files: map[client.ExternalID][]client.File{
				"h1": {{RelPath: "The.Matrix.1999.1080p.BluRay.x264-AMIABLE/the.matrix.1999.mkv",
					Size: 7, Wanted: true}},
			},
		},
	}})
	p.SetLibraries([]layout.Library{movieLibrary(lib)})

	sum, err := p.ScanNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Orphans != 1 {
		t.Fatalf("orphans = %d, want 1 (skipped: %v, errors: %v)",
			sum.Orphans, sum.Skipped, sum.Errors)
	}

	plans, err := db.ListPlans(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}

	// A plan is inert.
	entries, _ := filepathGlob(filepath.Join(lib, "*"))
	if len(entries) != 0 {
		t.Errorf("scanning wrote %d entries into the library", len(entries))
	}

	// And a SECOND scan must not mint a duplicate plan.
	if _, err := p.ScanNow(ctx); err != nil {
		t.Fatal(err)
	}
	plans2, _ := db.ListPlans(ctx, "", 10)
	if len(plans2) != 1 {
		t.Errorf("a second scan produced %d plans; at the 15-minute default that "+
			"is 96 duplicate drafts a day for one item", len(plans2))
	}
}

// A categorised torrent is never a candidate, and its paths still enter the
// overlap index so the uncategorised half of a cross-seed is blocked.
func TestCategorisedTorrentIsSkipped(t *testing.T) {
	p, db, _, base := newPipeline(t)
	ctx := context.Background()

	cid, _ := db.SaveClient(ctx, store.Client{Name: "qb", BaseURL: "http://x", Enabled: true})
	cat := "tv-sonarr"
	p.SetClients(ctx, []*ClientEntry{{
		Cfg:    client.Config{ID: cid, Name: "qb"},
		Mapper: pathmap.Identity(),
		Client: &fakeClient{
			id:   itoa(cid),
			caps: client.Capabilities{Categories: true},
			items: []client.Item{{
				ID: "h1", Name: "Show.S01E01", Category: &cat, Complete: true,
				SavePath: filepath.Join(base, "dl"),
			}},
			files: map[client.ExternalID][]client.File{
				"h1": {{RelPath: "Show.S01E01/ep.mkv", Size: 5, Wanted: true}},
			},
		},
	}})

	sum, err := p.ScanNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Orphans != 0 {
		t.Errorf("a categorised torrent was treated as an orphan")
	}
	if sum.Skipped["SKIP_CATEGORIZED"] != 1 {
		t.Errorf("SKIP_CATEGORIZED = %d, want 1", sum.Skipped["SKIP_CATEGORIZED"])
	}
}

// A client that cannot express "no category" is never scanned (I14).
func TestClientWithoutCategoriesIsNeverScanned(t *testing.T) {
	p, db, _, _ := newPipeline(t)
	ctx := context.Background()

	cid, _ := db.SaveClient(ctx, store.Client{Name: "deluge", BaseURL: "http://x", Enabled: true})
	e := &ClientEntry{
		Cfg:    client.Config{ID: cid, Name: "deluge"},
		Mapper: pathmap.Identity(),
		Client: &fakeClient{id: itoa(cid), caps: client.Capabilities{Categories: false}},
	}
	p.SetClients(ctx, []*ClientEntry{e})

	if e.Scannable {
		t.Fatal("a client that cannot express categories was marked scannable; on a " +
			"stock Deluge every torrent reads as uncategorised, which under O1 is " +
			"the user's entire seeding library")
	}
	if e.Refusal == "" {
		t.Error("the refusal must carry a reason the user can act on")
	}
}
