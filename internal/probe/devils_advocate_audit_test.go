package probe

// Round-03 audit (devil's advocate, 2026-08-07), converted to regression
// guards.
//
// DA-4 and DA-5 originally demonstrated that this package wrote into a
// download root and into every library root using raw os calls — bypassing
// fsx.Guard, mid-scan, with dry-run engaged, and once per UI poll.
//
// A hardlink probe MUST create a real file on the source side; that is what
// link(2) needs and it cannot be tested any other way. So the fix was never
// "do not write" — it was three narrower properties, which is what these
// now assert:
//
//	1. every write goes through the port, so the guard can refuse it;
//	2. only the reserved probe filename is ever used;
//	3. the probe is keyed on a configured ROOT, never on a torrent's own
//	   subdirectory.
//
// When and how often it runs is a pipeline concern and is asserted there.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenGann/Orphanarr/internal/fsx"
)

// A Prober whose port refuses everything must not touch the filesystem.
// This is the property that makes the guard meaningful: before the fix,
// probe called os.OpenFile directly and no refusal was possible.
func TestProbeCannotWriteWithoutThePort(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "downloads", "Some.Movie.2019.1080p-GRP")
	dst := filepath.Join(base, "media")
	mustMkdir(t, src, dst)

	// A guard with NO registered roots refuses every path.
	p := New(fsx.NewGuarded(fsx.NewOS()))
	res := p.Probe(src, dst)

	if res.Outcome == Available {
		t.Error("the probe reported success while the guard was refusing every write")
	}
	for _, d := range []string{src, dst} {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("%s was written to despite the guard refusing: %d entries", d, len(entries))
		}
	}
}

// Every filename this package creates must carry the reserved prefix, or
// the guard's carve-out would have to be widened to arbitrary names — and a
// carve-out that accepts any name is not a carve-out.
func TestEveryProbeFilenameIsReserved(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "t")
	dst := filepath.Join(base, "m")
	mustMkdir(t, src, dst)

	var seen []string
	p := New(recorder{&seen})

	p.Probe(src, dst)
	p.Writable(dst)
	p.CaseInsensitive(dst)

	if len(seen) == 0 {
		t.Fatal("no probe paths were recorded")
	}
	for _, path := range seen {
		if !strings.HasPrefix(filepath.Base(path), fsx.ProbePrefix) {
			t.Errorf("probe wrote %q, which the guard's carve-out would refuse", path)
		}
	}
}

// recorder captures the paths a probe asks to write, without touching disk.
type recorder struct{ seen *[]string }

func (r recorder) ProbeWrite(path string, fn func(string) error) error {
	*r.seen = append(*r.seen, path)
	return fn(path)
}
