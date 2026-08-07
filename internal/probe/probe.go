// Package probe answers "can this pair be hardlinked?" by trying it.
//
// The folklore is "hardlinks work if the files are on the same filesystem".
// That is not the kernel's test. filename_linkat() compares vfsmount
// pointers, not superblocks, so two bind mounts of ONE filesystem report
// identical st_dev and still return EXDEV — verified in
// tests/verification/bindmount_hardlink_test.sh.
//
// The consequence is that st_dev is worthless as a gate: it returns "safe"
// in exactly the misconfiguration that fails. The only sound probe is a real
// link(2) attempt.
package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// Outcome is deliberately three-valued.
//
// A boolean would collapse the last two, and they need different advice:
// "separate mounts" is fixed by consolidating to one -v, while "the source
// is read-only" means the user already chose the other topology and telling
// them to consolidate would be telling them to do what they have done.
type Outcome string

const (
	Available   Outcome = "available"
	SeparateMnt Outcome = "copy only — separate mounts"
	SourceRO    Outcome = "copy only — source is on a read-only mount"
	Unknown     Outcome = "not probed"
)

// Result is one pair's answer, with the evidence.
type Result struct {
	Outcome Outcome `json:"outcome"`
	// Errno is the raw errno string when the link failed. EXDEV vs EPERM
	// vs EMLINK are three different user-facing problems, and "failed to
	// link file" distinguishes none of them.
	Errno string `json:"errno,omitempty"`
	// SrcDev and DstDev are recorded for the device map only. They are
	// NEVER the gate.
	SrcDev uint64 `json:"src_dev"`
	DstDev uint64 `json:"dst_dev"`
	Detail string `json:"detail,omitempty"`
}

// Prober caches results per (source root, library root) pair.
//
// Per pair, not global: §3.5 and §6.3 both model it that way, and an
// all-or-nothing switch would force copies onto provably linkable pairs —
// spending the one resource that never comes back, for no safety.
// Writer is the narrow slice of the filesystem port a probe needs.
//
// The probe takes it rather than calling os directly, so that the one
// component that legitimately writes into a download root does so THROUGH
// the guard. Before this, probe was the single package that could mutate
// the filesystem without passing the chokepoint — and the README's
// headline claim was false because of it.
type Writer interface {
	ProbeWrite(path string, fn func(realPath string) error) error
}

type Prober struct {
	mu    sync.RWMutex
	cache map[string]Result
	w     Writer
}

func New(w Writer) *Prober { return &Prober{cache: map[string]Result{}, w: w} }

// probeFile names every file this package creates. The guard refuses any
// other name through ProbeWrite.
func probeFile(dir, suffix string) string {
	return filepath.Join(dir, ".orphanarr-probe-"+suffix)
}

