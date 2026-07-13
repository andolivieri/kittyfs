package carrier

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// A tiny but genuine PNG, so tests do not depend on the embedded corpus.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 0xCA, G: 0x7F, B: 0x00, A: 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, b []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDirCoversRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.png"), testPNG(t))
	// Recursive: a cover in a subdirectory counts too.
	writeFile(t, filepath.Join(dir, "sub", "b.png"), testPNG(t))

	covers, err := NewDirCovers(dir)
	if err != nil {
		t.Fatalf("NewDirCovers: %v", err)
	}
	if covers.Count() != 2 {
		t.Fatalf("Count() = %d, want 2", covers.Count())
	}

	c := NewPNGCarrier(covers)
	payload := bytes.Repeat([]byte{0x42}, 8192)
	media, err := c.Encode(9, payload, true)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// The block must still open as a normal image.
	if _, err := png.Decode(bytes.NewReader(media)); err != nil {
		t.Fatalf("block is not a valid PNG: %v", err)
	}

	// The headline property: decoding consults no CoverSource at all, so a
	// carrier that has never seen the corpus reads the block back.
	stock := NewPNGCarrier(nil)
	id, got, err := stock.Decode(media)
	if err != nil {
		t.Fatalf("decode without a corpus: %v", err)
	}
	if id != 9 || !bytes.Equal(got, payload) {
		t.Fatalf("got id=%d, %d payload bytes; want 9, %d", id, len(got), len(payload))
	}
}

func TestDirCoversSingleFile(t *testing.T) {
	path := writeFile(t, filepath.Join(t.TempDir(), "one.png"), testPNG(t))

	covers, err := NewDirCovers(path)
	if err != nil {
		t.Fatalf("NewDirCovers: %v", err)
	}
	if covers.Count() != 1 {
		t.Fatalf("Count() = %d, want 1", covers.Count())
	}
	if _, err := covers.Cover(0); err != nil {
		t.Fatalf("Cover: %v", err)
	}
}

func TestDirCoversSkipsAndCountsNonPNGs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cat.png"), testPNG(t))
	writeFile(t, filepath.Join(dir, "notes.txt"), []byte("hello"))
	writeFile(t, filepath.Join(dir, "sub", "data.bin"), []byte{0x00, 0x01})

	covers, err := NewDirCovers(dir)
	if err != nil {
		t.Fatalf("NewDirCovers: %v", err)
	}
	if covers.Count() != 1 {
		t.Errorf("Count() = %d, want 1", covers.Count())
	}
	if covers.Skipped() != 2 {
		t.Errorf("Skipped() = %d, want 2", covers.Skipped())
	}
}

func TestDirCoversEmptyDirErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), []byte("no cats here"))

	if _, err := NewDirCovers(dir); err == nil {
		t.Fatal("NewDirCovers on a PNG-less dir: want error, got nil")
	}
}

// A file named .png that is not one must fail at load, not halfway through a
// 100 MB import.
func TestDirCoversFakePNGErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "real.png"), testPNG(t))
	fake := writeFile(t, filepath.Join(dir, "fake.png"), []byte("I am not a PNG"))

	_, err := NewDirCovers(dir)
	if err == nil {
		t.Fatal("NewDirCovers with a fake PNG: want error, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte(filepath.Base(fake))) {
		t.Errorf("error should name the offending path, got: %v", err)
	}
}

// Using a kittyfs volume as its own corpus is a footgun; close it.
func TestDirCoversRejectsKittyfsBlock(t *testing.T) {
	block, err := newTestCarrier(t).Encode(1, []byte("secret"), false)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "blk_0000000001.png"), block)

	if _, err := NewDirCovers(dir); err == nil {
		t.Fatal("NewDirCovers on a kittyfs volume: want error, got nil")
	}
}

func TestHasKiFSChunk(t *testing.T) {
	plain := testPNG(t)
	if has, err := HasKiFSChunk(bytes.NewReader(plain)); err != nil || has {
		t.Fatalf("plain PNG: has=%v err=%v, want false/nil", has, err)
	}

	block, err := NewPNGCarrier(staticCover{plain}).Encode(2, []byte("payload"), true)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := HasKiFSChunk(bytes.NewReader(block)); err != nil || !has {
		t.Fatalf("kittyfs block: has=%v err=%v, want true/nil", has, err)
	}
}
