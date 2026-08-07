package fsx

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Guard wraps an FS and enforces the invariants that protect user data.
//
// The design says I1 "should be enforced by the fsx.FS port, not by
// discipline". This is that enforcement. A caller cannot mutate a source
// file by forgetting a check, because there is no unguarded path to it.
type Guard struct {
	inner FS

	mu           sync.RWMutex
	sourceRoots  []string
	libraryRoots []string
	configRoot   string
	dryRun       bool
}

// NewGuarded wraps inner. With no roots registered it refuses every
// mutation: a Guard that has not been told what it may touch may touch
// nothing. Failing closed is the whole point.
func NewGuarded(inner FS) *Guard {
	return &Guard{inner: inner}
}

func clean(p string) string { return filepath.Clean(p) }

// AddSourceRoot registers a download root. Nothing under it may ever be
// mutated (I1).
func (g *Guard) AddSourceRoot(root string) {
	if strings.TrimSpace(root) == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	r := clean(root)
	for _, e := range g.sourceRoots {
		if e == r {
			return
		}
	}
	g.sourceRoots = append(g.sourceRoots, r)
}

// SourceRoots returns the registered download roots.
//
// Exposed so the prober can key on a real configured root rather than on a
// torrent's own subdirectory, which is both unbounded and a write into
// someone's seeding data.
func (g *Guard) SourceRoots() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]string(nil), g.sourceRoots...)
}

// ResetLibraryRoots replaces the library set.
//
// Reload used to APPEND on every configuration save, so a library root the
// user deleted — or corrected after a typo — stayed writable for the life
// of the process, and the slice grew without bound. Source roots are
// deliberately NOT reset here: forgetting a download root un-protects it,
// and accumulating one only ever refuses more.
func (g *Guard) ResetLibraryRoots() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.libraryRoots = nil
}

// ErrProbeNotAtRoot means a probe was aimed somewhere other than the top
// level of a registered root.
var ErrProbeNotAtRoot = errors.New(
	"fsx: a probe may only be written at the top level of a registered root")

// ProbeWrite creates and removes a single named probe file.
//
// This is I10's carve-out, expressed AT the chokepoint rather than as an
// end-run around it. The probe package previously used raw os.OpenFile, so
// it was the one component writing to the filesystem without passing the
// port — which falsified the README's claim that every mutating entry
// point funnels through one check.
//
// Three rules, all narrower than a normal write, and each one closes a
// hole that was reintroduced after the first fix:
//
//  1. The name must carry the reserved probe prefix.
//  2. The path's DIRECTORY must BE a registered root, not merely be
//     contained by one. Containment alone let a caller probe
//     path.Dir(firstFile) — a seeding torrent's own data directory —
//     producing one write per orphan per scan and an unbounded cache.
//  3. Dry-run refuses. The carve-out covers a user asking "can you
//     hardlink?"; it does not cover a scheduled scan, and the dry-run gate
//     belongs here rather than in one of the callers, because a second
//     caller will not remember it.
func (g *Guard) ProbeWrite(path string, fn func(realPath string) error) error {
	base := filepath.Base(path)
	if !strings.HasPrefix(base, ProbePrefix) {
		return fmt.Errorf("fsx: %q is not a probe filename (must start with %q)",
			base, ProbePrefix)
	}

	g.mu.RLock()
	dry := g.dryRun
	dir := clean(filepath.Dir(path))
	atRoot := false
	for _, r := range g.sourceRoots {
		if r == dir {
			atRoot = true
			break
		}
	}
	if !atRoot {
		for _, r := range g.libraryRoots {
			if r == dir {
				atRoot = true
				break
			}
		}
	}
	g.mu.RUnlock()

	if dry {
		return fmt.Errorf("%w: capability probes do not run in dry-run", ErrDryRun)
	}
	if !atRoot {
		return fmt.Errorf("%w: %s", ErrProbeNotAtRoot, path)
	}
	return fn(path)
}

// SourceRootFor returns the registered download root containing path, so a
// caller can probe THE ROOT rather than an arbitrary directory beneath it.
func (g *Guard) SourceRootFor(path string) (string, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p := clean(path)
	best := ""
	for _, r := range g.sourceRoots {
		if underRoot(p, r) && len(r) > len(best) {
			best = r
		}
	}
	return best, best != ""
}

// ProbePrefix is the reserved filename prefix for capability probes.
const ProbePrefix = ".orphanarr-probe"

// AddLibraryRoot registers a destination root. Writes are permitted only
// inside one of these (I6).
func (g *Guard) AddLibraryRoot(root string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.libraryRoots = append(g.libraryRoots, clean(root))
}

// SetConfigRoot registers /config, which is writable in dry-run because the
// journal and database live there (I10).
func (g *Guard) SetConfigRoot(root string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.configRoot = clean(root)
}

// SetDryRun engages or releases I10.
func (g *Guard) SetDryRun(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dryRun = on
}

func (g *Guard) DryRun() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.dryRun
}

