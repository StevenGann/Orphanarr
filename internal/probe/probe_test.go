package probe

import (
	"os"
	"path/filepath"
	"testing"
)

// Two directories on one filesystem link fine. This is the baseline the
// interesting cases are measured against.
func TestProbeSucceedsWithinOneFilesystem(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "torrents")
	dst := filepath.Join(base, "media")
	mustMkdir(t, src, dst)

	got := New().Probe(src, dst)
	if got.Outcome != Available {
		t.Fatalf("outcome = %q (%s), want %q", got.Outcome, got.Detail, Available)
	}
}

// A probe must leave nothing behind. It writes into a directory a media
// server scans, so a leftover is a phantom file in someone's library.
func TestProbeLeavesNoFilesBehind(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "t")
	dst := filepath.Join(base, "m")
	mustMkdir(t, src, dst)

	New().Probe(src, dst)

	for _, d := range []string{src, dst} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("%s still contains %v", d, names)
		}
	}
}

// An interrupted probe leaves a stale destination file, and O_EXCL would
// then report EEXIST forever — reading as "cannot hardlink" on a pair that
// can.
func TestProbeRecoversFromALeftoverDestination(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "t")
	dst := filepath.Join(base, "m")
	mustMkdir(t, src, dst)

	stale := filepath.Join(dst, ".orphanarr-probe-dst")
	if err := os.WriteFile(stale, []byte("left over"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := New().Probe(src, dst); got.Outcome != Available {
		t.Fatalf("a leftover probe file made the pair look unlinkable: %q (%s)",
			got.Outcome, got.Detail)
	}
}

// The three outcomes must stay distinct. Collapsing the last two to a
// boolean is what makes the remediation banner tell a :ro user to do what
// they have already done.
func TestOutcomesAreDistinct(t *testing.T) {
	seen := map[Outcome]bool{}
	for _, o := range []Outcome{Available, SeparateMnt, SourceRO, Unknown} {
		if seen[o] {
			t.Fatalf("duplicate outcome value %q", o)
		}
		seen[o] = true
		if o == "" {
			t.Fatal("an empty outcome would render as a blank badge")
		}
	}
}

func TestResultsAreCachedPerPair(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "t")
	dstA := filepath.Join(base, "a")
	dstB := filepath.Join(base, "b")
	mustMkdir(t, src, dstA, dstB)

	p := New()
	if _, ok := p.Get(src, dstA); ok {
		t.Fatal("Get returned a result before any probe ran")
	}
	p.Probe(src, dstA)

	if _, ok := p.Get(src, dstA); !ok {
		t.Error("probed pair was not cached")
	}
	// A different library root is a different pair, and must not inherit
	// the first one's answer.
	if _, ok := p.Get(src, dstB); ok {
		t.Error("an unprobed pair returned a cached result from another pair")
	}
}

func TestWritableAndCaseProbesCleanUp(t *testing.T) {
	dir := t.TempDir()

	if ok, err := Writable(dir); err != nil || !ok {
		t.Fatalf("Writable = %v, %v", ok, err)
	}
	if _, err := CaseInsensitive(dir); err != nil {
		t.Fatalf("CaseInsensitive: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("probes left %d files behind", len(entries))
	}
}

// A library root that cannot be written is EROFS on every placement, and
// the design flags it as a predictable mistake right after teaching users
// to mount downloads :ro.
func TestWritableReportsAnUnwritableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission checks do not apply")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod on this filesystem")
	}
	defer os.Chmod(dir, 0o700)

	if ok, _ := Writable(dir); ok {
		t.Fatal("an unwritable root reported as writable")
	}
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
