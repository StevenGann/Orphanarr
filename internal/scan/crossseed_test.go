package scan

// Round-03 re-verification (fact-checker, 2026-08-07).
//
// B6's fix added Overlap.AddPeers for I4's categorised clause and
// Overlap.Transitive for the collapse. The collapse leg is now a transitive
// closure. The BLOCKING leg is not: AddPeers marks only candidates that
// overlap a categorised peer DIRECTLY.

import (
	"testing"
	"time"

	"github.com/StevenGann/Orphanarr/internal/client"
)

func cand(id, fp string, paths ...string) Candidate {
	c := base()
	c.Item.ID = client.ExternalID(id)
	c.Fingerprint = fp
	c.LocalPaths = paths
	return c
}

// I4: "Never act on a candidate whose resolved paths overlap a CATEGORIZED
// torrent on any client."
//
// One payload, three torrents:
//
//	P  categorised (Sonarr owns it)   path /data/t/shared/ep.mkv
//	B  uncategorised                  path /data/t/shared/ep.mkv   fp F
//	A  uncategorised                  path /data/t/a/ep.mkv        fp F
//
// A~B by fingerprint, B~P by path. All three are the same bytes, so I4
// forbids acting on A as much as on B. AddPeers marks only B.
func TestCrossSeedBlockIsTransitive(t *testing.T) {
	a := cand("a", "F", "/data/t/a/ep.mkv")
	b := cand("b", "F", "/data/t/shared/ep.mkv")
	categorised := cand("p", "", "/data/t/shared/ep.mkv")

	candidates := []Candidate{a, b}
	ov := NewOverlap()
	for i, c := range candidates {
		ov.Add(i, c, nil)
	}
	blocked := ov.AddPeers([]Candidate{categorised}, nil)

	if !blocked[1] {
		t.Fatal("B does not overlap the categorised torrent by path; the fixture is wrong")
	}
	if !blocked[0] {
		t.Fatal("A was NOT blocked. A shares a fingerprint with B, and B shares a path " +
			"with a categorised torrent, so all three are one payload an *arr already " +
			"owns. AddPeers marks only DIRECT overlaps, and ScanNow's loop `continue`s " +
			"on a blocked candidate without propagating the block to its peers — so " +
			"whichever member of the chain the loop reaches first and does not find " +
			"blocked gets planned, and under copy-only that is a full second copy of " +
			"already-imported content. Overlap.Transitive exists and is applied to the " +
			"COLLAPSE leg only; the BLOCKING leg needs the same closure.")
	}
}

// Reproduction of ScanNow's loop, to show the consequence rather than just
// the missing flag.
func TestScanLoopPlansAMemberOfABlockedCrossSeedChain(t *testing.T) {
	a := cand("a", "F", "/data/t/a/ep.mkv")
	b := cand("b", "F", "/data/t/shared/ep.mkv")
	categorised := cand("p", "", "/data/t/shared/ep.mkv")

	candidates := []Candidate{a, b}
	ov := NewOverlap()
	for i, c := range candidates {
		ov.Add(i, c, nil)
	}
	blocked := ov.AddPeers([]Candidate{categorised}, nil)

	// Verbatim from pipeline.ScanNow.
	claimed := map[int]bool{}
	var planned []int
	for i, c := range candidates {
		if claimed[i] {
			continue
		}
		if blocked[i] {
			continue
		}
		for _, peer := range ov.Transitive(i, candidates) {
			claimed[peer] = true
		}
		_ = c
		planned = append(planned, i)
	}

	if len(planned) != 0 {
		t.Fatalf("%d plan(s) produced (%v) for a payload a categorised torrent already "+
			"owns. Every candidate in the chain is the same bytes as the *arr-managed "+
			"copy; I4 requires zero.", len(planned), planned)
	}
}

// Positive control: a direct overlap IS blocked, and an unrelated candidate
// is not.
func TestCrossSeedBlockDirectAndNegative(t *testing.T) {
	b := cand("b", "F", "/data/t/shared/ep.mkv")
	unrelated := cand("u", "G", "/data/t/other/film.mkv")
	categorised := cand("p", "", "/data/t/shared/ep.mkv")

	candidates := []Candidate{b, unrelated}
	ov := NewOverlap()
	for i, c := range candidates {
		ov.Add(i, c, nil)
	}
	blocked := ov.AddPeers([]Candidate{categorised}, nil)

	if !blocked[0] {
		t.Fatal("direct path overlap with a categorised torrent was not blocked")
	}
	if blocked[1] {
		t.Fatal("an unrelated candidate was blocked")
	}
}

// I5 is now implemented; pin it, including the boundary case that the
// save-path exclusion fix also needed.
func TestI5_RefusesAPayloadInsideALibraryRoot(t *testing.T) {
	c := base()
	c.LocalPaths = []string{"/data/media/Movies/Some Release (2009)/Some Release (2009).mkv"}
	s := Settings{SettleWindow: 5 * time.Minute, LibraryRoots: []string{"/data/media/Movies"}}

	d := Evaluate(c, s, now)
	if d.Keep || d.Code != "SEEDING_FROM_LIBRARY" {
		t.Fatalf("keep=%v code=%q, want SEEDING_FROM_LIBRARY", d.Keep, d.Code)
	}
}

func TestI5_DoesNotFireOnASharedPrefix(t *testing.T) {
	c := base()
	c.LocalPaths = []string{"/data/media-staging/x/y.mkv"}
	s := Settings{SettleWindow: 5 * time.Minute, LibraryRoots: []string{"/data/media"}}

	if d := Evaluate(c, s, now); !d.Keep {
		t.Fatalf("/data/media-staging was treated as inside /data/media: %s", d.Code)
	}
}
