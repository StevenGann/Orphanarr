// Package exec is the ONLY package that writes to the media filesystem.
//
// Everything here exists because the alternative loses data. The rules are
// not defensive programming; each one corresponds to a failure that has been
// reproduced in tests/verification/.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/StevenGann/Orphanarr/internal/fsx"
	"github.com/StevenGann/Orphanarr/internal/layout"
)

// Method is how a placement was performed. Intent and outcome are recorded
// separately because permission handling is only legal on the branch that
// actually copied.
type Method string

const (
	MethodCopy Method = "copy"
	MethodLink Method = "hardlink"
)

// Step is one file placement.
type Step struct {
	Seq int
	Src string
	Dst string

	// Intent. The executor may fall back from link to copy, and records
	// which happened in Actual.
	Method Method
	Actual Method

	Bytes int64

	// Source identity at plan time, asserted again before publish (I13).
	SrcDev   uint64
	SrcIno   uint64
	SrcSize  int64
	SrcMtime time.Time

	CreatedByUs bool
	CreatedDirs []string
	Status      string
	Err         string
}

// Options control one execution.
type Options struct {
	// AllowLink permits the hardlink fast path for this pair. It is set
	// per (download root, library root) by the probe, never globally: an
	// all-or-nothing switch forces copies onto provably linkable pairs and
	// spends the one resource that never comes back.
	AllowLink bool

	FileMode fs.FileMode
	DirMode  fs.FileMode

	// ReserveBytes and ReserveFraction bound how full the destination may
	// get. The harm is not Orphanarr stopping; it is Orphanarr filling the
	// array so qBittorrent, Plex and the next download fail, with no
	// visible connection to us.
	ReserveBytes    uint64
	ReserveFraction float64

	// Collision decides what happens when the destination exists:
	// skip (default), suffix, fail. There is deliberately no overwrite.
	Collision string

	// BufSize bounds how long a copy can ignore cancellation. A step used
	// to be one link(2); it is now a multi-gigabyte read loop, so the kill
	// switch has to be honoured inside it rather than at step boundaries.
	BufSize int
}

func DefaultOptions() Options {
	return Options{
		FileMode:        0o644,
		DirMode:         0o755,
		ReserveBytes:    10 << 30,
		ReserveFraction: 0.05,
		Collision:       "skip",
		BufSize:         4 << 20,
	}
}

// Journal receives every mutation before it is attempted and again after it
// succeeds (I7). The NDJSON file it writes is redundant with the database,
// and that is the point: SQLite corruption, a botched migration or a
// docker run with the wrong -v all destroy the DB, and the only question
// that matters at that moment is "where did my files go".
type Journal interface {
	Before(ctx context.Context, s *Step) error
	After(ctx context.Context, s *Step) error
}

// Executor places files.
type Executor struct {
	fs   fsx.FS
	jrnl Journal
	opts Options
}

func New(f fsx.FS, j Journal, o Options) *Executor {
	if o.BufSize <= 0 {
		o.BufSize = 4 << 20
	}
	return &Executor{fs: f, jrnl: j, opts: o}
}

var (
	// ErrCollision means the destination exists with different content.
	ErrCollision = errors.New("exec: destination exists")
	// ErrSourceChanged is I13: the source moved under a copy in flight.
	ErrSourceChanged = errors.New("exec: source changed during copy")
	// ErrNoSpace is a refusal before any bytes were written.
	ErrNoSpace = errors.New("exec: insufficient free space")
)

// Preflight runs before any step. It is separate from Run because the user
// sees its result in the plan, before approving anything.
func (e *Executor) Preflight(steps []Step, dstRoot string) error {
	var need int64
	for i := range steps {
		need += steps[i].Bytes
	}

	st, err := e.fs.Statfs(dstRoot)
	if err != nil {
		return fmt.Errorf("exec: statfs %s: %w", dstRoot, err)
	}

	// Bavail, not Free. #C28 measured 11.61 GiB of root-reserved ext4
	// space that Free reports and an unprivileged process can never have —
	// budget against it and the last plan of a run fails with ENOSPC after
	// writing most of itself.
	reserve := e.opts.ReserveBytes
	if frac := uint64(float64(st.Total) * e.opts.ReserveFraction); frac > reserve {
		reserve = frac
	}
	if st.Avail < reserve || st.Avail-reserve < uint64(need) {
		return fmt.Errorf("%w: need %s, available %s, reserve %s on %s",
			ErrNoSpace, human(need), human(int64(st.Avail)), human(int64(reserve)), dstRoot)
	}

	// Every source must be READABLE, and there is no fallback if it is
	// not: safe_hardlink_source() requires MAY_READ|MAY_WRITE and systemd
	// ships fs.protected_hardlinks=1, so an unreadable source fails the
	// link path too.
	for i := range steps {
		f, err := e.fs.Open(steps[i].Src)
		if err != nil {
			return fmt.Errorf("exec: SRC_UNREADABLE %s: %w", steps[i].Src, err)
		}
		f.Close()
	}
	return nil
}

