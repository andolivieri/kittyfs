package carrier

import (
	"bytes"
	"errors"
	"image/png"
	"testing"
)

func newTestCarrier(t *testing.T) *PNGCarrier {
	t.Helper()
	cats, err := NewEmbeddedCats()
	if err != nil {
		t.Fatalf("embedded cats: %v", err)
	}
	return NewPNGCarrier(cats)
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	c := newTestCarrier(t)
	cases := [][]byte{
		nil,
		[]byte("hello kitty"),
		bytes.Repeat([]byte{0xAB}, 64*1024),
	}
	for i, payload := range cases {
		id := uint64(i * 7)
		media, err := c.Encode(id, payload, i%2 == 1)
		if err != nil {
			t.Fatalf("encode[%d]: %v", i, err)
		}
		// The carrier output must still decode as a real image.
		if _, err := png.Decode(bytes.NewReader(media)); err != nil {
			t.Fatalf("encode[%d]: output is not a valid PNG: %v", i, err)
		}
		gotID, gotPayload, err := c.Decode(media)
		if err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		if gotID != id {
			t.Errorf("decode[%d]: blockID = %d, want %d", i, gotID, id)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Errorf("decode[%d]: payload mismatch (%d vs %d bytes)", i, len(gotPayload), len(payload))
		}
	}
}

func TestDecodePlainCatIsNotACarrier(t *testing.T) {
	cats, err := NewEmbeddedCats()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cats.Cover(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&PNGCarrier{covers: cats}).Decode(plain); !errors.Is(err, ErrNotACarrier) {
		t.Fatalf("plain cat Decode err = %v, want ErrNotACarrier", err)
	}
}

func TestEncodeIsIdempotentOnCarrier(t *testing.T) {
	c := newTestCarrier(t)
	first, err := c.Encode(3, []byte("one"), false)
	if err != nil {
		t.Fatal(err)
	}
	// Re-encoding an already-carrier file (via a cover that has a kiFS chunk)
	// must not stack chunks. Decode still yields the new payload only.
	c2 := &PNGCarrier{covers: staticCover{first}}
	second, err := c2.Encode(3, []byte("two"), false)
	if err != nil {
		t.Fatal(err)
	}
	id, payload, err := c.Decode(second)
	if err != nil {
		t.Fatal(err)
	}
	if id != 3 || string(payload) != "two" {
		t.Fatalf("got id=%d payload=%q, want 3/\"two\"", id, payload)
	}
	if bytes.Count(second, []byte(kiFSType)) != 1 {
		t.Errorf("expected exactly one kiFS chunk, found %d", bytes.Count(second, []byte(kiFSType)))
	}
}

// staticCover always returns the same media bytes, ignoring blockID.
type staticCover struct{ b []byte }

func (s staticCover) Cover(uint64) ([]byte, error) { return s.b, nil }
func (s staticCover) Count() int                    { return 1 }
