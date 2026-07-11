// crypto defines the seam between raw block bytes and the carrier payload
package crypto

type Cipher interface {
	Seal(plaintext []byte) (payload []byte, err error)
	Open(payload []byte) (plaintext []byte, err error)
	Encrypted() bool
}

type NopCipher struct{}

func (NopCipher) Seal(plaintext []byte) ([]byte, error) { return plaintext, nil }

func (NopCipher) Open(payload []byte) ([]byte, error) { return payload, nil }

func (NopCipher) Encrypted() bool { return false }
