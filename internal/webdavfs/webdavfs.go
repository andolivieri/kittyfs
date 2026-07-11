// webdavfs adapts an internal/fs.FS to the golang.org/x/net/webdav
// FileSystem/File interfaces
//
// Given the whole-file write model, a file opened for writing buffers all bytes
// in memory and does a single fs.WriteFile on Close; one opened for reading
// loads its full contents up front.
//
// A single coarse mutex serializes everything
package webdavfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	kfs "github.com/andolivieri/kittyfs/internal/fs"
)

type davFS struct {
	mu sync.Mutex
	fs kfs.FS
}

var _ webdav.FileSystem = (*davFS)(nil)

func New(fs kfs.FS) webdav.FileSystem {
	return &davFS{fs: fs}
}

// maps kittFS errors into the os.Err*
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, kfs.ErrNotExist):
		return os.ErrNotExist
	case errors.Is(err, kfs.ErrExist):
		return os.ErrExist
	default:
		return err
	}
}

func (d *davFS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fs.Mkdir(name); err != nil {
		return mapErr(err)
	}
	return mapErr(d.fs.Flush())
}

func (d *davFS) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	writing := flag&(os.O_WRONLY|os.O_RDWR) != 0
	node, err := d.fs.Stat(name)
	switch {
	case err == nil:
		if node.IsDir {
			return &davFile{d: d, name: name, node: node}, nil
		}
		if !writing {
			data, err := d.fs.ReadFile(name)
			if err != nil {
				return nil, mapErr(err)
			}
			return &davFile{d: d, name: name, node: node, reader: bytes.NewReader(data)}, nil
		}
		// Existing file open for writing: whole-file replace on Close.
		f := &davFile{d: d, name: name, node: node, wbuf: &bytes.Buffer{}}
		if flag&os.O_TRUNC == 0 {
			data, err := d.fs.ReadFile(name)
			if err != nil {
				return nil, mapErr(err)
			}
			f.wbuf.Write(data)
		}
		return f, nil
	case errors.Is(err, kfs.ErrNotExist):
		if flag&os.O_CREATE == 0 || !writing {
			return nil, os.ErrNotExist
		}
		return &davFile{
			d:      d,
			name:   name,
			node:   kfs.Inode{Name: path.Base(name), Mode: 0o644},
			wbuf:   &bytes.Buffer{},
			create: true,
		}, nil
	default:
		return nil, mapErr(err)
	}
}

func (d *davFS) RemoveAll(_ context.Context, name string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.removeAll(name); err != nil {
		return mapErr(err)
	}
	return mapErr(d.fs.Flush())
}

func (d *davFS) removeAll(name string) error {
	node, err := d.fs.Stat(name)
	if err != nil {
		if errors.Is(err, kfs.ErrNotExist) {
			return nil // DELETE of a missing path is a no-op success.
		}
		return err
	}
	if node.IsDir {
		entries, err := d.fs.List(name)
		if err != nil {
			return err
		}
		// children cleared depth-first.
		for _, e := range entries {
			if err := d.removeAll(path.Join(name, e.Name)); err != nil {
				return err
			}
		}
	}
	return d.fs.Remove(name)
}

func (d *davFS) Rename(_ context.Context, oldName, newName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.fs.Rename(oldName, newName); err != nil {
		return mapErr(err)
	}
	return mapErr(d.fs.Flush())
}

func (d *davFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	node, err := d.fs.Stat(name)
	if err != nil {
		return nil, mapErr(err)
	}
	return infoFromInode(name, node), nil
}

// open handle over a kittyFS path: a bytes.Reader for reads, an in-memory
// buffer flushed on Close for writes, or a directory for Readdir.
type davFile struct {
	d      *davFS
	name   string
	node   kfs.Inode
	reader *bytes.Reader // non-nil for a file opened read-only
	wbuf   *bytes.Buffer // non-nil for a file opened for writing
	create bool          // wbuf targets a not-yet-existing path
	dirOff int           // Readdir cursor
}

var _ webdav.File = (*davFile)(nil)

func (f *davFile) Read(p []byte) (int, error) {
	if f.reader == nil {
		return 0, os.ErrInvalid
	}
	return f.reader.Read(p)
}

func (f *davFile) Seek(offset int64, whence int) (int64, error) {
	if f.reader == nil {
		return 0, os.ErrInvalid
	}
	return f.reader.Seek(offset, whence)
}

func (f *davFile) Write(p []byte) (int, error) {
	if f.wbuf == nil {
		return 0, os.ErrInvalid
	}
	return f.wbuf.Write(p)
}

// Persists a write handle as one whole-file write, then flushes. Read and
// directory handles have nothing to persist.
func (f *davFile) Close() error {
	if f.wbuf == nil {
		return nil
	}
	buf := f.wbuf
	f.wbuf = nil // make Close idempotent
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if err := f.d.fs.WriteFile(f.name, buf.Bytes()); err != nil {
		return mapErr(err)
	}
	return mapErr(f.d.fs.Flush())
}

// Follows os.File semantics: count <= 0 returns everything; count > 0 returns
// up to count and io.EOF when done.
func (f *davFile) Readdir(count int) ([]os.FileInfo, error) {
	if !f.node.IsDir {
		return nil, os.ErrInvalid
	}
	f.d.mu.Lock()
	entries, err := f.d.fs.List(f.name)
	f.d.mu.Unlock()
	if err != nil {
		return nil, mapErr(err)
	}

	if f.dirOff >= len(entries) {
		if count <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	rest := entries[f.dirOff:]
	if count > 0 && count < len(rest) {
		rest = rest[:count]
	}
	out := make([]os.FileInfo, len(rest))
	for i, e := range rest {
		out[i] = infoFromInode(path.Join(f.name, e.Name), e)
	}
	f.dirOff += len(rest)
	return out, nil
}

func (f *davFile) Stat() (os.FileInfo, error) {
	if f.wbuf != nil {
		// A write in progress: report the buffered length and a fresh mtime.
		return davFileInfo{name: path.Base(f.name), size: int64(f.wbuf.Len()), mode: 0o644, mtime: time.Now()}, nil
	}
	return infoFromInode(f.name, f.node), nil
}

// os.FileInfo shim over a kittyFS inode.
type davFileInfo struct {
	name  string
	size  int64
	mode  os.FileMode
	mtime time.Time
	dir   bool
}

var _ os.FileInfo = davFileInfo{}

func (fi davFileInfo) Name() string { return fi.name }
func (fi davFileInfo) Size() int64  { return fi.size }
func (fi davFileInfo) Mode() os.FileMode {
	if fi.dir {
		return fi.mode | os.ModeDir
	}
	return fi.mode
}
func (fi davFileInfo) ModTime() time.Time { return fi.mtime }
func (fi davFileInfo) IsDir() bool        { return fi.dir }
func (fi davFileInfo) Sys() any           { return nil }

// name is the full path; only its base is exposed as Name(), except the root
// ("/") which keeps a non-empty name.
func infoFromInode(name string, n kfs.Inode) davFileInfo {
	base := path.Base(name)
	if base == "." || base == "/" || base == "" {
		base = "/"
	}
	return davFileInfo{
		name:  base,
		size:  n.Size,
		mode:  os.FileMode(n.Mode),
		mtime: time.Unix(n.Mtime, 0),
		dir:   n.IsDir,
	}
}
