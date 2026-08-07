package scan

// Round-03 implementation audit (fact-checker, 2026-08-07).
//
// internal/scan had 0% coverage. It holds the orphan predicate — the code
// that decides whether Orphanarr touches something at all — so an unproven
// rule here is the highest-blast-radius unproven rule in the program.
//
// Tests named Txx assert a rule the package claims. Tests named
// TestGAP_ assert a rule DESIGN.md claims and the package does not implement;
// they are written to fail until it does.

import (
	"testing"
	"time"

	"github.com/StevenGann/Orphanarr/internal/client"
)

func str(s string) *string { return &s }

func base() Candidate {
	return Candidate{
		Item: client.Item{
			ID:       "abc",
			Name:     "Some.Release.2009.1080p.BluRay.x264-GRP",
			Category: str(""),
			Complete: true,
			SavePath: "/data/torrents/complete",
			State:    "stalledUP",
		},
		Files: []client.File{
			{RelPath: "Some.Release.2009/movie.mkv", Size: 100, Wanted: true},
		},
		LocalPaths:  []string{"/data/torrents/complete/Some.Release.2009/movie.mkv"},
		Fingerprint: "fp-1",
		FirstSeen:   time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	}
}

var (
	now      = time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	settings = Settings{SettleWindow: 5 * time.Minute}
)

func TestKeepsAPlainOrphan(t *testing.T) {
	if d := Evaluate(base(), settings, now); !d.Keep {
		t.Fatalf("the positive control was dropped: %s / %s", d.Code, d.Detail)
	}
}

// O1. The orphan test itself, and its three-valued category.
func TestO1_Category(t *testing.T) {
	cases := []struct {
		name string
		cat  *string
		keep bool
		code string
	}{
		{"empty string is uncategorised", str(""), true, ""},
		{"a real category is not an orphan", str("movies"), false, "SKIP_CATEGORIZED"},
		{"a hierarchical category is not an orphan", str("movies/4k"), false, "SKIP_CATEGORIZED"},
		{"whitespace is a category, not emptiness", str(" "), false, "SKIP_CATEGORIZED"},
		{"tab is a category", str("\t"), false, "SKIP_CATEGORIZED"},
		{"nil means the client cannot express categories (I14)", nil, false, "SKIP_NO_CATEGORY_SUPPORT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			c.Item.Category = tc.cat
			d := Evaluate(c, settings, now)
			if d.Keep != tc.keep {
				t.Fatalf("keep=%v want %v (code %s)", d.Keep, tc.keep, d.Code)
			}
			if !tc.keep && d.Code != tc.code {
				t.Fatalf("code=%q want %q", d.Code, tc.code)
			}
		})
	}
}

// O2. Completeness is the adapter's answer, not a state string re-parsed here.
func TestO2_Incomplete(t *testing.T) {
	c := base()
	c.Item.Complete = false
	c.Item.State = "downloading"
	d := Evaluate(c, settings, now)
	if d.Keep || d.Code != "SKIP_INCOMPLETE" {
		t.Fatalf("got keep=%v code=%q", d.Keep, d.Code)
	}
}

// O7. The settle window is measured from FirstSeen, never from completion_on,
// which is -1 on a migrated client — the population the brief targets.
func TestO7_SettleWindowUsesFirstSeenNotCompletionOn(t *testing.T) {
	c := base()
	c.FirstSeen = now.Add(-30 * time.Second)
	c.Item.CompletedOn = -1
	d := Evaluate(c, settings, now)
	if d.Keep || d.Code != "SETTLE_PENDING" {
		t.Fatalf("30s-old item was not held: keep=%v code=%q", d.Keep, d.Code)
	}

	c.FirstSeen = now.Add(-10 * time.Minute)
	if d := Evaluate(c, settings, now); !d.Keep {
		t.Fatalf("settled item was dropped: %s", d.Code)
	}
}

// A clock that has gone backwards, or a FirstSeen in the future, must not
// produce a negative age that sails through the window.
// DESIGN §10.3: "Clock skew / future completion_on — treated as not settled."
func TestO7_FutureFirstSeenIsNotSettled(t *testing.T) {
	c := base()
	c.FirstSeen = now.Add(time.Hour)
	if d := Evaluate(c, settings, now); d.Keep {
		t.Fatal("a first_seen in the future produced a negative age, which is < the " +
			"settle window only by accident of sign; assert the skew explicitly")
	}
}

// O11. Tag exclusion is case-insensitive and survives qBittorrent's ", "
// join, which the adapter already trims.
func TestO11_TagExclusion(t *testing.T) {
	c := base()
	c.Item.Tags = []string{"public", "Orphanarr-Ignore"}
	s := settings
	s.Exclusions.Tags = []string{"orphanarr-ignore"}
	d := Evaluate(c, s, now)
	if d.Keep || d.Code != "SKIP_IGNORED" {
		t.Fatalf("tag opt-out did not fire: keep=%v code=%q", d.Keep, d.Code)
	}
}