func (p *Prober) create(path string, perm os.FileMode) error {
	if p.w == nil {
		return errors.New("probe: no filesystem port configured")
	}
	return p.w.ProbeWrite(path, func(real string) error {
		f, err := os.OpenFile(real, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

func (p *Prober) remove(path string) {
	if p.w == nil {
		return
	}
	_ = p.w.ProbeWrite(path, func(real string) error { return os.Remove(real) })
}

func key(src, dst string) string { return src + "\x00" + dst }

// Get returns a cached result.
func (p *Prober) Get(srcRoot, dstRoot string) (Result, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.cache[key(srcRoot, dstRoot)]
	return r, ok
}

// BestFor summarises every probed pair that targets dstRoot.
//
// The UI shows one badge per library, but a library can be reachable from
// several download roots with different answers. Reporting the BEST is the
// honest summary for a badge that says "can this be hardlinked at all" —
// and the per-pair result is still what the executor consults, so a
// linkable pair is never downgraded by an unlinkable sibling.
func (p *Prober) BestFor(dstRoot string) (Result, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var best Result
	found := false
	for k, r := range p.cache {
		if len(k) < len(dstRoot) || k[len(k)-len(dstRoot):] != dstRoot {
			continue
		}
		if !found || rank(r.Outcome) > rank(best.Outcome) {
			best, found = r, true
		}
	}
	return best, found
}

// rank orders outcomes best-first. SourceRO outranks SeparateMnt because it
// is a deliberate configuration rather than a mistake, and the UI should
// not nag a user who chose it.
func rank(o Outcome) int {
	switch o {
	case Available:
		return 3
	case SourceRO:
		return 2
	case SeparateMnt:
		return 1
	}
	return 0
}

// EnsureProbed probes a pair once, lazily. Under the common deployment the
// container mounts the client's download folder at the path the client
// already reports, so there are no path-mapping rules to enumerate at
// startup and the source root is only discovered when a scan reads it.
func (p *Prober) EnsureProbed(srcRoot, dstRoot string) Result {
	if r, ok := p.Get(srcRoot, dstRoot); ok {
		return r
	}
	return p.Probe(srcRoot, dstRoot)
}

// Probe attempts a real link and caches the outcome.
//
// It writes exactly two files, both named and both removed: this is a
// user-initiated capability probe, which is I10's explicit carve-out from
// the dry-run rule. The carve-out is why the probe is a named method rather
// than something the executor does implicitly.
func (p *Prober) Probe(srcRoot, dstRoot string) Result {
	res := p.probePair(srcRoot, dstRoot)
	p.mu.Lock()
	p.cache[key(srcRoot, dstRoot)] = res
	p.mu.Unlock()
	return res
}

func (p *Prober) probePair(srcRoot, dstRoot string) Result {
	var res Result

	if st, err := statDev(srcRoot); err == nil {
		res.SrcDev = st
	}
	if st, err := statDev(dstRoot); err == nil {
		res.DstDev = st
	}

	// A read-only source is checked FIRST, because it explains the EXDEV
	// that follows. Probing in the other order reports "separate mounts"
	// for a user who deliberately mounted :ro, and sends them to fix a
	// configuration that is already what they intended.
	if ro, err := isReadOnly(srcRoot); err == nil && ro {
		res.Outcome = SourceRO
		res.Detail = "the download root is mounted read-only, which enforces " +
			"\"never touch a source file\" in the kernel but makes every " +
			"placement a copy: a :ro bind is a separate vfsmount, so link(2) " +
			"returns EXDEV unconditionally"
		return res
	}

	src := probeFile(srcRoot, "src")
	dst := probeFile(dstRoot, "dst")

	if err := p.create(src, 0o600); err != nil {
		res.Outcome = SeparateMnt
		res.Detail = "could not create a probe file in the download root: " + err.Error()
		return res
	}
	defer p.remove(src)

	// A leftover from an interrupted probe would give EEXIST forever,
	// reading as "cannot hardlink" on a pair that can.
	p.remove(dst)
	err := os.Link(src, dst)
	if err == nil {
		p.remove(dst)
		res.Outcome = Available
		return res
	}

	res.Outcome = SeparateMnt
	res.Errno = errnoOf(err)
	switch res.Errno {
	case "EXDEV":
		res.Detail = "link(2) returned EXDEV. If these are on one filesystem, " +
			"the cause is mount topology, not the filesystem: use a single " +
			"-v /pool:/data with the download and library roots beneath it, " +
			"rather than separate -v for each"
	case "EPERM":
		res.Detail = "link(2) returned EPERM. Either the filesystem does not " +
			"support hardlinks, or fs.protected_hardlinks refused it because " +
			"the source is not readable and writable by this user"
	case "EMLINK":
		res.Detail = "link(2) returned EMLINK: the source has reached the " +
			"filesystem's link-count limit, which heavy cross-seeding can do"
	default:
		res.Detail = "link(2) failed: " + err.Error()
	}
	return res
}

func statDev(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}

// isReadOnly asks the kernel rather than parsing /proc/mounts, which does
// not reflect what THIS mount namespace sees.
func isReadOnly(path string) (bool, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false, err
	}
	return st.Flags&unix.ST_RDONLY != 0, nil
}

func errnoOf(err error) string {
	var errno unix.Errno
	if le, ok := err.(*os.LinkError); ok {
		if e, ok := le.Err.(unix.Errno); ok {
			errno = e
		}
	}
	switch errno {
	case unix.EXDEV:
		return "EXDEV"
	case unix.EPERM:
		return "EPERM"
	case unix.EMLINK:
		return "EMLINK"
	case unix.ENOSYS:
		return "ENOSYS"
	case unix.EOPNOTSUPP:
		return "EOPNOTSUPP"
	case unix.EACCES:
		return "EACCES"
	case 0:
		return ""
	}
	return fmt.Sprintf("errno %d", int(errno))
}

// Writable reports whether a library root can be written to, which is a
// different question from whether it can be linked into. A read-only
// LIBRARY root means EROFS on every placement — a predictable mistake right
// after the documentation teaches users to mount downloads :ro.
func (p *Prober) Writable(root string) (bool, error) {
	path := probeFile(root, "write")
	if err := p.create(path, 0o600); err != nil {
		return false, err
	}
	p.remove(path)
	return true, nil
}

// CaseInsensitive probes whether the destination folds case, so collision
// detection can match what the filesystem will actually do.
func (p *Prober) CaseInsensitive(root string) (bool, error) {
	path := probeFile(root, "case")
	if err := p.create(path, 0o600); err != nil {
		return false, err
	}
	defer p.remove(path)

	_, err := os.Stat(filepath.Join(root, ".ORPHANARR-PROBE-CASE"))
	return err == nil, nil
}
