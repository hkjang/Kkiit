package cryptox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
)

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize GCM: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Encrypt(plain []byte, purpose string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(purpose)), nil
}

func (b *Box) Decrypt(ciphertext []byte, purpose string) ([]byte, error) {
	if len(ciphertext) < b.aead.NonceSize() {
		return nil, fmt.Errorf("ciphertext is too short")
	}
	nonce, data := ciphertext[:b.aead.NonceSize()], ciphertext[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, data, []byte(purpose))
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", purpose, err)
	}
	return plain, nil
}

func RandomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func Digest(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func DigestMatches(digest []byte, value string) bool {
	actual := Digest(value)
	return len(digest) == len(actual) && subtle.ConstantTimeCompare(digest, actual) == 1
}
