package fsx

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// osFS is the real filesystem. It is deliberately unguarded — the guard is a
// separate wrapper so that the fault-injecting implementation and the real
// one are protected by the same code rather than by two copies of it.
type osFS struct{}

// NewOS returns an unguarded filesystem. Production callers want NewGuarded.
func NewOS() FS { return osFS{} }

func toInfo(name string, fi os.FileInfo) FileInfo {
	out := FileInfo{
		Name:    name,
		Size:    fi.Size(),
		Mode:    fi.Mode(),
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		out.Dev = uint64(st.Dev)
		out.Ino = st.Ino
		out.Nlink = uint64(st.Nlink)
		out.UID = st.Uid
		out.GID = st.Gid
	}
	return out
}

func (osFS) Stat(path string) (FileInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return toInfo(path, fi), nil
}

func (osFS) Lstat(path string) (FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, err
	}
	return toInfo(path, fi), nil
}

func (osFS) Open(path string) (io.ReadCloser, error) { return os.Open(path) }

func (osFS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }

func (osFS) Statfs(path string) (StatfsInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return StatfsInfo{}, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	bs := uint64(st.Bsize)
	info := StatfsInfo{
		Total: st.Blocks * bs,
		Free:  st.Bfree * bs,
		Avail: st.Bavail * bs,
	}
	// Statfs.Fsid is not the same identifier st_dev gives, and the design
	// accounts free space per st_dev. Take it from a stat of the path.
	if fi, err := os.Stat(path); err == nil {
		if s, ok := fi.Sys().(*syscall.Stat_t); ok && s != nil {
			info.Dev = uint64(s.Dev)
		}
	}
	return info, nil
}

type osFile struct{ f *os.File }

func (o osFile) Write(p []byte) (int, error) { return o.f.Write(p) }
func (o osFile) Close() error                { return o.f.Close() }
func (o osFile) Sync() error                 { return o.f.Sync() }
func (o osFile) Chmod(m fs.FileMode) error   { return o.f.Chmod(m) }
func (o osFile) Stat() (FileInfo, error) {
	fi, err := o.f.Stat()
	if err != nil {
		return FileInfo{}, err
	}
	return toInfo(o.f.Name(), fi), nil
}

func (osFS) CreateExcl(path string, perm fs.FileMode) (File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return nil, err
	}
	return osFile{f}, nil
}

// MkdirAll reports every directory it created. os.MkdirAll does not, and
// rollback cannot remove "directories this job created" without the list.
func (o osFS) MkdirAll(path string, perm fs.FileMode) ([]string, error) {
	path = filepath.Clean(path)
	if fi, err := os.Stat(path); err == nil {
		if !fi.IsDir() {
			return nil, fmt.Errorf("fsx: %s exists and is not a directory", path)
		}
		return nil, nil
	}

	// Find the deepest existing ancestor, then create downward so the
	// created list is ordered shallowest-first.
	var missing []string
	cur := path
	for {
		if _, err := os.Stat(cur); err == nil {
			break
		}
		missing = append(missing, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	var created []string
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], perm); err != nil {
			if os.IsExist(err) {
				continue
			}
			return created, err
		}
		// #C24: mkdir's mode argument is masked by umask, so the mode we
		// asked for is not the mode we got. An explicit chmod is the only
		// way to deliver dir_mode, and I12 permits it because this is a
		// directory we just created.
		if err := os.Chmod(missing[i], perm); err != nil {
			return created, err
		}
		created = append(created, missing[i])
	}
	return created, nil
}

func (osFS) Link(oldpath, newpath string) error { return os.Link(oldpath, newpath) }

func (osFS) RenameNoReplace(oldpath, newpath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
	if err != nil {
		return &os.LinkError{Op: "renameat2", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}

func (osFS) Remove(path string) error { return os.Remove(path) }

func (osFS) RemoveDirIfEmpty(path string) error {
	// os.Remove on a directory is rmdir(2), which already refuses a
	// non-empty directory with ENOTEMPTY. Relying on that is better than
	// racing a ReadDir check against a concurrent scanner.
	err := os.Remove(path)
	if err != nil {
		if errno, ok := err.(*os.PathError); ok {
			if errno.Err == syscall.ENOTEMPTY || errno.Err == syscall.EEXIST {
				return nil
			}
		}
	}
	return err
}

func (osFS) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }

func (osFS) SyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
