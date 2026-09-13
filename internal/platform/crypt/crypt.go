// Package crypt encrypts and authenticates values with AES-256-GCM.
//
// A payload has the form "v1:" followed by base64 of the nonce and the sealed
// data. Decryption tries the current key first and the previous keys next.
// Previous keys let you rotate APP_KEY without data loss.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const (
	KeySize = 32

	keyPrefix     = "base64:"
	payloadPrefix = "v1:"
)

var ErrDecrypt = errors.New("decrypt payload")

// Key is an AES-256 key. Its text form is "base64:" followed by standard base64.
type Key []byte

// GenerateKey returns a random key in text form.
func GenerateKey() string {
	key := make([]byte, KeySize)
	_, _ = rand.Read(key)
	return keyPrefix + base64.StdEncoding.EncodeToString(key)
}

func (k *Key) UnmarshalText(text []byte) error {
	encoded, found := strings.CutPrefix(string(text), keyPrefix)
	if !found {
		return fmt.Errorf("key must start with %q", keyPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("key is not valid base64")
	}
	if len(raw) != KeySize {
		return fmt.Errorf("key must contain %d bytes, got %d", KeySize, len(raw))
	}
	*k = raw
	return nil
}

func (Key) String() string { return "[REDACTED]" }

func (Key) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

type Encrypter struct {
	current  cipher.AEAD
	previous []cipher.AEAD
}

func New(key Key, previous ...Key) (*Encrypter, error) {
	current, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	e := &Encrypter{current: current}
	for i, key := range previous {
		aead, err := newAEAD(key)
		if err != nil {
			return nil, fmt.Errorf("previous key %d: %w", i, err)
		}
		e.previous = append(e.previous, aead)
	}
	return e, nil
}

func newAEAD(key Key) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must contain %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return aead, nil
}

// Encrypt uses a new random nonce for each call, so equal values give different payloads.
func (e *Encrypter) Encrypt(plaintext []byte) string {
	nonce := make([]byte, e.current.NonceSize(), e.current.NonceSize()+len(plaintext)+e.current.Overhead())
	_, _ = rand.Read(nonce)
	sealed := e.current.Seal(nonce, nonce, plaintext, nil)
	return payloadPrefix + base64.StdEncoding.EncodeToString(sealed)
}

// Decrypt returns ErrDecrypt when no key authenticates the payload.
func (e *Encrypter) Decrypt(payload string) ([]byte, error) {
	encoded, found := strings.CutPrefix(payload, payloadPrefix)
	if !found {
		return nil, fmt.Errorf("%w: unknown format", ErrDecrypt)
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64", ErrDecrypt)
	}
	for _, aead := range append([]cipher.AEAD{e.current}, e.previous...) {
		if len(sealed) < aead.NonceSize()+aead.Overhead() {
			return nil, fmt.Errorf("%w: payload is too short", ErrDecrypt)
		}
		nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
		if plaintext, err := aead.Open(nil, nonce, ciphertext, nil); err == nil {
			return plaintext, nil
		}
	}
	return nil, fmt.Errorf("%w: authentication failed", ErrDecrypt)
}
