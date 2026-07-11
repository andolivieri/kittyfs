package carrier

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// ancillary chunk type
const kiFSType = "kiFS"

const (
	kiFSMagic        = "KFS1"
	kiFSVersionPlain = 0x01
	kiFSVersionEnc   = 0x02 // nonce||ciphertext||tag
	kiFSHeaderLen    = 20   // magic(4)+version(1)+flags(1)+reserved(2)+blockID(8)+payloadLen(4)
	flagEncrypted    = 0x01
)

var ErrBadPNG = errors.New("carrier: not a valid PNG")

// parse PNG chunk
type chunk struct {
	typ  string
	data []byte
}

// Stores block payloads inside a private "kiFS" ancillary PNG chunk of a cover
// cat image. Standard PNG decoders render the cat and ignore the chunk.
type PNGCarrier struct {
	covers CoverSource
}

func NewPNGCarrier(covers CoverSource) *PNGCarrier {
	return &PNGCarrier{covers: covers}
}

func (c *PNGCarrier) Ext() string { return ".png" }

// 0 = unbounded by the carrier itself; the block store picks the block size.
func (c *PNGCarrier) MaxPayload() int { return 0 }

// Parses the cover PNG, builds the kiFS container and inserts it just before
// IEND (leaving IHDR/IDAT untouched).
func (c *PNGCarrier) Encode(blockID uint64, payload []byte, encrypted bool) ([]byte, error) {
	cover, err := c.covers.Cover(blockID)
	if err != nil {
		return nil, fmt.Errorf("carrier: cover for block %d: %w", blockID, err)
	}

	chunks, err := parseChunks(cover)
	if err != nil {
		return nil, fmt.Errorf("carrier: parse cover: %w", err)
	}

	container := buildKiFS(blockID, payload, encrypted)

	// Drop any pre-existing kiFS chunk, keeping Encode idempotent on covers
	// that already carry data.
	out := make([]chunk, 0, len(chunks)+1)
	for _, ch := range chunks {
		if ch.typ == kiFSType {
			continue
		}
		if ch.typ == "IEND" {
			out = append(out, chunk{typ: kiFSType, data: container})
		}
		out = append(out, ch)
	}

	return writeChunks(out), nil
}

func (c *PNGCarrier) Decode(mediaFile []byte) (blockID uint64, payload []byte, err error) {
	chunks, err := parseChunks(mediaFile)
	if err != nil {
		return 0, nil, fmt.Errorf("carrier: parse: %w", err)
	}

	for _, ch := range chunks {
		if ch.typ != kiFSType {
			continue
		}
		return parseKiFS(ch.data)
	}
	return 0, nil, ErrNotACarrier
}

func buildKiFS(blockID uint64, payload []byte, encrypted bool) []byte {
	buf := make([]byte, kiFSHeaderLen+len(payload))
	copy(buf[0:4], kiFSMagic)
	if encrypted {
		buf[4] = kiFSVersionEnc
		buf[5] = flagEncrypted
	} else {
		buf[4] = kiFSVersionPlain
		buf[5] = 0x00
	}
	// buf[6:8] reserved, already zero
	binary.BigEndian.PutUint64(buf[8:16], blockID)
	binary.BigEndian.PutUint32(buf[16:20], uint32(len(payload)))
	copy(buf[20:], payload)
	return buf
}

func parseKiFS(data []byte) (blockID uint64, payload []byte, err error) {
	if len(data) < kiFSHeaderLen {
		return 0, nil, fmt.Errorf("carrier: kiFS chunk too short (%d bytes)", len(data))
	}
	if string(data[0:4]) != kiFSMagic {
		return 0, nil, fmt.Errorf("carrier: bad kiFS magic %q", data[0:4])
	}
	if data[4] != kiFSVersionPlain && data[4] != kiFSVersionEnc {
		return 0, nil, fmt.Errorf("carrier: unsupported kiFS version %d", data[4])
	}
	blockID = binary.BigEndian.Uint64(data[8:16])
	payloadLen := binary.BigEndian.Uint32(data[16:20])
	if int(payloadLen) > len(data)-kiFSHeaderLen {
		return 0, nil, fmt.Errorf("carrier: kiFS payloadLen %d exceeds chunk", payloadLen)
	}
	payload = make([]byte, payloadLen)
	copy(payload, data[kiFSHeaderLen:kiFSHeaderLen+int(payloadLen)])
	return blockID, payload, nil
}

// Splits a PNG into its ordered chunks, validating signature and CRCs.
func parseChunks(png []byte) ([]chunk, error) {
	if len(png) < len(pngSignature) || string(png[:len(pngSignature)]) != string(pngSignature) {
		return nil, ErrBadPNG
	}

	var chunks []chunk
	pos := len(pngSignature)
	for pos < len(png) {
		if pos+8 > len(png) {
			return nil, fmt.Errorf("%w: truncated chunk header at %d", ErrBadPNG, pos)
		}
		length := binary.BigEndian.Uint32(png[pos : pos+4])
		typ := string(png[pos+4 : pos+8])
		dataStart := pos + 8
		dataEnd := dataStart + int(length)
		if dataEnd+4 > len(png) {
			return nil, fmt.Errorf("%w: chunk %q overruns file", ErrBadPNG, typ)
		}
		data := png[dataStart:dataEnd]
		wantCRC := binary.BigEndian.Uint32(png[dataEnd : dataEnd+4])
		if got := crcChunk(typ, data); got != wantCRC {
			return nil, fmt.Errorf("%w: chunk %q CRC mismatch", ErrBadPNG, typ)
		}
		// Copy so slices stay valid independent of the input buffer.
		cp := make([]byte, len(data))
		copy(cp, data)
		chunks = append(chunks, chunk{typ: typ, data: cp})

		pos = dataEnd + 4
		if typ == "IEND" {
			break
		}
	}
	return chunks, nil
}

// Reassembles a PNG from an ordered chunk list, recomputing lengths and CRCs.
func writeChunks(chunks []chunk) []byte {
	size := len(pngSignature)
	for _, ch := range chunks {
		size += 12 + len(ch.data) // length(4)+type(4)+data+crc(4)
	}
	out := make([]byte, 0, size)
	out = append(out, pngSignature...)
	var lenBuf [4]byte
	var crcBuf [4]byte
	for _, ch := range chunks {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ch.data)))
		out = append(out, lenBuf[:]...)
		out = append(out, ch.typ...)
		out = append(out, ch.data...)
		binary.BigEndian.PutUint32(crcBuf[:], crcChunk(ch.typ, ch.data))
		out = append(out, crcBuf[:]...)
	}
	return out
}

// PNG CRC32 (IEEE) over type||data.
func crcChunk(typ string, data []byte) uint32 {
	h := crc32.NewIEEE()
	h.Write([]byte(typ))
	h.Write(data)
	return h.Sum32()
}
