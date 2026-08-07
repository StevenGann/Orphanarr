package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/layout"
)

func newExec(t *testing.T) (*Executor, string, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "torrents")
	lib := filepath.Join(base, "media")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}

	g := fsx.NewGuarded(fsx.NewOS())
	g.AddSourceRoot(src)
	g.AddLibraryRoot(lib)

	return New(g, nil, DefaultOptions()), src, lib
}

func writeFile(t *testing.T, p, content string) os.FileInfo {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func stepFor(t *testing.T, src, dst string) Step {
	t.Helper()
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	return Step{
		Src: src, Dst: dst, Method: MethodCopy,
		SrcSize: fi.Size(), SrcMtime: fi.ModTime(), Bytes: fi.Size(),
	}
}

func TestCopyPlacesFileAndLeavesSourceUntouched(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "movie.mkv")
	writeFile(t, s, "payload")

	step := stepFor(t, s, filepath.Join(lib, "Movie (2020)", "Movie (2020).mkv"))
	if err := e.Run(context.Background(), []Step{step}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(lib, "Movie (2020)", "Movie (2020).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("destination content = %q", got)
	}
	// I1: the source must be byte-identical and still present.
	if b, err := os.ReadFile(s); err != nil || string(b) != "payload" {
		t.Fatalf("source was disturbed: %v %q", err, b)
	}
}

// The partial must not survive a successful publish, and must never be
// visible at the final name.
func TestNoPartialSurvivesASuccessfulCopy(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "a.mkv")
	writeFile(t, s, "data")
	dst := filepath.Join(lib, "A", "a.mkv")

	if err := e.Run(context.Background(), []Step{stepFor(t, s, dst)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + layout.PartialSuffix); !os.IsNotExist(err) {
		t.Fatal("partial survived a successful publish")
	}
}

// #C9's lesson, asserted at the executor. rename(2) would destroy this
// file silently; the publish ladder must not.
func TestPublishNeverClobbersAnExistingDestination(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "new.mkv")
	writeFile(t, s, "new content")

	dst := filepath.Join(lib, "existing.mkv")
	writeFile(t, dst, "IRREPLACEABLE")

	opts := DefaultOptions()
	opts.Collision = "fail"
	e.opts = opts

	err := e.Run(context.Background(), []Step{stepFor(t, s, dst)})
	if !errors.Is(err, ErrCollision) {
		t.Fatalf("expected ErrCollision, got %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "IRREPLACEABLE" {
		t.Fatalf("pre-existing destination was destroyed: %q", got)
	}
	if _, err := os.Stat(dst + layout.PartialSuffix); !os.IsNotExist(err) {
		t.Error("a refused publish left its partial behind")
	}
}

// I13. This is the failure that did not exist under hardlinking: link(2)
// never opened the source, and a copy holds a live file open for minutes.
func TestSourceChangedDuringCopyIsRefused(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "live.mkv")
	writeFile(t, s, "original")

	step := stepFor(t, s, filepath.Join(lib, "live.mkv"))
	// Simulate the source having changed since the plan was built.
	step.SrcSize = 999

	err := e.Run(context.Background(), []Step{step})
	if !errors.Is(err, ErrSourceChanged) && !strings.Contains(fmt.Sprint(err), "short copy") {
		t.Fatalf("expected a source-changed or short-copy refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib, "live.mkv")); !os.IsNotExist(err) {
		t.Fatal("a file was published despite the source not matching the plan")
	}
}

// A skip is not a success. It must leave nothing behind.
func TestCollisionSkipLeavesNoDebris(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "x.mkv")
	writeFile(t, s, "x")
	dst := filepath.Join(lib, "x.mkv")
	writeFile(t, dst, "already here")

	steps := []Step{stepFor(t, s, dst)}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst + layout.PartialSuffix); !os.IsNotExist(err) {
		t.Error("skip left a partial behind")
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "already here" {
		t.Errorf("skip modified the destination: %q", got)
	}
}

// Rollback removes only what we created and returns the filesystem to its
// pre-plan state. It must never touch a directory the user already had.
func TestRollbackRemovesOnlyWhatWeCreated(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "a.mkv")
	writeFile(t, s, "a")

	preexisting := filepath.Join(lib, "TV")
	if err := os.Mkdir(preexisting, 0o755); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(preexisting, "user-put-this-here.txt")
	writeFile(t, userFile, "mine")

	dst := filepath.Join(preexisting, "Show", "Season 01", "a.mkv")
	steps := []Step{stepFor(t, s, dst)}
	if err := e.Run(context.Background(), steps); err != nil {
		t.Fatal(err)
	}

	if errs := e.Rollback(steps); len(errs) != 0 {
		t.Fatalf("rollback errors: %v", errs)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("rollback did not remove the file it created")
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Error("rollback removed a file the user already had")
	}
	if _, err := os.Stat(preexisting); err != nil {
		t.Error("rollback removed a directory it did not create")
	}
}

// Reconcile's partial sweep must be unconditional: link+unlink leaves BOTH
// names for a window, so a crash inside it leaves a dst that verifies with
// the partial still on disk. As an elif the sweep would never run.
func TestReconcileSweepsPartialEvenWhenDestinationVerifies(t *testing.T) {
	e, _, lib := newExec(t)
	dst := filepath.Join(lib, "done.mkv")
	writeFile(t, dst, "12345")
	writeFile(t, dst+layout.PartialSuffix, "12345")

	steps := []Step{{Dst: dst, SrcSize: 5, Status: "in_progress"}}
	if errs := e.Reconcile(steps); len(errs) != 0 {
		t.Fatalf("reconcile errors: %v", errs)
	}
	if steps[0].Status != "done" {
		t.Errorf("status = %q, want done", steps[0].Status)
	}
	if _, err := os.Stat(dst + layout.PartialSuffix); !os.IsNotExist(err) {
		t.Fatal("partial survived Reconcile; the sweep is not unconditional")
	}
}

