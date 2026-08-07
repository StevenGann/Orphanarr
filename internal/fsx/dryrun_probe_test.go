package fsx

// Round-03 re-verification (fact-checker, 2026-08-07).
//
// ProbeWrite is the B2 fix: it moves the capability probe onto the port so
// that "every mutating entry point funnels through one check" is true. Its
// own doc comment says the carve-out is now "expressed AT the chokepoint
// rather than as an end-run around it."
//
// The name check and the root check are at the chokepoint. The DRY-RUN half
// is not: it lives in pipeline.probeAllPairs, and pipeline.savePlan reaches
// the prober by a second route that does not pass it.

import (
	"os"
	"path/filepath"
	"testing"
)

// I10: "In dry-run, zero write syscalls occur outside /config — except
// user-initiated capability probes (§5.8 case-sensitivity, §6.3 hardlink,
// §8.3 writability), which write a single named probe file into A LIBRARY
// ROOT and remove it."
//
// A scheduled scan is not user-initiated. ProbeWrite cannot tell the two
// apart, so the rule has to be enforced by every caller remembering — which
// is the failure mode fsx exists to remove.
func TestProbeWrite_RefusesInDryRun(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "media")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewGuarded(NewOS())
	g.AddLibraryRoot(lib)
	g.SetDryRun(true)

	p := filepath.Join(lib, ProbePrefix+"-write")
	called := false
	err := g.ProbeWrite(p, func(real string) error {
		called = true
		f, e := os.OpenFile(real, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		return f.Close()
	})

	if called || err == nil {
		os.Remove(p)
		t.Fatal("ProbeWrite executed a write with dry-run engaged. The dry-run half " +
			"of I10's carve-out is enforced in pipeline.probeAllPairs, not here, and " +
			"pipeline.savePlan reaches the prober by a route that skips it — so a " +
			"SCHEDULED scan with ops__mode=link_or_copy writes probe files while " +
			"dry-run is on. Either gate ProbeWrite on dry-run, or give it an " +
			"explicit user-initiated argument that savePlan cannot supply.")
	}
}

// The B2 fix keyed probing on a REGISTERED SOURCE ROOT specifically so that
// nothing writes inside an individual torrent's data directory. ScanNow no
// longer does that — but savePlan does, via
// EnsureProbed(path.Dir(f.Src.AbsPath), lib.Root), and ProbeWrite permits it
// because a torrent directory is of course inside a registered source root.
//
// This pins the property the fix was for: a probe path must be a root, not
// an arbitrary directory beneath one.
func TestProbeWrite_RefusesInsideATorrentDirectory(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "downloads")
	torrentDir := filepath.Join(downloads, "Some.Release.2009.1080p-GRP")
	if err := os.MkdirAll(torrentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewGuarded(NewOS())
	g.AddSourceRoot(downloads)

	p := filepath.Join(torrentDir, ProbePrefix+"-src")
	err := g.ProbeWrite(p, func(real string) error {
		f, e := os.OpenFile(real, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		return f.Close()
	})
	if err == nil {
		os.Remove(p)
		t.Fatalf("ProbeWrite created %s INSIDE a seeding torrent's data directory. "+
			"ProbeWrite tests containment (inAnySource) but not that the path IS a "+
			"root, so pipeline.savePlan's EnsureProbed(path.Dir(firstFile), ...) "+
			"reintroduces exactly the write B2 removed — one file per orphan per "+
			"scan, plus one prober cache entry per torrent directory, unbounded.", p)
	}
}

// Positive control: the legitimate case still works.
func TestProbeWrite_AllowsANamedProbeInALibraryRootWhenNotDryRun(t *testing.T) {
	lib := t.TempDir()
	g := NewGuarded(NewOS())
	g.AddLibraryRoot(lib)

	p := filepath.Join(lib, ProbePrefix+"-write")
	if err := g.ProbeWrite(p, func(real string) error {
		f, e := os.OpenFile(real, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		return f.Close()
	}); err != nil {
		t.Fatalf("the permitted probe was refused: %v", err)
	}
	if err := g.ProbeWrite(p, func(real string) error { return os.Remove(real) }); err != nil {
		t.Fatalf("probe cleanup was refused: %v", err)
	}
}

// Positive control: the name rule holds.
func TestProbeWrite_RefusesAnUnreservedName(t *testing.T) {
	lib := t.TempDir()
	g := NewGuarded(NewOS())
	g.AddLibraryRoot(lib)
	if err := g.ProbeWrite(filepath.Join(lib, "movie.mkv"), func(string) error {
		return nil
	}); err == nil {
		t.Fatal("ProbeWrite accepted a non-probe filename")
	}
}
