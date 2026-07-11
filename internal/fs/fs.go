// fs implements POSIX-ish filesystem semantics (inodes, a directory tree,
// whole-file reads/writes) on top of a blockstore.BlockStore.
// It never touches a Carrier or a media file directly.
package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/andolivieri/kittyfs/internal/blockstore"
)

type InodeID = uint64

const rootID InodeID = 1

var (
	ErrNotExist    = errors.New("fs: no such file or directory")
	ErrExist       = errors.New("fs: file already exists")
	ErrNotDir      = errors.New("fs: not a directory")
	ErrIsDir       = errors.New("fs: is a directory")
	ErrNotEmpty    = errors.New("fs: directory not empty")
	ErrInvalidPath = errors.New("fs: invalid path")
)

type Inode struct {
	ID       InodeID   `json:"id"`
	Name     string    `json:"name"`
	IsDir    bool      `json:"isDir"`
	Size     int64     `json:"size"`
	Mode     uint32    `json:"mode"`
	Mtime    int64     `json:"mtime"`
	Children []InodeID `json:"children,omitempty"` // dir: child inode ids
	Blocks   []uint64  `json:"blocks,omitempty"`   // file: ordered data block ids
}

// whole-volume filesystem metadata: the inode table and the directory tree
// root. Serialized as JSON into reserved index blocks on Flush.
type Index struct {
	Root   InodeID            `json:"root"`
	Inodes map[InodeID]*Inode `json:"inodes"`
	NextID InodeID            `json:"nextID"`
}

// inodes + directory tree built entirely on a blockstore.BlockStore.
// File updates are whole-file rewrites.
type FS interface {
	Mkdir(path string) error
	WriteFile(path string, data []byte) error
	ReadFile(path string) ([]byte, error)
	List(path string) ([]Inode, error)
	Stat(path string) (Inode, error)
	Rename(oldPath, newPath string) error
	Remove(path string) error
	Flush() error
}

type VolumeFS struct {
	store blockstore.BlockStore
	index *Index
}

var _ FS = (*VolumeFS)(nil)

// Builds an empty filesystem (just a root directory). Caller must Flush.
func Create(store blockstore.BlockStore) *VolumeFS {
	now := time.Now().Unix()
	root := &Inode{ID: rootID, Name: "", IsDir: true, Mode: 0o755, Mtime: now}
	return &VolumeFS{
		store: store,
		index: &Index{
			Root:   rootID,
			Inodes: map[InodeID]*Inode{rootID: root},
			NextID: rootID + 1,
		},
	}
}

// Reconstructs the filesystem from the index blocks recorded in the superblock.
func Open(store blockstore.BlockStore) (*VolumeFS, error) {
	blocks := store.Root()
	if len(blocks) == 0 {
		// A flush always records at least one block, so no blocks means the
		// volume predates any index write — treat as empty.
		return Create(store), nil
	}
	var buf []byte
	for _, id := range blocks {
		b, err := store.Read(id)
		if err != nil {
			return nil, fmt.Errorf("fs: read index block %d: %w", id, err)
		}
		buf = append(buf, b...)
	}
	var idx Index
	if err := json.Unmarshal(buf, &idx); err != nil {
		return nil, fmt.Errorf("fs: parse index: %w", err)
	}
	if idx.Inodes == nil {
		idx.Inodes = map[InodeID]*Inode{}
	}
	return &VolumeFS{store: store, index: &idx}, nil
}

// Parent must exist.
func (v *VolumeFS) Mkdir(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: cannot mkdir root", ErrExist)
	}
	parent, err := v.resolveDir(parts[:len(parts)-1])
	if err != nil {
		return err
	}
	name := parts[len(parts)-1]
	if v.childByName(parent, name) != nil {
		return fmt.Errorf("%w: %s", ErrExist, p)
	}
	now := time.Now().Unix()
	dir := &Inode{ID: v.index.NextID, Name: name, IsDir: true, Mode: 0o755, Mtime: now}
	v.index.NextID++
	v.index.Inodes[dir.ID] = dir
	parent.Children = append(parent.Children, dir.ID)
	parent.Mtime = now
	return nil
}

