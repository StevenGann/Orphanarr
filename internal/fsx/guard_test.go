package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newTestGuard builds a guard over a real temp filesystem with one source
// root and one library root, which is the shape every rule below is about.
func newTestGuard(t *testing.T) (g *Guard, src, lib, cfg string) {
	t.Helper()
	base := t.TempDir()
	src = filepath.Join(base, "torrents")
	lib = filepath.Join(base, "media")
	cfg = filepath.Join(base, "config")
	for _, d := range []string{src, lib, cfg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g = NewGuarded(NewOS())
	g.AddSourceRoot(src)
	g.AddLibraryRoot(lib)
	g.SetConfigRoot(cfg)
	return g, src, lib, cfg
}

// I1 is the prime directive. Every mutating entry point must refuse a path
// under a source root — not most of them, every one. A test that checks
// only Remove would pass while CreateExcl quietly truncated a seeding file.
func TestI1_EveryMutationRefusesSourceRoots(t *testing.T) {
	g, src, lib, _ := newTestGuard(t)

	victim := filepath.Join(src, "seeding.mkv")
	if err := os.WriteFile(victim, []byte("IRREPLACEABLE"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(lib, "ok.mkv")

	cases := []struct {
		name string
		call func() error
	}{
		{"CreateExcl", func() error { _, err := g.CreateExcl(victim, 0o644); return err }},
		{"MkdirAll", func() error { _, err := g.MkdirAll(filepath.Join(src, "new"), 0o755); return err }},
		{"Link", func() error { return g.Link(other, filepath.Join(src, "link.mkv")) }},
		{"RenameNoReplace", func() error { return g.RenameNoReplace(victim, other) }},
		{"Remove", func() error { return g.Remove(victim) }},
		{"RemoveDirIfEmpty", func() error { return g.RemoveDirIfEmpty(src) }},
		{"Chmod", func() error { return g.Chmod(victim, 0o600) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, ErrSourceRoot) {
				t.Fatalf("expected ErrSourceRoot, got %v", err)
			}
		})
	}

	// The file must be byte-identical afterwards. #C9's lesson: an
	// operation that "failed" can still have destroyed something.
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "IRREPLACEABLE" {
		t.Fatalf("source file was modified: %q", got)
	}
}

// I1 outranks everything below it. A misconfiguration that nests a library
// root inside a download root must not unlock writes to the download root.
func TestI1_OutranksLibraryContainment(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "data")
	nested := filepath.Join(src, "media")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewGuarded(NewOS())
	g.AddSourceRoot(src)
	g.AddLibraryRoot(nested) // operator error

	err := g.Remove(filepath.Join(nested, "anything"))
	if !errors.Is(err, ErrSourceRoot) {
		t.Fatalf("library nested in a source root must still refuse; got %v", err)
	}
}

// I6: never create a path outside a configured library root.
func TestI6_RefusesOutsideLibrary(t *testing.T) {
	g, _, _, _ := newTestGuard(t)
	escape := filepath.Join(t.TempDir(), "elsewhere.mkv")
	_, err := g.CreateExcl(escape, 0o644)
	if !errors.Is(err, ErrOutsideLibrary) {
		t.Fatalf("expected ErrOutsideLibrary, got %v", err)
	}
}

// I6's lexical half is defeated by a symlinked intermediate directory, which
// is exactly the case a torrent-supplied name can construct. The walk is the
// other half.
func TestI6_RefusesSymlinkedIntermediateDirectory(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)
	outside := t.TempDir()
	link := filepath.Join(lib, "sneaky")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Lexically this is inside the library root. It is not.
	target := filepath.Join(link, "escaped.mkv")
	_, err := g.CreateExcl(target, 0o644)
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("expected ErrSymlink, got %v", err)
	}
}

// I12: no mode change on a non-directory with st_nlink > 1. Chmod on a
// hardlink reaches the seeding torrent's inode (#C19).
func TestI12_RefusesChmodOnSharedInode(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)

	a := filepath.Join(lib, "a.mkv")
	b := filepath.Join(lib, "b.mkv")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}

	if err := g.Chmod(b, 0o600); !errors.Is(err, ErrSharedInode) {
		t.Fatalf("expected ErrSharedInode, got %v", err)
	}

	// The mode must be untouched — a refusal that already applied the
	// change is not a refusal.
	fi, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode changed despite refusal: %v", fi.Mode().Perm())
	}
}

