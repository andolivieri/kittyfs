package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const KeyLen = 32 // AES-256
const nonceLen = 12

var verifierPlaintext = []byte("kittyFS-ok")

var ErrWrongPassword = errors.New("crypto: wrong password / authentication failed")

// Argon2id knobs persisted in the plaintext superblock
type Argon2Params struct {
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"` // in KiB
	Threads uint8  `json:"threads"`
}

func DefaultArgon2Params() Argon2Params {
	return Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4}
}

func DeriveKey(password, salt []byte, p Argon2Params) [KeyLen]byte {
	k := argon2.IDKey(password, salt, p.Time, p.Memory, p.Threads, KeyLen)
	var key [KeyLen]byte
	copy(key[:], k)
	// Wipe the heap copy argon2 returned; the array copy is the one we keep.
	for i := range k {
		k[i] = 0
	}
	return key
}

// Cipher with AES-256-GCM + Argon2id-derived key.
// A fresh random nonce per Seal; blocks are re-encrypted whole, so nonces are
// never reused under one key.
type AEADCipher struct {
	key  [KeyLen]byte
	aead cipher.AEAD
}

var _ Cipher = (*AEADCipher)(nil)

func NewAEADCipher(key [KeyLen]byte) (*AEADCipher, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return &AEADCipher{key: key, aead: gcm}, nil
}

// nonce || ciphertext || tag.
func (c *AEADCipher) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	// Seal appends ciphertext+tag onto nonce, giving nonce||ct||tag in one buf.
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (c *AEADCipher) Open(payload []byte) ([]byte, error) {
	if len(payload) < nonceLen+c.aead.Overhead() {
		return nil, fmt.Errorf("crypto: payload too short (%d bytes)", len(payload))
	}
	nonce := payload[:nonceLen]
	ct := payload[nonceLen:]
	pt, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrWrongPassword
	}
	return pt, nil
}

func (c *AEADCipher) Encrypted() bool { return true }

func (c *AEADCipher) MakeVerifier() ([]byte, error) {
	return c.Seal(verifierPlaintext)
}

func (c *AEADCipher) CheckVerifier(verifier []byte) error {
	pt, err := c.Open(verifier)
	if err != nil {
		return err
	}
	if string(pt) != string(verifierPlaintext) {
		return ErrWrongPassword
	}
	return nil
}

// best-effort wipe of the in-memory key.
func (c *AEADCipher) Zero() {
	for i := range c.key {
		c.key[i] = 0
	}
}
