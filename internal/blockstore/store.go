// blockstore manages a volume: i.e. a directory of carrier files plus
// allocation state.
// It is carrier-agnostic: given a carrier.Carrier and a
// directory, it holds byte blocks and hands back opaque ids.
package blockstore

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andolivieri/kittyfs/internal/carrier"
	"github.com/andolivieri/kittyfs/internal/crypto"
)

type BlockID = uint64

const BlockSize = 4194304 // 4 MB

const superblockID BlockID = 0

const (
	superblockName = "blk_0000000000.png"
	sbMagic        = "KFSV"
	sbVersion      = 2
	saltLen        = 16
)

var ErrNotFound = errors.New("blockstore: block not found")

// carrier-agnostic "hold N bytes" abstraction.
type BlockStore interface {
	// Alloc reserves a fresh block and returns its id.
	Alloc() (BlockID, error)
	// Write replaces the contents of a block (whole-block write; v0 has no
	// partial writes). data must be <= BlockSize.
	Write(id BlockID, data []byte) error
	// Read returns the current payload of a block.
	Read(id BlockID) ([]byte, error)
	// Free marks a block reusable and deletes its carrier file.
	Free(id BlockID) error
	// SetRoot records the ordered block ids holding the serialized FS index.
	// It is an opaque pointer the FS layer persists via the superblock.
	SetRoot(blocks []BlockID)
	// Root returns the FS index block ids recorded by the last SetRoot.
	Root() []BlockID
	// Flush persists allocation metadata (superblock) to disk.
	Flush() error
}

// superblock is block 0's payload, with volume's allocation state, pointer
// to the FS index blocks, and KDF bootstrap.
// stored in plaintext
type superblock struct {
	Magic         string              `json:"magic"`
	FormatVersion int                 `json:"formatVersion"`
	BlockSize     int                 `json:"blockSize"`
	NextBlockID   BlockID             `json:"nextBlockID"`
	FreeList      []BlockID           `json:"freeList"`
	FSIndexBlocks []BlockID           `json:"fsIndexBlocks"`
	Argon2        crypto.Argon2Params `json:"argon2"`
	Salt          []byte              `json:"salt"`
	Verifier      []byte              `json:"verifier"`
}

// DirStore is the on-disk BlockStore: one carrier file per block in a
// directory ("blk_0000000001.png"), plus blk_0000000000.png for block 0.
type DirStore struct {
	dir     string
	carrier carrier.Carrier
	cipher  crypto.Cipher
	sb      *superblock
}

var _ BlockStore = (*DirStore)(nil)

// KeyDeriver turns a volume's salt + Argon2 params into a 32-byte key
type KeyDeriver func(salt []byte, params crypto.Argon2Params) ([crypto.KeyLen]byte, error)

// Initializes a brand-new empty encrypted volume in dir (created if
// needed) and writes its plaintext superblock: it generates a random salt,
// derives the key via derive, and stores KDF params/salt/verifier. It fails if
// a superblock already exists.
func Create(dir string, c carrier.Carrier, params crypto.Argon2Params, derive KeyDeriver) (*DirStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("blockstore: create dir: %w", err)
	}
	s := &DirStore{
		dir:     dir,
		carrier: c,
		sb: &superblock{
			Magic:         sbMagic,
			FormatVersion: sbVersion,
			BlockSize:     BlockSize,
			NextBlockID:   1, // block 0 is the superblock
			Argon2:        params,
		},
	}
	if _, err := os.Stat(s.superblockPath()); err == nil {
		return nil, fmt.Errorf("blockstore: volume already exists at %s", dir)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("blockstore: salt: %w", err)
	}
	s.sb.Salt = salt

	key, err := derive(salt, params)
	if err != nil {
		return nil, err
	}
	cipher, err := crypto.NewAEADCipher(key)
	if err != nil {
		return nil, err
	}
	verifier, err := cipher.MakeVerifier()
	if err != nil {
		return nil, fmt.Errorf("blockstore: verifier: %w", err)
	}
	s.sb.Verifier = verifier
	s.cipher = cipher

	if err := s.Flush(); err != nil {
		return nil, err
	}
	return s, nil
}

// loads an existing volume's plaintext superblock, re-derives the key via
// derive from the stored salt/params, and verifies it against the stored
// verifier — returning crypto.ErrWrongPassword on a bad password.
func Open(dir string, c carrier.Carrier, derive KeyDeriver) (*DirStore, error) {
	s := &DirStore{dir: dir, carrier: c}
	raw, err := os.ReadFile(s.superblockPath())
	if err != nil {
		return nil, fmt.Errorf("blockstore: open volume: %w", err)
	}
	_, payload, err := c.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("blockstore: decode superblock: %w", err)
	}
	var sb superblock
	if err := json.Unmarshal(payload, &sb); err != nil {
		return nil, fmt.Errorf("blockstore: parse superblock: %w", err)
	}
	if sb.Magic != sbMagic {
		return nil, fmt.Errorf("blockstore: bad superblock magic %q", sb.Magic)
	}
	s.sb = &sb

	key, err := derive(sb.Salt, sb.Argon2)
	if err != nil {
		return nil, err
	}
	cipher, err := crypto.NewAEADCipher(key)
	if err != nil {
		return nil, err
	}
	if err := cipher.CheckVerifier(sb.Verifier); err != nil {
		return nil, err
	}
	s.cipher = cipher
	return s, nil
}

