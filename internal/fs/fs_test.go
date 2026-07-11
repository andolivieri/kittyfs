package fs

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/andolivieri/kittyfs/internal/blockstore"
	"github.com/andolivieri/kittyfs/internal/carrier"
	"github.com/andolivieri/kittyfs/internal/crypto"
)

func newTestStore(t *testing.T) *blockstore.DirStore {
	t.Helper()
	cats, err := carrier.NewEmbeddedCats()
	if err != nil {
		t.Fatal(err)
	}
	store, err := blockstore.Create(t.TempDir(), carrier.NewPNGCarrier(cats), crypto.DefaultArgon2Params(), testDerive)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// testDerive is a cheap fixed-key deriver so tests exercise real AES-256-GCM
// without paying Argon2's cost on every run.
func testDerive(salt []byte, _ crypto.Argon2Params) ([crypto.KeyLen]byte, error) {
	var k [crypto.KeyLen]byte
	copy(k[:], []byte("kittyfs-test-key-000000000000000"))
	return k, nil
}

func TestFileRoundTripAcrossReopen(t *testing.T) {
	store := newTestStore(t)
	dir := store // keep the same underlying directory

	big := make([]byte, 3*blockstore.BlockSize+123) // spans several blocks
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}

	vfs := Create(store)
	mustNil(t, vfs.Mkdir("sub"))
	mustNil(t, vfs.WriteFile("sub/data.bin", big))
	mustNil(t, vfs.WriteFile("hi.txt", []byte("hello")))
	mustNil(t, vfs.Flush())

	// Reopen from persisted index (proves Flush/Open round-trips).
	reopened, err := reopen(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.ReadFile("sub/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Fatalf("data.bin mismatch: %d vs %d bytes", len(got), len(big))
	}

	entries, err := reopened.List("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("root entries = %d, want 2", len(entries))
	}
}

func TestRemoveFreesAndReuses(t *testing.T) {
	store := newTestStore(t)
	vfs := Create(store)

	payload := bytes.Repeat([]byte{0x5A}, 5*blockstore.BlockSize)
	mustNil(t, vfs.WriteFile("a.bin", payload))
	mustNil(t, vfs.Remove("a.bin"))

	// After removal the file is gone and reading it errors as not-exist.
	if _, err := vfs.ReadFile("a.bin"); !errors.Is(err, ErrNotExist) {
		t.Fatalf("read removed file err = %v, want ErrNotExist", err)
	}

	// Re-adding should reuse freed block ids rather than growing NextBlockID.
	mustNil(t, vfs.WriteFile("b.bin", payload))
	got, err := vfs.ReadFile("b.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("b.bin mismatch after reuse")
	}
}

func TestReplaceOverwritesFile(t *testing.T) {
	store := newTestStore(t)
	vfs := Create(store)
	mustNil(t, vfs.WriteFile("f", []byte("first-and-longer")))
	mustNil(t, vfs.WriteFile("f", []byte("second")))
	got, err := vfs.ReadFile("f")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Fatalf("got %q, want %q", got, "second")
	}
}

func TestInvalidPaths(t *testing.T) {
	vfs := Create(newTestStore(t))
	for _, p := range []string{"../escape", "a/../b", "."} {
		if err := vfs.WriteFile(p, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) = nil, want error", p)
		}
	}
}

func reopen(store *blockstore.DirStore) (*VolumeFS, error) {
	return Open(store)
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