// The scoping of I12 to non-directories is the defect the design round
// caught at the vote, and it is worth a test of its own so nobody
// "simplifies" the rule back. Every directory has nlink >= 2.
func TestI12_AllowsChmodOnDirectories(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)

	d := filepath.Join(lib, "Series Name")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := g.Lstat(d); err != nil {
		t.Fatal(err)
	} else if st.Nlink < 2 {
		t.Fatalf("precondition failed: expected directory nlink>=2, got %d — "+
			"if this ever fires, the unscoped I12 rule would have looked correct here", st.Nlink)
	}
	_ = fi

	if err := g.Chmod(d, 0o755); err != nil {
		t.Fatalf("directory chmod must be permitted (dir_mode depends on it): %v", err)
	}
	after, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o755 {
		t.Fatalf("expected 0755, got %v", after.Mode().Perm())
	}
}

// I10: in dry-run, zero write syscalls outside /config.
func TestI10_DryRunRefusesLibraryWritesButAllowsConfig(t *testing.T) {
	g, _, lib, cfg := newTestGuard(t)
	g.SetDryRun(true)

	if _, err := g.CreateExcl(filepath.Join(lib, "x.mkv"), 0o644); !errors.Is(err, ErrDryRun) {
		t.Fatalf("expected ErrDryRun for a library write, got %v", err)
	}

	// The journal and database live in /config and must keep working, or
	// dry-run cannot record what it would have done.
	f, err := g.CreateExcl(filepath.Join(cfg, "journal.ndjson"), 0o644)
	if err != nil {
		t.Fatalf("config writes must be permitted in dry-run: %v", err)
	}
	f.Close()
}

// A guard told nothing may touch nothing. Failing closed is the default.
func TestGuard_FailsClosedWithNoRoots(t *testing.T) {
	g := NewGuarded(NewOS())
	if _, err := g.CreateExcl(filepath.Join(t.TempDir(), "x"), 0o644); err == nil {
		t.Fatal("a guard with no registered roots must refuse every write")
	}
}

// MkdirAll must report exactly the directories it created, because rollback
// removes only those. Reporting a pre-existing directory would make rollback
// delete something the user already had.
func TestMkdirAll_ReportsOnlyWhatItCreated(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)

	existing := filepath.Join(lib, "TV")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := g.MkdirAll(filepath.Join(existing, "Severance", "Season 01"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("expected 2 created dirs, got %d: %v", len(created), created)
	}
	for _, c := range created {
		if c == existing {
			t.Fatal("reported a pre-existing directory as created; rollback would delete the user's folder")
		}
	}

	// Re-running must report nothing new.
	again, err := g.MkdirAll(filepath.Join(existing, "Severance", "Season 01"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("idempotent MkdirAll must report nothing, got %v", again)
	}
}

// #C24: mkdir's mode argument is masked by umask, so MkdirAll must chmod to
// deliver the configured dir_mode. Without this the whole permissions fix is
// silently defeated one level up from the files.
func TestMkdirAll_DefeatsUmask(t *testing.T) {
	old := setUmask(0o077)
	defer setUmask(old)

	g, _, lib, _ := newTestGuard(t)
	d := filepath.Join(lib, "Movies")
	if _, err := g.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("umask 077 defeated dir_mode: got %v, want 0755", fi.Mode().Perm())
	}
}

// Link must refuse to overwrite. This is the property that makes I2
// mechanically true rather than aspirational — rename(2) silently destroys
// an existing destination (#C9) and link(2) does not.
func TestLink_RefusesExistingDestination(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)

	src := filepath.Join(lib, "new.mkv")
	dst := filepath.Join(lib, "existing.mkv")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("IRREPLACEABLE"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := g.Link(src, dst); err == nil {
		t.Fatal("link over an existing destination must fail")
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "IRREPLACEABLE" {
		t.Fatalf("destination was destroyed: %q", got)
	}
}

// CreateExcl must refuse an existing path. #C34: a partial reused without
// O_TRUNC leaves a stale tail that passes a size check.
func TestCreateExcl_RefusesExisting(t *testing.T) {
	g, _, lib, _ := newTestGuard(t)
	p := filepath.Join(lib, "partial.tmp")
	if err := os.WriteFile(p, []byte("stale tail"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateExcl(p, 0o644); err == nil {
		t.Fatal("CreateExcl must refuse an existing path")
	}
}

func TestChown_IsNeverImplemented(t *testing.T) {
	g, _, _, _ := newTestGuard(t)
	if err := g.Chown("/anything", 1000, 1000); !errors.Is(err, ErrChownUnsupported) {
		t.Fatalf("chown must refuse by design (CAP_CHOWN, D3): %v", err)
	}
}
