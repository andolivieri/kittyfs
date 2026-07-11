// carrier defines the pluggable seam between opaque block payloads
// and standalone media files that carry them
package carrier

import "errors"

var ErrNotACarrier = errors.New("carrier: file carries no kittyfs data")
var ErrNotImplemented = errors.New("carrier: not implemented")

// Carrier embeds/extracts opaque block payloads into/from standalone media
// files. Implementations must be deterministic on Encode inputs except
// where a cover source intentionally varies (see the PNG carrier). Encode
// output MUST be a valid, openable file of the carrier's media type.
type Carrier interface {
	// Ext is the on-disk file extension, including dot, e.g. ".png".
	Ext() string

	// MaxPayload is the max number of payload bytes one carrier file can
	// hold. Returns 0 for "effectively unbounded".
	MaxPayload() int

	// Encode wraps payload into the bytes of a complete media file.
	// blockID is stored inside so the file is self-describing. encrypted
	// marks the payload as ciphertext so the container records it.
	Encode(blockID uint64, payload []byte, encrypted bool) ([]byte, error)

	// Decode extracts (blockID, payload) from a complete media file's
	// bytes. Returns ErrNotACarrier if the file carries no kittyFS data.
	Decode(mediaFile []byte) (blockID uint64, payload []byte, err error)
}

// CoverSource yields cover-media bytes to dress up a block. The choice of
// cover is cosmetic — the blockID is stored inside the carrier container, not
// implied by the cover — so implementations are free to pick at random.
// EmbeddedCats does exactly that and ignores blockID.
type CoverSource interface {
	Cover(blockID uint64) (mediaBytes []byte, err error)
	Count() int
}