// Creates or whole-file-replaces path. Parent dirs must already exist.
// On replace, the file's previous data blocks are freed.
func (v *VolumeFS) WriteFile(p string, data []byte) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: cannot write root", ErrIsDir)
	}
	parent, err := v.resolveDir(parts[:len(parts)-1])
	if err != nil {
		return err
	}
	name := parts[len(parts)-1]

	now := time.Now().Unix()
	existing := v.childByName(parent, name)
	if existing != nil {
		if existing.IsDir {
			return fmt.Errorf("%w: %s", ErrIsDir, p)
		}
		if err := v.freeBlocks(existing.Blocks); err != nil {
			return err
		}
		existing.Blocks = nil
	}

	blocks, err := v.writeBlocks(data)
	if err != nil {
		return err
	}

	if existing != nil {
		existing.Blocks = blocks
		existing.Size = int64(len(data))
		existing.Mtime = now
	} else {
		file := &Inode{
			ID:     v.index.NextID,
			Name:   name,
			Mode:   0o644,
			Mtime:  now,
			Size:   int64(len(data)),
			Blocks: blocks,
		}
		v.index.NextID++
		v.index.Inodes[file.ID] = file
		parent.Children = append(parent.Children, file.ID)
		parent.Mtime = now
	}
	return nil
}

func (v *VolumeFS) ReadFile(p string) ([]byte, error) {
	node, err := v.resolve(p)
	if err != nil {
		return nil, err
	}
	if node.IsDir {
		return nil, fmt.Errorf("%w: %s", ErrIsDir, p)
	}
	out := make([]byte, 0, node.Size)
	for _, id := range node.Blocks {
		b, err := v.store.Read(id)
		if err != nil {
			return nil, fmt.Errorf("fs: read %s: %w", p, err)
		}
		out = append(out, b...)
	}
	return out, nil
}

func (v *VolumeFS) List(p string) ([]Inode, error) {
	node, err := v.resolve(p)
	if err != nil {
		return nil, err
	}
	if !node.IsDir {
		return nil, fmt.Errorf("%w: %s", ErrNotDir, p)
	}
	entries := make([]Inode, 0, len(node.Children))
	for _, cid := range node.Children {
		if c := v.index.Inodes[cid]; c != nil {
			entries = append(entries, *c)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (v *VolumeFS) Stat(p string) (Inode, error) {
	node, err := v.resolve(p)
	if err != nil {
		return Inode{}, err
	}
	return *node, nil
}

// Re-parents and/or renames in place, with no block re-encoding.
// The destination's parent must exist and the destination must not.
func (v *VolumeFS) Rename(oldPath, newPath string) error {
	oldParts, err := splitPath(oldPath)
	if err != nil {
		return err
	}
	if len(oldParts) == 0 {
		return fmt.Errorf("%w: cannot rename root", ErrInvalidPath)
	}
	newParts, err := splitPath(newPath)
	if err != nil {
		return err
	}
	if len(newParts) == 0 {
		return fmt.Errorf("%w: cannot overwrite root", ErrExist)
	}

	oldParent, err := v.resolveDir(oldParts[:len(oldParts)-1])
	if err != nil {
		return err
	}
	node := v.childByName(oldParent, oldParts[len(oldParts)-1])
	if node == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, oldPath)
	}

	newParent, err := v.resolveDir(newParts[:len(newParts)-1])
	if err != nil {
		return err
	}
	newName := newParts[len(newParts)-1]
	if v.childByName(newParent, newName) != nil {
		return fmt.Errorf("%w: %s", ErrExist, newPath)
	}
	if node.IsDir && v.isSelfOrDescendant(node.ID, newParent.ID) {
		return fmt.Errorf("%w: cannot move %s into itself", ErrInvalidPath, oldPath)
	}

	oldParent.Children = removeID(oldParent.Children, node.ID)
	node.Name = newName
	newParent.Children = append(newParent.Children, node.ID)
	now := time.Now().Unix()
	oldParent.Mtime = now
	newParent.Mtime = now
	node.Mtime = now
	return nil
}

func (v *VolumeFS) isSelfOrDescendant(ancestor, target InodeID) bool {
	if ancestor == target {
		return true
	}
	a := v.index.Inodes[ancestor]
	if a == nil {
		return false
	}
	for _, cid := range a.Children {
		if v.isSelfOrDescendant(cid, target) {
			return true
		}
	}
	return false
}

// Frees the data blocks. Directories must be empty.
func (v *VolumeFS) Remove(p string) error {
	parts, err := splitPath(p)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: cannot remove root", ErrIsDir)
	}
	parent, err := v.resolveDir(parts[:len(parts)-1])
	if err != nil {
		return err
	}
	name := parts[len(parts)-1]
	node := v.childByName(parent, name)
	if node == nil {
		return fmt.Errorf("%w: %s", ErrNotExist, p)
	}
	if node.IsDir && len(node.Children) > 0 {
		return fmt.Errorf("%w: %s", ErrNotEmpty, p)
	}
	if err := v.freeBlocks(node.Blocks); err != nil {
		return err
	}
	delete(v.index.Inodes, node.ID)
	parent.Children = removeID(parent.Children, node.ID)
	parent.Mtime = time.Now().Unix()
	return nil
}

