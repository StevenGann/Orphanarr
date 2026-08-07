package fsx

import (
	"os"
	"path/filepath"
	"testing"
)

// DA-9 (round-03 regression): ProbeWrite is the one mutating entry point on
// the port that ignores dry-run.
//
// The round-03 fix moved the probe behind the guard, which is right, and
// then gated "do not probe in dry-run" at ONE of the two call sites —
// probeAllPairs skips when dry, savePlan's EnsureProbed does not. The
// chokepoint itself carries no such rule, so the discipline the port exists
// to replace is back, in the same function that removed it.
//
// I10: "In dry-run, zero write syscalls occur outside /config — except
// user-initiated capability probes ... which write a single named probe
// file into a LIBRARY ROOT and remove it." A scheduled scan is not
// user-initiated and a download root is not a library root.
func TestDA9_ProbeWriteIgnoresDryRun(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "downloads", "Some.Movie.2019-GRP")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	g := NewGuarded(NewOS())
	g.AddSourceRoot(filepath.Join(root, "downloads"))
	g.SetDryRun(true)

	probe := filepath.Join(src, ProbePrefix+"-src")
	err := g.ProbeWrite(probe, func(real string) error {
		f, err := os.OpenFile(real, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		return f.Close()
	})
	if err == nil {
		t.Errorf("DA-9 CONFIRMED: the port created %s inside a registered "+
			"download root with dry-run engaged. Every other mutating method "+
			"on this Guard consults g.dryRun; this one does not, so whether "+
			"I10 holds depends on each caller remembering.", probe)
	}
	if _, statErr := os.Stat(probe); statErr == nil {
		os.Remove(probe)
	}
}

// And the same for a library root: a dry-run scan must not create files a
// media server will index, however briefly.
func TestDA9b_ProbeWriteIgnoresDryRunInALibraryRoot(t *testing.T) {
	root := t.TempDir()
	g := NewGuarded(NewOS())
	g.AddLibraryRoot(root)
	g.SetDryRun(true)

	called := false
	err := g.ProbeWrite(filepath.Join(root, ProbePrefix+"-write"),
		func(string) error { called = true; return nil })
	if err == nil || called {
		t.Errorf("ProbeWrite ran its callback in dry-run (called=%v, err=%v)", called, err)
	}
}