// Reserves a fresh block id, reusing a freed id when available.
func (s *DirStore) Alloc() (BlockID, error) {
	if n := len(s.sb.FreeList); n > 0 {
		id := s.sb.FreeList[n-1]
		s.sb.FreeList = s.sb.FreeList[:n-1]
		return id, nil
	}
	id := s.sb.NextBlockID
	s.sb.NextBlockID++
	return id, nil
}

// Write encodes data through the cipher and carrier and writes the block's file.
func (s *DirStore) Write(id BlockID, data []byte) error {
	if id == superblockID {
		return fmt.Errorf("blockstore: block 0 is reserved for the superblock")
	}
	if len(data) > BlockSize {
		return fmt.Errorf("blockstore: block %d oversized: %d > %d", id, len(data), BlockSize)
	}
	payload, err := s.cipher.Seal(data)
	if err != nil {
		return fmt.Errorf("blockstore: seal block %d: %w", id, err)
	}
	png, err := s.carrier.Encode(id, payload, s.cipher.Encrypted())
	if err != nil {
		return fmt.Errorf("blockstore: encode block %d: %w", id, err)
	}
	return writeFileAtomic(s.blockPath(id), png)
}

// Read loads a block's PNG file and reverses the carrier + cipher.
func (s *DirStore) Read(id BlockID) ([]byte, error) {
	raw, err := os.ReadFile(s.blockPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: block %d", ErrNotFound, id)
		}
		return nil, fmt.Errorf("blockstore: read block %d: %w", id, err)
	}
	_, payload, err := s.carrier.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("blockstore: decode block %d: %w", id, err)
	}
	data, err := s.cipher.Open(payload)
	if err != nil {
		return nil, fmt.Errorf("blockstore: open block %d: %w", id, err)
	}
	return data, nil
}

// Free deletes a block's carrier file and returns its id to the free list.
func (s *DirStore) Free(id BlockID) error {
	if id == superblockID {
		return fmt.Errorf("blockstore: cannot free the superblock")
	}
	if err := os.Remove(s.blockPath(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("blockstore: free block %d: %w", id, err)
	}
	s.sb.FreeList = append(s.sb.FreeList, id)
	return nil
}

// SetRoot records the FS index block ids in the superblock (in memory until
// Flush).
func (s *DirStore) SetRoot(blocks []BlockID) {
	s.sb.FSIndexBlocks = append([]BlockID(nil), blocks...)
}

// Root returns the recorded FS index block ids.
func (s *DirStore) Root() []BlockID {
	return append([]BlockID(nil), s.sb.FSIndexBlocks...)
}

type Stats struct {
	Dir             string
	FormatVersion   int
	BlockSize       int
	AllocatedBlocks int
	FreeBlocks      int
	NextBlockID     BlockID
	IndexBlocks     int
	Encrypted       bool
	Argon2          crypto.Argon2Params
	SaltLen         int
	CarrierFiles    int
	CarrierBytes    int64
}

// Reports the volume's allocation and crypto state
func (s *DirStore) Stats() (Stats, error) {
	st := Stats{
		Dir:           s.dir,
		FormatVersion: s.sb.FormatVersion,
		BlockSize:     s.sb.BlockSize,
		FreeBlocks:    len(s.sb.FreeList),
		NextBlockID:   s.sb.NextBlockID,
		IndexBlocks:   len(s.sb.FSIndexBlocks),
		Encrypted:     s.cipher != nil && s.cipher.Encrypted(),
		Argon2:        s.sb.Argon2,
		SaltLen:       len(s.sb.Salt),
	}
	// Ids 1..NextBlockID-1 have been handed out; block 0 is the superblock.
	st.AllocatedBlocks = int(s.sb.NextBlockID) - 1 - st.FreeBlocks

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return Stats{}, fmt.Errorf("blockstore: stat volume: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "blk_") || !strings.HasSuffix(name, ".png") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return Stats{}, fmt.Errorf("blockstore: stat %s: %w", name, err)
		}
		st.CarrierFiles++
		st.CarrierBytes += info.Size()
	}
	return st, nil
}

func (s *DirStore) Flush() error {
	payload, err := json.Marshal(s.sb)
	if err != nil {
		return fmt.Errorf("blockstore: marshal superblock: %w", err)
	}
	// The superblock is always plaintext, so it
	// bypasses the cipher and is tagged as an unencrypted container.
	png, err := s.carrier.Encode(superblockID, payload, false)
	if err != nil {
		return fmt.Errorf("blockstore: encode superblock: %w", err)
	}
	return writeFileAtomic(s.superblockPath(), png)
}

func (s *DirStore) superblockPath() string {
	return filepath.Join(s.dir, superblockName)
}

func (s *DirStore) blockPath(id BlockID) string {
	if id == superblockID {
		return s.superblockPath()
	}
	return filepath.Join(s.dir, fmt.Sprintf("blk_%010d.png", id))
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
