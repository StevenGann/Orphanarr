package fsx

// Round-03 implementation audit (fact-checker + devil's advocate,
// 2026-08-07), converted to regression guards.
//
// guard_test.go proves the Guard enforces I1/I6/I10/I12 once roots are
// registered. These ask the question that suite did not: is the Guard ever
// GIVEN the roots, and can it be un-given them. Both answers were wrong.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// I1 is enforced by matching against registered source roots, so a Guard
// that was never told about a download root cannot protect it.
//
// That was the shipped default: Reload registered a source root only from a
// client's explicit path mappings, and identity mapping — the documented
// common deployment, and what a single `-v /mnt/pool:/data` produces — has
// none. The invariant was unit-tested and unreachable in production.
//
// The fix registers each client's resolved save path during the scan. This
// test pins the Guard half: registration must actually arm the check, and
// I1 must still outrank library containment.
func TestI1_ArmedWhenTheSourceRootIsRegistered(t *testing.T) {
	base := t.TempDir()
	downloads := filepath.Join(base, "downloads")
	// The misconfiguration guard_test.go exists to defeat: a library root
	// that contains the download tree.
	library := base
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(downloads, "Some.Release", "movie.mkv")
	if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("seeding payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGuarded(NewOS())
	g.AddLibraryRoot(library)
	g.AddSourceRoot(downloads) // what the scan now does for every save path

	if err := g.Chmod(victim, 0o600); !errors.Is(err, ErrSourceRoot) {
		t.Fatalf("expected ErrSourceRoot, got %v — I1 must outrank the library "+
			"containment that would otherwise permit this write", err)
	}
}

// Library roots must be REPLACED on reload, not appended.
//
// Appending left a root the user deleted — or corrected after a typo —
// writable for the life of the process, and the slice grew on every
// settings save.
func TestLibraryRootsAreReplacedOnReset(t *testing.T) {
	base := t.TempDir()
	oldRoot := filepath.Join(base, "typo-media")
	newRoot := filepath.Join(base, "media")
	for _, d := range []string{oldRoot, newRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	g := NewGuarded(NewOS())
	g.AddLibraryRoot(oldRoot) // first Reload

	g.ResetLibraryRoots() // second Reload, after the user fixed the path
	g.AddLibraryRoot(newRoot)

	f, err := g.CreateExcl(filepath.Join(oldRoot, "x.mkv"), 0o644)
	if err == nil {
		f.Close()
		t.Fatal("a library root the user removed is still writable")
	}
	if !errors.Is(err, ErrOutsideLibrary) {
		t.Fatalf("expected ErrOutsideLibrary, got %v", err)
	}

	// And the replacement must work.
	f2, err := g.CreateExcl(filepath.Join(newRoot, "x.mkv"), 0o644)
	if err != nil {
		t.Fatalf("the current library root is not writable: %v", err)
	}
	f2.Close()
}

// Source roots accumulate rather than reset — forgetting one un-protects a
// download tree, while keeping a stale one only ever refuses more. But they
// must not accumulate DUPLICATES: the scan registers a save path on every
// pass, and an unbounded slice is a slow leak in a long-running process.
func TestSourceRootsDeduplicate(t *testing.T) {
	dir := t.TempDir()
	g := NewGuarded(NewOS())
	for i := 0; i < 100; i++ {
		g.AddSourceRoot(dir)
	}
	g.mu.RLock()
	n := len(g.sourceRoots)
	g.mu.RUnlock()
	if n != 1 {
		t.Fatalf("registered the same source root %d times; the scan calls this "+
			"once per item per scan and the slice would grow without bound", n)
	}
}

// ProbeWrite is I10's carve-out expressed AT the chokepoint. It must accept
// only the reserved probe filename, and only inside a registered root.
func TestProbeWriteIsNarrow(t *testing.T) {
	base := t.TempDir()
	lib := filepath.Join(base, "media")
	os.MkdirAll(lib, 0o755)

	g := NewGuarded(NewOS())
	g.AddLibraryRoot(lib)

	called := false
	if err := g.ProbeWrite(filepath.Join(lib, ".orphanarr-probe-src"),
		func(string) error { called = true; return nil }); err != nil {
		t.Fatalf("a correctly named probe inside a library root was refused: %v", err)
	}
	if !called {
		t.Fatal("ProbeWrite did not invoke its callback")
	}

	// Any other filename is a normal write and must not get the carve-out.
	if err := g.ProbeWrite(filepath.Join(lib, "movie.mkv"),
		func(string) error { t.Fatal("callback ran for a non-probe filename"); return nil }); err == nil {
		t.Fatal("ProbeWrite accepted an arbitrary filename")
	}

	// And it must not reach outside every registered root.
	if err := g.ProbeWrite(filepath.Join(base, ".orphanarr-probe-src"),
		func(string) error { t.Fatal("callback ran outside a registered root"); return nil }); err == nil {
		t.Fatal("ProbeWrite accepted a path outside every registered root")
	}
}
