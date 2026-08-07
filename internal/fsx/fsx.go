// Package fsx is the filesystem port.
//
// It exists for two reasons the design states explicitly (DESIGN §2.3):
//
//  1. EXDEV, ENOSPC, EACCES and crash-mid-copy cannot be tested reliably
//     against a real disk, and those are the three bugs that eat someone's
//     library. The fault-injecting implementation is the point.
//  2. The invariants that protect user data should be enforced here rather
//     than by discipline at every call site. I1, I6 and I12 are mechanical
//     properties of this package, not rules an implementer must remember.
//
// Every mutating method is guarded. A caller that forgets a check does not
// get a subtly wrong result; it gets an error.
package fsx

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"time"
)

// Sentinel errors. Callers branch on these; they are not cosmetic.
var (
	// ErrSourceRoot means a mutation was attempted on a path under a
	// registered download root. This is I1, and it is the single most
	// important refusal in the program.
	ErrSourceRoot = errors.New("fsx: refusing to mutate a path under a source root (I1)")

	// ErrOutsideLibrary means a path escaped every configured library root.
	// This is I6.
	ErrOutsideLibrary = errors.New("fsx: path is outside every configured library root (I6)")

	// ErrSymlink means a symlink was encountered while resolving a path.
	// Following one would let a torrent-supplied name redirect the write.
	ErrSymlink = errors.New("fsx: refusing to follow a symlink in a destination path")

	// ErrSharedInode means a mode or ownership change was attempted on a
	// non-directory with st_nlink > 1 — an inode shared with a seeding
	// torrent. This is I12.
	ErrSharedInode = errors.New("fsx: refusing to change mode on a shared inode (I12)")

	// ErrDryRun means a write was attempted while dry-run is engaged. This
	// is I10, and the carve-out for user-initiated capability probes is
	// handled by the caller using a separate, explicitly-named method.
	ErrDryRun = errors.New("fsx: refusing to write in dry-run mode (I10)")

	// ErrChownUnsupported is returned by Chown always. The design does not
	// perform chown at all: it needs CAP_CHOWN, which contradicts running
	// as an unprivileged user (DESIGN §6.4, D3).
	ErrChownUnsupported = errors.New("fsx: chown is not implemented by design (DESIGN §6.4)")
)

// FileInfo carries the fields the design actually branches on. os.FileInfo
// hides Dev, Ino and Nlink behind an interface{} that every call site would
// otherwise have to type-assert — and Nlink is load-bearing for I12 and for
// the FP-8 already-imported heuristic.
type FileInfo struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
	Dev     uint64
	Ino     uint64
	Nlink   uint64
	UID     uint32
	GID     uint32
}

// StatfsInfo reports space in bytes, already converted from blocks.
//
// Avail is what an unprivileged process can actually use. Free includes
// root-reserved blocks and is carried only so the UI can show the gap:
// #C28 measured 11.61 GiB of ext4 space that Free reports and we can never
// have. Budget against Avail (DESIGN §6.5).
type StatfsInfo struct {
	Total uint64
	Free  uint64
	Avail uint64
	// Dev identifies the filesystem, which is how free space is accounted:
	// seven library roots on one pool must share one budget, not seven.
	Dev uint64
}

// File is an open file being written.
type File interface {
	io.Writer
	io.Closer
	// Sync fsyncs the file. Omitting it leaves a correctly-named,
	// correctly-sized, silently zero-filled file after a power loss.
	Sync() error
	// Chmod sets the mode on the open descriptor. Used on the partial
	// before publish, which is what keeps I12 true by construction.
	Chmod(mode fs.FileMode) error
	// Stat reports the open file, for size verification without a re-open.
	Stat() (FileInfo, error)
}

// FS is the port. Read methods are unguarded; every mutating method is
// guarded by the rules in guard.go.
type FS interface {
	// Reads.
	Stat(path string) (FileInfo, error)
	Lstat(path string) (FileInfo, error)
	Open(path string) (io.ReadCloser, error)
	ReadDir(path string) ([]os.DirEntry, error)
	Statfs(path string) (StatfsInfo, error)

	// Mutations.

	// CreateExcl opens a new file with O_CREAT|O_EXCL|O_WRONLY. Exclusive
	// creation is not optional: #C34 shows a reused partial without
	// O_TRUNC leaves a stale tail that passes a size check.
	CreateExcl(path string, perm fs.FileMode) (File, error)

	// MkdirAll creates the directory and its parents, returning every
	// directory it actually created, deepest last. The list is what
	// rollback keys on — a directory we did not create is never removed.
	MkdirAll(path string, perm fs.FileMode) ([]string, error)

	// Link is the publish primitive and the placement optimisation both.
	// It returns EEXIST rather than overwriting, which is the sole reason
	// I2 is mechanically true (DESIGN §6.5).
	Link(oldpath, newpath string) error

	// RenameNoReplace is the fallback publish for filesystems without
	// hardlinks. It never clobbers. Plain rename is deliberately absent
	// from this interface: #C9 turned b'IRREPLACEABLE' into b'replacement'
	// with no error, and an interface that offers it invites its use.
	RenameNoReplace(oldpath, newpath string) error

	Remove(path string) error
	RemoveDirIfEmpty(path string) error

	// Chmod refuses on a non-directory with Nlink > 1 (I12).
	Chmod(path string, mode fs.FileMode) error

	// SyncDir fsyncs a directory so a rename into it is durable. The step
	// that gets omitted.
	SyncDir(path string) error
}