// Run executes steps in order, journalling each one.
func (e *Executor) Run(ctx context.Context, steps []Step) error {
	for i := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.runStep(ctx, &steps[i]); err != nil {
			steps[i].Status = "failed"
			steps[i].Err = err.Error()
			return err
		}
		steps[i].Status = "done"
	}
	return nil
}

func (e *Executor) runStep(ctx context.Context, s *Step) error {
	s.Status = "in_progress"
	if e.jrnl != nil {
		if err := e.jrnl.Before(ctx, s); err != nil {
			return err
		}
	}

	dirs, err := e.fs.MkdirAll(path.Dir(s.Dst), e.opts.DirMode)
	if err != nil {
		return err
	}
	s.CreatedDirs = dirs

	// Collision policy runs immediately before the write, not only at plan
	// time: plans sit in the review queue for hours or days by design, so
	// the check-to-publish window is the length of the queue.
	if _, err := e.fs.Stat(s.Dst); err == nil {
		switch e.opts.Collision {
		case "suffix":
			s.Dst = suffixed(s.Dst)
		case "fail":
			return fmt.Errorf("%w: %s", ErrCollision, s.Dst)
		default:
			// skip. A skip marks the PLAN partial, which is a visible,
			// actionable state — not a success.
			s.Status = "skipped"
			return nil
		}
	}

	if e.opts.AllowLink && s.Method == MethodLink {
		if err := e.fs.Link(s.Src, s.Dst); err == nil {
			s.Actual = MethodLink
			s.CreatedByUs = true
			if e.jrnl != nil {
				return e.jrnl.After(ctx, s)
			}
			return nil
		}
		// Attempt the operation and handle the errno; never predict.
		// Falling through to copy is the whole point of trying.
	}

	if err := e.copy(ctx, s); err != nil {
		return err
	}
	s.Actual = MethodCopy
	s.CreatedByUs = true
	if e.jrnl != nil {
		return e.jrnl.After(ctx, s)
	}
	return nil
}

// copy writes to a partial in the FINAL directory, verifies, and publishes
// without clobbering.
func (e *Executor) copy(ctx context.Context, s *Step) error {
	partial := s.Dst + layout.PartialSuffix

	src, err := e.fs.Open(s.Src)
	if err != nil {
		return err
	}
	defer src.Close()

	// O_CREAT|O_EXCL. #C34: a partial reused without O_TRUNC leaves a
	// stale tail that passes a size check.
	dst, err := e.fs.CreateExcl(partial, e.opts.FileMode)
	if err != nil {
		return fmt.Errorf("exec: create partial: %w", err)
	}

	cleanup := func() {
		dst.Close()
		// Unlink the partial on ANY exit that is not a successful publish,
		// including a collision routed to skip — which is not a failure
		// but leaves the same debris, and under copy-only that debris is a
		// full-size file.
		_ = e.fs.Remove(partial)
	}

	written, err := copyCtx(ctx, dst, src, e.opts.BufSize)
	if err != nil {
		cleanup()
		return err
	}
	if written != s.SrcSize && s.SrcSize > 0 {
		cleanup()
		return fmt.Errorf("exec: short copy: wrote %d of %d bytes", written, s.SrcSize)
	}

	// I13. #C29 produces a destination matching NEITHER the source's old
	// nor its new contents, at exactly the right size, after a successful
	// fsync. Size and fsync cannot detect it, and a checksum would not
	// either — the hash matches what we read. Only re-stat'ing the source
	// catches it. This class did not exist under hardlinking, because
	// link(2) never opened the source.
	if err := e.assertSourceUnchanged(s); err != nil {
		cleanup()
		return err
	}

	// fsync the file, then the directory. This is the step that gets
	// omitted, and without it a power loss leaves a correctly-named,
	// correctly-sized, silently zero-filled file that a scanner indexes
	// and the user finds six months later.
	if err := dst.Sync(); err != nil {
		cleanup()
		return err
	}
	// Set the mode on the PARTIAL, before publish. The hardlink path has
	// no partial and therefore structurally cannot reach a chmod, which is
	// what keeps I12 true by construction rather than by a recorded flag.
	if err := dst.Chmod(e.opts.FileMode); err != nil {
		cleanup()
		return err
	}
	if err := dst.Close(); err != nil {
		_ = e.fs.Remove(partial)
		return err
	}
	if err := e.fs.SyncDir(path.Dir(s.Dst)); err != nil {
		_ = e.fs.Remove(partial)
		return err
	}

	if err := e.publish(partial, s.Dst); err != nil {
		_ = e.fs.Remove(partial)
		return err
	}
	return nil
}