// A destination that does not verify AND that the journal says we created
// is removed, and the step re-queued. Leaving a short file where a scanner
// indexes it is the failure this prevents.
//
// CreatedByUs is set explicitly here. The original version of this test
// left it at its zero value and still asserted the file was deleted, which
// pinned a real bug in place: Reconcile would delete a destination it had
// no record of creating. A fixture's zero values are part of its
// preconditions, and an unset one is where a missing check hides.
func TestReconcileRemovesUnverifiableDestinationWeCreated(t *testing.T) {
	e, _, lib := newExec(t)
	dst := filepath.Join(lib, "short.mkv")
	writeFile(t, dst, "12")

	steps := []Step{{Dst: dst, SrcSize: 100, Status: "in_progress", CreatedByUs: true}}
	e.Reconcile(steps)

	if steps[0].Status != "pending" {
		t.Errorf("status = %q, want pending", steps[0].Status)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("an unverifiable destination we created was left in the library")
	}
}

// The converse, and the one that matters. A step routed to skip stays at
// in_progress with dst_path pointing at the USER'S file; a crash later in
// the same run brings Reconcile here. It must refuse.
func TestReconcileRefusesToRemoveAFileWeDidNotCreate(t *testing.T) {
	e, _, lib := newExec(t)
	dst := filepath.Join(lib, "users-own-file.mkv")
	writeFile(t, dst, "THE USER PUT THIS HERE")

	steps := []Step{{Dst: dst, SrcSize: 100, Status: "in_progress", CreatedByUs: false}}
	errs := e.Reconcile(steps)

	if len(errs) == 0 {
		t.Error("refusing to delete an unrecorded file must be surfaced, not silent")
	}
	if steps[0].Status != "blocked" {
		t.Errorf("status = %q, want blocked", steps[0].Status)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("Reconcile deleted a file it had no record of creating: %v", err)
	}
	if string(got) != "THE USER PUT THIS HERE" {
		t.Fatalf("the user's file was modified: %q", got)
	}
}

// Free space is refused BEFORE any bytes are written, with the numbers.
func TestPreflightRefusesWhenSpaceIsShort(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "huge.mkv")
	writeFile(t, s, "x")

	opts := DefaultOptions()
	opts.ReserveBytes = 1 << 62 // larger than any real filesystem
	e.opts = opts

	err := e.Preflight([]Step{stepFor(t, s, filepath.Join(lib, "huge.mkv"))}, lib)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("expected ErrNoSpace, got %v", err)
	}
	if !strings.Contains(err.Error(), "reserve") {
		t.Errorf("the refusal must state the numbers, got: %v", err)
	}
}

// An unreadable source fails preflight rather than mid-run. There is no
// link fallback: safe_hardlink_source() needs read AND write access.
func TestPreflightRefusesUnreadableSource(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks do not apply")
	}
	e, src, lib := newExec(t)
	s := filepath.Join(src, "secret.mkv")
	writeFile(t, s, "x")
	if err := os.Chmod(s, 0o000); err != nil {
		t.Skip("cannot chmod on this filesystem")
	}
	defer os.Chmod(s, 0o600)

	err := e.Preflight([]Step{{Src: s, Dst: filepath.Join(lib, "x.mkv")}}, lib)
	if err == nil || !strings.Contains(err.Error(), "SRC_UNREADABLE") {
		t.Fatalf("expected SRC_UNREADABLE, got %v", err)
	}
}

// Cancellation must be honoured inside a copy, not only between steps. A
// step used to be one link(2); it is now a multi-gigabyte read loop.
func TestCopyHonoursCancellationMidStream(t *testing.T) {
	e, src, lib := newExec(t)
	s := filepath.Join(src, "big.mkv")
	writeFile(t, s, strings.Repeat("x", 1<<20))

	opts := DefaultOptions()
	opts.BufSize = 4096
	e.opts = opts

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := e.Run(ctx, []Step{stepFor(t, s, filepath.Join(lib, "big.mkv"))})
	if err == nil {
		t.Fatal("expected cancellation to abort the copy")
	}
	if _, err := os.Stat(filepath.Join(lib, "big.mkv")); !os.IsNotExist(err) {
		t.Error("a cancelled copy published a file")
	}
}

func TestExecutorRespectsDryRunThroughTheGuard(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "t")
	lib := filepath.Join(base, "m")
	os.MkdirAll(src, 0o755)
	os.MkdirAll(lib, 0o755)

	g := fsx.NewGuarded(fsx.NewOS())
	g.AddSourceRoot(src)
	g.AddLibraryRoot(lib)
	g.SetDryRun(true)

	s := filepath.Join(src, "a.mkv")
	writeFile(t, s, "a")
	e := New(g, nil, DefaultOptions())

	err := e.Run(context.Background(), []Step{stepFor(t, s, filepath.Join(lib, "a.mkv"))})
	if !errors.Is(err, fsx.ErrDryRun) {
		t.Fatalf("dry-run must reach the executor through the port, got %v", err)
	}
	entries, _ := os.ReadDir(lib)
	if len(entries) != 0 {
		t.Fatalf("dry-run wrote %d entries into the library", len(entries))
	}
}
