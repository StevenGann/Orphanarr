package probe

import (
	"testing"

	"github.com/StevenGann/Orphanarr/internal/fsx"
)

// newTestProber wires a Prober to a real Guard with every temp directory
// registered, which is what production does.
//
// The probe takes the port rather than calling os directly, so a test that
// bypassed it would prove nothing about the property that matters: that the
// one component legitimately writing into a download root does so THROUGH
// the guard.
func newTestProber(t *testing.T) *Prober {
	t.Helper()
	g := fsx.NewGuarded(fsx.NewOS())
	g.AddSourceRoot(t.TempDir())
	g.AddLibraryRoot(t.TempDir())
	return New(&openGuard{g})
}

// openGuard registers roots on demand, so a test can probe any directory it
// creates without listing them up front. Production registers roots from
// configuration; this only changes WHICH paths are permitted, not whether
// the call passes through the guard at all.
type openGuard struct{ g *fsx.Guard }

func (o *openGuard) ProbeWrite(path string, fn func(string) error) error {
	o.g.AddSourceRoot(dirOf(path))
	return o.g.ProbeWrite(path, fn)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return p
}