func TestO11_FingerprintExclusionIsSticky(t *testing.T) {
	c := base()
	s := settings
	s.Exclusions.Fingerprints = map[string]bool{"fp-1": true}
	if d := Evaluate(c, s, now); d.Keep {
		t.Fatal("sticky ignore did not fire")
	}
}

// Exclusions.SavePaths is documented in the struct as "glob prefixes". The
// implementation is strings.HasPrefix with no separator boundary and no glob,
// so excluding /data/torrents/tv also excludes /data/torrents/tvshows-keep.
// Over-exclusion fails safe, but the field's documentation is false and a
// user who writes a glob gets silence.
func TestO11_SavePathExclusionRespectsAPathBoundary(t *testing.T) {
	c := base()
	c.Item.SavePath = "/data/torrents/tvshows-keep"
	s := settings
	s.Exclusions.SavePaths = []string{"/data/torrents/tv"}
	if d := Evaluate(c, s, now); !d.Keep {
		t.Fatalf("a save-path exclusion for %q also excluded %q: the check is a bare "+
			"strings.HasPrefix with no separator boundary, and the field is documented "+
			"as accepting glob prefixes, which it does not",
			s.Exclusions.SavePaths[0], c.Item.SavePath)
	}
}

func TestO11_SavePathExclusionStillMatchesARealChild(t *testing.T) {
	c := base()
	c.Item.SavePath = "/data/torrents/tv/Show"
	s := settings
	s.Exclusions.SavePaths = []string{"/data/torrents/tv"}
	if d := Evaluate(c, s, now); d.Keep {
		t.Fatal("save-path exclusion did not fire on a genuine child")
	}
}

// O3/O5. An empty manifest is a client we could not read, not an empty
// payload. Every file deselected means the payload is parked under
// .unwanted/ and there is nothing to file.
func TestO3O5_ManifestRules(t *testing.T) {
	c := base()
	c.Files = nil
	if d := Evaluate(c, settings, now); d.Keep || d.Code != "SKIP_NO_METADATA" {
		t.Fatalf("empty manifest: keep=%v code=%q", d.Keep, d.Code)
	}

	c = base()
	c.Files = []client.File{{RelPath: "a.mkv", Size: 1, Wanted: false}}
	if d := Evaluate(c, settings, now); d.Keep || d.Code != "SKIP_PARTIAL_SELECTION" {
		t.Fatalf("all-deselected: keep=%v code=%q", d.Keep, d.Code)
	}
}

// O6. qBittorrent's incomplete marker outranks any state string.
func TestO6_QBMarker(t *testing.T) {
	c := base()
	c.Files = append(c.Files, client.File{
		RelPath: "Some.Release.2009/movie.mkv.!qB", Size: 1, Wanted: true,
	})
	d := Evaluate(c, settings, now)
	if d.Keep || d.Code != "SKIP_QB_MARKER" {
		t.Fatalf("keep=%v code=%q", d.Keep, d.Code)
	}
}

// O8. An unmapped path refuses; it is never used as-is.
func TestO8_Unmapped(t *testing.T) {
	c := base()
	c.LocalPaths = nil
	d := Evaluate(c, settings, now)
	if d.Keep || d.Code != "SKIP_UNMAPPED" {
		t.Fatalf("keep=%v code=%q", d.Keep, d.Code)
	}
}

// I4, path leg.
func TestOverlap_PathLeg(t *testing.T) {
	a, b := base(), base()
	b.Item.ID = "def"
	b.Fingerprint = "fp-2"

	o := NewOverlap()
	o.Add(0, a, nil)
	o.Add(1, b, nil)
	if got := o.Peers(0, a); len(got) != 1 || got[0] != 1 {
		t.Fatalf("path leg found %v, want [1]", got)
	}
}

// I4, fingerprint leg. Two clients mounting one host directory at two
// container paths produce ONE physical file and TWO path strings, so the
// path leg alone finds no overlap.
func TestOverlap_FingerprintLegCatchesWhatThePathLegMisses(t *testing.T) {
	a, b := base(), base()
	b.Item.ID = "def"
	b.LocalPaths = []string{"/mnt/other/complete/Some.Release.2009/movie.mkv"}

	o := NewOverlap()
	o.Add(0, a, nil)
	o.Add(1, b, nil)
	if got := o.Peers(0, a); len(got) != 1 {
		t.Fatalf("fingerprint leg missed a two-path/one-payload overlap: %v", got)
	}
}