// publish moves the partial to its final name WITHOUT clobbering.
//
// This ladder is the sole reason I2 is mechanically true rather than
// aspirational. It must not be "simplified" to rename(2): rename destroys
// an existing destination silently and with no error (#C9 turned
// b'IRREPLACEABLE' into b'replacement'), the plan-time collision check is
// separated from this moment by the whole review queue, and rollback would
// then remove our copy too — leaving the user with NEITHER file and
// falsifying the claim that rollback is total because we only ever added.
func (e *Executor) publish(partial, dst string) error {
	// 1. link + unlink. Same directory, therefore same filesystem,
	//    therefore always available wherever rename was — and EEXIST
	//    routes to the collision policy instead of destroying anything.
	if err := e.fs.Link(partial, dst); err == nil {
		return e.fs.Remove(partial)
	} else if os.IsExist(err) {
		return fmt.Errorf("%w: %s", ErrCollision, dst)
	}

	// 2. Filesystems without hardlinks (exFAT, some SMB servers).
	if err := e.fs.RenameNoReplace(partial, dst); err == nil {
		return nil
	} else if os.IsExist(err) {
		return fmt.Errorf("%w: %s", ErrCollision, dst)
	}

	// There is deliberately no rung 3. The design's third fallback was a
	// re-checked plain rename with a recorded warning; this port does not
	// expose plain rename at all, so a filesystem supporting neither
	// primitive fails loudly rather than racing.
	return fmt.Errorf("exec: cannot publish %s: destination filesystem supports neither "+
		"link(2) nor renameat2(RENAME_NOREPLACE), and plain rename is not used because it "+
		"destroys an existing destination silently", dst)
}

func (e *Executor) assertSourceUnchanged(s *Step) error {
	fi, err := e.fs.Stat(s.Src)
	if err != nil {
		return fmt.Errorf("%w: source vanished: %v", ErrSourceChanged, err)
	}
	if s.SrcSize > 0 && fi.Size != s.SrcSize {
		return fmt.Errorf("%w: size %d -> %d", ErrSourceChanged, s.SrcSize, fi.Size)
	}
	if s.SrcIno != 0 && fi.Ino != s.SrcIno {
		return fmt.Errorf("%w: inode %d -> %d", ErrSourceChanged, s.SrcIno, fi.Ino)
	}
	if !s.SrcMtime.IsZero() && !fi.ModTime.Equal(s.SrcMtime) {
		return fmt.Errorf("%w: mtime %s -> %s", ErrSourceChanged,
			s.SrcMtime.Format(time.RFC3339), fi.ModTime.Format(time.RFC3339))
	}
	return nil
}

// copyCtx copies with cancellation checked every buffer.
//
// io.Copy would ignore ctx entirely, so the global kill switch would take
// as long as the largest remaining file — up to hours for a remux.
func copyCtx(ctx context.Context, dst io.Writer, src io.Reader, bufSize int) (int64, error) {
	buf := make([]byte, bufSize)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, rerr
		}
	}
}

// Rollback removes only what this run created, in reverse order, then
// removes directories it created if and only if they are empty.
//
// Because sources are never deleted, a completed rollback returns the
// filesystem to its exact pre-plan state.
func (e *Executor) Rollback(steps []Step) []error {
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if !s.CreatedByUs {
			continue
		}
		if err := e.fs.Remove(s.Dst); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
		for j := len(s.CreatedDirs) - 1; j >= 0; j-- {
			if err := e.fs.RemoveDirIfEmpty(s.CreatedDirs[j]); err != nil && !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

// Reconcile repairs state after an unclean exit, before any new work.
func (e *Executor) Reconcile(steps []Step) []error {
	var errs []error
	for i := range steps {
		s := &steps[i]
		if s.Status != "in_progress" {
			continue
		}

		fi, err := e.fs.Stat(s.Dst)
		switch {
		case err == nil && (s.SrcSize == 0 || fi.Size == s.SrcSize):
			s.Status = "done"
		case err == nil:
			// Exists but does not verify. We created it, so removing it is
			// safe — and leaving a short file where a scanner will index
			// it is not.
			if rmErr := e.fs.Remove(s.Dst); rmErr != nil {
				errs = append(errs, rmErr)
			}
			s.Status = "pending"
		default:
			s.Status = "pending"
		}

		// ALWAYS, after the branch resolves: sweep the partial. As an elif
		// this would never run, because link+unlink leaves BOTH names for
		// a window and a crash inside it leaves a dst that verifies.
		//
		// Journal-scoped: only a partial belonging to a step we recorded.
		// This is the only unlink in the design, and it must not remove a
		// file we did not create.
		partial := s.Dst + layout.PartialSuffix
		if err := e.fs.Remove(partial); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errs
}

func suffixed(p string) string {
	ext := path.Ext(p)
	return strings.TrimSuffix(p, ext) + " (2)" + ext
}

func human(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
