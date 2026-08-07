package exec

// Round-03 implementation audit (fact-checker, 2026-08-07).
//
// Every test in this file asserts a property that the code, a comment, or
// DESIGN.md CLAIMS to hold. Where the claim does not hold the test is written
// to fail, so the claim and the code cannot drift apart again.

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// exec.go:212 claims: "A skip marks the PLAN partial, which is a visible,
// actionable state — not a success." pipeline/execute.go:99-102 reads that
// state back with `if s.Status == "skipped"`.
//
// Run() sets steps[i].Status = "done" unconditionally after any runStep that
// returns nil, and a collision-skip returns nil. The "skipped" marker is
// therefore destroyed before any caller can observe it.
func TestSkippedStepSurvivesRun(t *testing.T) {
	e, src, lib := newExec(t)

	writeFile(t, filepath.Join(src, "a.mkv"), "payload")
	dst := filepath.Join(lib, "Movie (2009)", "Movie (2009).mkv")
	writeFile(t, dst, "an existing file the user already had")

	steps := []Step{stepFor(t, filepath.Join(src, "a.mkv"), dst)}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatalf("collision under the default skip policy must not error: %v", err)
	}

	if steps[0].Status != "skipped" {
		t.Fatalf("step status is %q, want \"skipped\": Run() overwrote the marker that "+
			"pipeline.Execute reads to set a plan to `partial`, so a plan in which every "+
			"file collided is reported to the user as `done`", steps[0].Status)
	}
}

// A skipped step must not be reported as something rollback can undo.
func TestSkippedStepIsNotClaimedAsCreatedByUs(t *testing.T) {
	e, src, lib := newExec(t)

	writeFile(t, filepath.Join(src, "a.mkv"), "payload")
	dst := filepath.Join(lib, "Movie (2009)", "Movie (2009).mkv")
	writeFile(t, dst, "pre-existing")

	steps := []Step{stepFor(t, filepath.Join(src, "a.mkv"), dst)}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if steps[0].CreatedByUs {
		t.Fatal("a skipped step must never be created_by_us; rollback would delete the " +
			"user's own file")
	}
	// Rollback must leave the pre-existing file alone.
	e.Rollback(steps)
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("rollback removed a file Orphanarr did not create: %v", err)
	}
}

// I13 (DESIGN §10.1): "A copy is never published without re-stat'ing its
// source and proving src_size/src_mtime/src_dev/src_ino unchanged since the
// copy began."
//
// assertSourceUnchanged compares Size, Ino and ModTime. It never reads
// fi.Dev, so Step.SrcDev is captured at plan time (pipeline.savePlan:518),
// stored (store/schema.go), carried into the Step (pipeline/execute.go:50)
// and then never asserted.
func TestI13_AssertsSourceDevice(t *testing.T) {
	e, src, _ := newExec(t)
	p := filepath.Join(src, "a.mkv")
	writeFile(t, p, "payload")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)

	s := &Step{
		Src: p, SrcSize: fi.Size(), SrcMtime: fi.ModTime(),
		SrcIno: st.Ino,
		// A device that is not the one the plan recorded. In the real
		// failure this is the same path resolving to a different filesystem
		// after a mount changed under a plan that sat in the review queue.
		SrcDev: uint64(st.Dev) + 1,
	}
	if err := e.assertSourceUnchanged(s); err == nil {
		t.Fatal("I13 claims src_dev is proved unchanged before publish; " +
			"assertSourceUnchanged never reads FileInfo.Dev, so a source that moved to a " +
			"different filesystem between plan and publish is published anyway")
	}
}

// The three fields assertSourceUnchanged does check are only checked when
// they were populated. If the plan-time stat failed (pipeline.savePlan
// ignores the error from p.fs.Stat) every field is the zero value and I13
// silently degrades to nothing at all.
func TestI13_RefusesAStepWithNoRecordedSourceIdentity(t *testing.T) {
	e, src, _ := newExec(t)
	p := filepath.Join(src, "a.mkv")
	writeFile(t, p, "payload")

	// Exactly the Step that pipeline.savePlan produces when its Stat fails:
	// SrcSize/SrcIno/SrcDev zero and SrcMtime the zero time.
	s := &Step{Src: p}
	if err := e.assertSourceUnchanged(s); err == nil {
		t.Fatal("a step with no recorded source identity passes I13 vacuously; " +
			"savePlan discards the error from its plan-time Stat, so this Step is " +
			"reachable and the invariant becomes a no-op with no marker anywhere")
	}
}

// Positive control: the fields that ARE checked really are checked.
func TestI13_CatchesMtimeAndSizeChange(t *testing.T) {
	e, src, _ := newExec(t)
	p := filepath.Join(src, "a.mkv")
	writeFile(t, p, "payload")
	fi, _ := os.Stat(p)

	if err := e.assertSourceUnchanged(&Step{
		Src: p, SrcSize: fi.Size() + 1, SrcMtime: fi.ModTime(),
	}); err == nil {
		t.Fatal("size change not detected")
	}
	if err := e.assertSourceUnchanged(&Step{
		Src: p, SrcSize: fi.Size(), SrcMtime: fi.ModTime().Add(-time.Hour),
	}); err == nil {
		t.Fatal("mtime change not detected")
	}
}
