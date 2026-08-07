package exec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/fsx"
)

func daFS(t *testing.T) (*fsx.Guard, string, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "downloads")
	dst := filepath.Join(root, "media")
	os.MkdirAll(src, 0o755)
	os.MkdirAll(dst, 0o755)
	g := fsx.NewGuarded(fsx.NewOS())
	g.AddSourceRoot(src)
	g.AddLibraryRoot(dst)
	return g, src, dst
}

// DA-1: a collision routed to `skip` is reported as `done`.
//
// runStep sets Status="skipped" and returns nil; Run then unconditionally
// overwrites it with "done". pipeline.Execute's partial detection tests for
// "skipped", so it can never fire, and the plan is reported complete.
func TestDA1_SkipIsReportedAsDone(t *testing.T) {
	g, srcRoot, dstRoot := daFS(t)

	src := filepath.Join(srcRoot, "movie.mkv")
	os.WriteFile(src, []byte("ours"), 0o644)

	dst := filepath.Join(dstRoot, "movie.mkv")
	os.WriteFile(dst, []byte("THE USER'S EXISTING FILE"), 0o644)

	e := New(g, nil, DefaultOptions()) // Collision: "skip"
	steps := []Step{{Seq: 0, Src: src, Dst: dst, Method: MethodCopy, SrcSize: 4}}

	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatalf("run: %v", err)
	}
	if steps[0].Status != "skipped" {
		t.Errorf("DA-1 CONFIRMED: step status is %q, want \"skipped\" — "+
			"the destination already existed and nothing was placed, "+
			"but the plan will be recorded as done", steps[0].Status)
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "THE USER'S EXISTING FILE" {
		t.Fatalf("destination was clobbered: %q", b)
	}
}

// DA-2: Rollback removes whatever is at Dst, with no identity check.
//
// DESIGN §6.7: "for a hardlink, confirm (dev, ino) still matches what was
// recorded, then Remove(dst). For a copy, confirm size and mtime. Undo is
// disabled with a stated reason where it cannot be proven safe."
// Rollback checks only the created_by_us flag.
func TestDA2_UndoDeletesAFileTheUserReplaced(t *testing.T) {
	g, srcRoot, dstRoot := daFS(t)

	src := filepath.Join(srcRoot, "movie.mkv")
	os.WriteFile(src, []byte("720p"), 0o644)
	dst := filepath.Join(dstRoot, "movie.mkv")

	e := New(g, nil, DefaultOptions())
	steps := []Step{{Seq: 0, Src: src, Dst: dst, Method: MethodCopy, SrcSize: 4}}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !steps[0].CreatedByUs {
		t.Fatal("expected created_by_us")
	}

	// The user replaces the placed file with their own 2160p remux, at the
	// same path. Different size, different mtime, different inode.
	os.Remove(dst)
	os.WriteFile(dst, []byte("2160p REMUX THE USER SPENT A WEEK ON"), 0o644)

	// Steps as Undo now rebuilds them: the FULL identity, not just paths.
	// Dropping SrcSize here was itself the bug — it left Rollback nothing
	// to verify against.
	undo := []Step{{Seq: 0, Src: src, Dst: dst, CreatedByUs: true, SrcSize: 4}}
	errs := e.Rollback(undo)
	if len(errs) == 0 {
		t.Error("Rollback silently removed a file that no longer matches what " +
			"was placed; §6.7 requires it to refuse and surface instead")
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Fatal("Undo deleted the user's replacement file. The recorded size " +
			"did not match, and §6.7 requires that check before this unlink.")
	}
	b, _ := os.ReadFile(dst)
	if string(b) != "2160p REMUX THE USER SPENT A WEEK ON" {
		t.Fatalf("the user's file was modified: %q", b)
	}
}

// And the converse: a destination that IS still what we placed is removed,
// or Undo would never work at all.
func TestUndoRemovesAnUnchangedPlacement(t *testing.T) {
	g, srcRoot, dstRoot := daFS(t)
	src := filepath.Join(srcRoot, "movie.mkv")
	os.WriteFile(src, []byte("720p"), 0o644)
	dst := filepath.Join(dstRoot, "movie.mkv")

	e := New(g, nil, DefaultOptions())
	steps := []Step{{Seq: 0, Src: src, Dst: dst, Method: MethodCopy, SrcSize: 4}}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatalf("run: %v", err)
	}
	if errs := e.Rollback(steps); len(errs) > 0 {
		t.Fatalf("rollback refused an unchanged placement: %v", errs)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("Undo left a file it placed and that was unchanged")
	}
}

// Rollback must also refuse when there is NOTHING to verify against.
// "Disabled with a stated reason where it cannot be proven safe" means
// disabled, not defaulted to yes.
func TestUndoRefusesWithoutARecordedSize(t *testing.T) {
	g, _, dstRoot := daFS(t)
	dst := filepath.Join(dstRoot, "movie.mkv")
	os.WriteFile(dst, []byte("something"), 0o644)

	e := New(g, nil, DefaultOptions())
	errs := e.Rollback([]Step{{Seq: 0, Dst: dst, CreatedByUs: true}})
	if len(errs) == 0 {
		t.Fatal("Rollback deleted a file with no recorded size to check against")
	}
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Fatal("the file was removed despite being unverifiable")
	}
}

// DA-3: Method is the only gate on hardlinking, and pipeline.savePlan
// hardcodes it to "copy". AllowLink is therefore unreachable.
func TestDA3_AllowLinkIsUnreachableWhenMethodIsCopy(t *testing.T) {
	g, srcRoot, dstRoot := daFS(t)
	src := filepath.Join(srcRoot, "movie.mkv")
	os.WriteFile(src, []byte("bytes"), 0o644)
	dst := filepath.Join(dstRoot, "movie.mkv")

	opts := DefaultOptions()
	opts.AllowLink = true // probe passed: link(2) is provably available

	e := New(g, nil, opts)
	// pipeline.savePlan now derives Method from ops__mode and the per-pair
	// probe, so a plan built where the probe passed carries MethodLink.
	// It used to hardcode "copy", which made AllowLink, the probe's
	// Available outcome and the whole three-valued badge inert.
	steps := []Step{{Seq: 0, Src: src, Dst: dst, Method: MethodLink, SrcSize: 5}}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatalf("run: %v", err)
	}
	if steps[0].Actual != MethodLink {
		t.Errorf("AllowLink=true, source and destination are on one mount, "+
			"and the placement was %q — the hardlink path is unreachable.",
			steps[0].Actual)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dst)
	if os.SameFile(si, di) {
		t.Log("hardlinked after all")
	}
}