// underRoot reports whether path is root or is contained by it. It is
// purely lexical and therefore not sufficient on its own — a symlinked
// intermediate directory defeats it, which is why assertWritable also walks
// for symlinks (DESIGN I6).
func underRoot(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (g *Guard) inAnySource(path string) bool {
	for _, r := range g.sourceRoots {
		if underRoot(path, r) {
			return true
		}
	}
	return false
}

func (g *Guard) inAnyLibrary(path string) bool {
	for _, r := range g.libraryRoots {
		if underRoot(path, r) {
			return true
		}
	}
	return false
}

// assertNoSymlinks walks every existing component of path and refuses if any
// is a symlink. The lexical containment check above is defeated by a
// symlinked intermediate directory; this is the second half of I6.
//
// Components that do not yet exist are fine — we are about to create them.
func (g *Guard) assertNoSymlinks(path string) error {
	path = clean(path)
	var parts []string
	cur := path
	for {
		parts = append(parts, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	// Walk shallowest to deepest so a symlink is caught before anything
	// beneath it is considered.
	for i := len(parts) - 1; i >= 0; i-- {
		fi, err := g.inner.Lstat(parts[i])
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if fi.Mode&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrSymlink, parts[i])
		}
	}
	return nil
}

// assertWritable is the single chokepoint every mutation passes through.
func (g *Guard) assertWritable(path string) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	p := clean(path)

	// I1 first, and unconditionally. A path under a download root is never
	// writable, no matter what else is true of it — including when a
	// library root has been misconfigured to overlap one.
	if g.inAnySource(p) {
		return fmt.Errorf("%w: %s", ErrSourceRoot, p)
	}

	// /config is always writable: the database and journal live there, and
	// I10's dry-run rule is about the media filesystem.
	if g.configRoot != "" && underRoot(p, g.configRoot) {
		return nil
	}

	if g.dryRun {
		return fmt.Errorf("%w: %s", ErrDryRun, p)
	}

	if !g.inAnyLibrary(p) {
		return fmt.Errorf("%w: %s", ErrOutsideLibrary, p)
	}

	return g.assertNoSymlinks(p)
}

// Reads pass straight through. Reading is not a mutation, and the scanner
// must be able to stat inside source roots to do its job at all.

func (g *Guard) Stat(path string) (FileInfo, error)      { return g.inner.Stat(path) }
func (g *Guard) Lstat(path string) (FileInfo, error)     { return g.inner.Lstat(path) }
func (g *Guard) Open(path string) (io.ReadCloser, error) { return g.inner.Open(path) }
func (g *Guard) ReadDir(p string) ([]os.DirEntry, error) { return g.inner.ReadDir(p) }
func (g *Guard) Statfs(path string) (StatfsInfo, error)  { return g.inner.Statfs(path) }

func (g *Guard) CreateExcl(path string, perm fs.FileMode) (File, error) {
	if err := g.assertWritable(path); err != nil {
		return nil, err
	}
	return g.inner.CreateExcl(path, perm)
}

func (g *Guard) MkdirAll(path string, perm fs.FileMode) ([]string, error) {
	if err := g.assertWritable(path); err != nil {
		return nil, err
	}
	return g.inner.MkdirAll(path, perm)
}

// Link guards the destination only. The source of a hardlink is read, not
// written — that is the entire reason hardlinking was ever safe here.
func (g *Guard) Link(oldpath, newpath string) error {
	if err := g.assertWritable(newpath); err != nil {
		return err
	}
	return g.inner.Link(oldpath, newpath)
}

// RenameNoReplace guards both sides: a rename mutates the source name too.
func (g *Guard) RenameNoReplace(oldpath, newpath string) error {
	if err := g.assertWritable(oldpath); err != nil {
		return err
	}
	if err := g.assertWritable(newpath); err != nil {
		return err
	}
	return g.inner.RenameNoReplace(oldpath, newpath)
}

func (g *Guard) Remove(path string) error {
	if err := g.assertWritable(path); err != nil {
		return err
	}
	return g.inner.Remove(path)
}

func (g *Guard) RemoveDirIfEmpty(path string) error {
	if err := g.assertWritable(path); err != nil {
		return err
	}
	return g.inner.RemoveDirIfEmpty(path)
}

// Chmod carries I12 on top of the usual guard.
//
// The scoping to non-directories is load-bearing and must not be
// "simplified": a POSIX directory is never st_nlink == 1 — it is 2 plus one
// per subdirectory — and a directory cannot be hardlinked at all (EPERM,
// #C6), so a link-count test carries no safety information about one. An
// unscoped rule here forbids the dir_mode the design requires and leaves
// library directories at 0700 under the container umask: correctly-moded,
// still-invisible files inside a directory nothing can traverse.
func (g *Guard) Chmod(path string, mode fs.FileMode) error {
	if err := g.assertWritable(path); err != nil {
		return err
	}
	fi, err := g.inner.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir && fi.Nlink > 1 {
		return fmt.Errorf("%w: %s has nlink=%d", ErrSharedInode, path, fi.Nlink)
	}
	return g.inner.Chmod(path, mode)
}

func (g *Guard) SyncDir(path string) error { return g.inner.SyncDir(path) }

// Chown is never implemented. It is here so that the refusal is discoverable
// at the port rather than as an absence someone "fixes" later.
func (g *Guard) Chown(path string, uid, gid int) error { return ErrChownUnsupported }

var _ FS = (*Guard)(nil)