// What the user put in the volume, as opposed to how the blockstore laid it out.
type Stats struct {
	Files      int
	Dirs       int   // excludes the root directory
	Bytes      int64 // logical size of all files, not the space they take on disk
	FileBlocks int
}

func (v *VolumeFS) Stats() Stats {
	var st Stats
	for id, n := range v.index.Inodes {
		switch {
		case id == v.index.Root:
			continue
		case n.IsDir:
			st.Dirs++
		default:
			st.Files++
			st.Bytes += n.Size
			st.FileBlocks += len(n.Blocks)
		}
	}
	return st
}

// Serializes the Index into fresh index blocks, records them in the store's
// root pointer, and persists the superblock.
func (v *VolumeFS) Flush() error {
	data, err := json.Marshal(v.index)
	if err != nil {
		return fmt.Errorf("fs: marshal index: %w", err)
	}
	// Recycle the previous index blocks before allocating new ones so ids get
	// reused rather than leaked.
	if err := v.freeBlocks(v.store.Root()); err != nil {
		return err
	}
	blocks, err := v.writeBlocks(data)
	if err != nil {
		return err
	}
	v.store.SetRoot(blocks)
	return v.store.Flush()
}

// --- helpers ---

// Splits data into BlockSize chunks and returns the ordered block ids.
// A zero-length input still produces one empty block, so every object owns at
// least one block.
func (v *VolumeFS) writeBlocks(data []byte) ([]uint64, error) {
	var blocks []uint64
	for off := 0; off < len(data); off += blockstore.BlockSize {
		end := off + blockstore.BlockSize
		if end > len(data) {
			end = len(data)
		}
		id, err := v.store.Alloc()
		if err != nil {
			return nil, err
		}
		if err := v.store.Write(id, data[off:end]); err != nil {
			return nil, err
		}
		blocks = append(blocks, id)
	}
	if len(blocks) == 0 {
		id, err := v.store.Alloc()
		if err != nil {
			return nil, err
		}
		if err := v.store.Write(id, nil); err != nil {
			return nil, err
		}
		blocks = append(blocks, id)
	}
	return blocks, nil
}

func (v *VolumeFS) freeBlocks(blocks []uint64) error {
	for _, id := range blocks {
		if err := v.store.Free(id); err != nil {
			return err
		}
	}
	return nil
}

func (v *VolumeFS) resolve(p string) (*Inode, error) {
	parts, err := splitPath(p)
	if err != nil {
		return nil, err
	}
	node := v.index.Inodes[v.index.Root]
	for i, name := range parts {
		if !node.IsDir {
			return nil, fmt.Errorf("%w: %s", ErrNotDir, "/"+strings.Join(parts[:i], "/"))
		}
		child := v.childByName(node, name)
		if child == nil {
			return nil, fmt.Errorf("%w: %s", ErrNotExist, p)
		}
		node = child
	}
	return node, nil
}

func (v *VolumeFS) resolveDir(parts []string) (*Inode, error) {
	node := v.index.Inodes[v.index.Root]
	for i, name := range parts {
		child := v.childByName(node, name)
		if child == nil {
			return nil, fmt.Errorf("%w: /%s", ErrNotExist, strings.Join(parts[:i+1], "/"))
		}
		if !child.IsDir {
			return nil, fmt.Errorf("%w: /%s", ErrNotDir, strings.Join(parts[:i+1], "/"))
		}
		node = child
	}
	return node, nil
}

func (v *VolumeFS) childByName(dir *Inode, name string) *Inode {
	for _, cid := range dir.Children {
		if c := v.index.Inodes[cid]; c != nil && c.Name == name {
			return c
		}
	}
	return nil
}

// Splits p into components, rejecting anything that could escape the tree.
// Backslashes count as separators (Windows). The root ("/", "", ".") yields an
// empty slice. Unlike path.Clean, ".." is rejected rather than rewritten.
func splitPath(p string) ([]string, error) {
	raw := strings.Split(strings.ReplaceAll(p, "\\", "/"), "/")
	var parts []string
	for _, part := range raw {
		switch part {
		case "", ".":
			continue
		case "..":
			return nil, fmt.Errorf("%w: %s", ErrInvalidPath, p)
		default:
			parts = append(parts, part)
		}
	}
	return parts, nil
}

func removeID(ids []InodeID, id InodeID) []InodeID {
	out := ids[:0]
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