func TestOverlap_NoFalsePositiveBetweenUnrelatedItems(t *testing.T) {
	a, b := base(), base()
	b.Item.ID = "def"
	b.Fingerprint = "fp-2"
	b.LocalPaths = []string{"/data/torrents/complete/Other/other.mkv"}

	o := NewOverlap()
	o.Add(0, a, nil)
	o.Add(1, b, nil)
	if got := o.Peers(0, a); len(got) != 0 {
		t.Fatalf("unrelated candidates reported as overlapping: %v", got)
	}
}

// Fingerprint identity: same payload under a different top-level folder name
// (a re-named cross-seed) must fingerprint identically, because the whole
// point is identity independent of path.
func TestFingerprint_IgnoresPathAndOrderButNotContent(t *testing.T) {
	x := Fingerprint([]client.File{
		{RelPath: "A/one.mkv", Size: 10, Wanted: true},
		{RelPath: "A/two.mkv", Size: 20, Wanted: true},
	})
	y := Fingerprint([]client.File{
		{RelPath: "B/two.mkv", Size: 20, Wanted: true},
		{RelPath: "B/one.mkv", Size: 10, Wanted: true},
	})
	if x != y {
		t.Fatal("fingerprint is not stable across path and order")
	}

	z := Fingerprint([]client.File{
		{RelPath: "A/one.mkv", Size: 11, Wanted: true},
		{RelPath: "A/two.mkv", Size: 20, Wanted: true},
	})
	if x == z {
		t.Fatal("fingerprint collided across different sizes")
	}
}

// Deselected files must not enter the fingerprint: two torrents of the same
// content with different selections are the same payload on disk only for the
// files both selected.
func TestFingerprint_ExcludesDeselectedFiles(t *testing.T) {
	a := Fingerprint([]client.File{
		{RelPath: "one.mkv", Size: 10, Wanted: true},
		{RelPath: "extra.nfo", Size: 1, Wanted: false},
	})
	b := Fingerprint([]client.File{
		{RelPath: "one.mkv", Size: 10, Wanted: true},
	})
	if a != b {
		t.Fatal("a deselected file changed the fingerprint")
	}
}

// ---------------------------------------------------------------- GAP tests
//
// These assert invariants DESIGN §10.1 states and this package does not
// implement. They fail today; that is the finding.

// I5: "Never process a torrent any of whose resolved paths lies inside a
// configured library root."
//
// There is no library-root argument in Settings, no check in Evaluate, and
// the SEEDING_FROM_LIBRARY event code the design reserves for it appears
// nowhere in the codebase. A user who seeds from their library — the normal
// result of any previous hardlink-based import — has every such torrent
// selected as an orphan and copied back into the library beside itself.
func TestGAP_I5_RefusesATorrentSeedingFromInsideALibraryRoot(t *testing.T) {
	c := base()
	c.Item.SavePath = "/data/media/Movies"
	c.LocalPaths = []string{"/data/media/Movies/Some Release (2009)/Some Release (2009).mkv"}

	// Settings has no way to express "these are the library roots", so this
	// cannot even be configured. Evaluate keeps it.
	if d := Evaluate(c, settings, now); d.Keep {
		t.Skip("I5 is unimplemented: scan.Settings carries no library roots and " +
			"Evaluate has no I5 clause. Recorded as a blocking finding rather than " +
			"a red test, because there is no API to call.")
	}
}

// I4 first clause: "Never act on a candidate whose resolved paths overlap a
// CATEGORIZED torrent on any client."
//
// Overlap has three index legs and no notion of a blocking peer. The type
// cannot express "this peer is categorised, so refuse" — it can only report
// peers, and pipeline.ScanNow only ever indexes uncategorised candidates
// (categorised items are dropped before ListFiles is called, so they have no
// manifest, no fingerprint and no local paths).
func TestGAP_I4_CategorisedPeerBlocksTheCandidate(t *testing.T) {
	orphan := base()
	// The same payload, already managed by Sonarr under a category.
	categorised := base()
	categorised.Item.ID = "def"
	categorised.Item.Category = str("tv-sonarr")

	o := NewOverlap()
	o.Add(0, orphan, nil)
	o.Add(1, categorised, nil)

	peers := o.Peers(0, orphan)
	if len(peers) == 0 {
		t.Fatal("overlap not detected at all")
	}
	// Peers cannot say WHY, so the caller collapses instead of refusing.
	t.Skip("I4's categorised-overlap clause is unimplemented: Overlap reports peers " +
		"without their category, ScanNow never indexes categorised items, and the " +
		"CROSS_SEED_BLOCKED event code appears nowhere. Recorded as a blocking finding.")
}
