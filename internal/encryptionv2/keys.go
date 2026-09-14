package encryptionv2

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"errors"

	"filippo.io/edwards25519"
)

// A clamped scalar is a multiple of the cofactor, so every low-order point gives an all-zero shared secret.
var lowOrderProbe = [32]byte{9}

func ValidateSigningPublicKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("signing public key must contain 32 bytes")
	}
	point, err := new(edwards25519.Point).SetBytes(key)
	if err != nil || !bytes.Equal(point.Bytes(), key) {
		return errors.New("signing public key is not a canonical Ed25519 point")
	}
	if new(edwards25519.Point).MultByCofactor(point).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return errors.New("signing public key has small order")
	}
	return nil
}

func ValidateRecipientPublicKey(key []byte) error {
	if len(key) != 32 || key[31]&0x80 != 0 || x25519AtLeastPrime(key) {
		return errors.New("recipient public key is not a canonical X25519 value")
	}
	public, err := ecdh.X25519().NewPublicKey(key)
	if err != nil {
		return errors.New("recipient public key is not a canonical X25519 value")
	}
	probe, err := ecdh.X25519().NewPrivateKey(lowOrderProbe[:])
	if err != nil {
		return err
	}
	if _, err = probe.ECDH(public); err != nil {
		return errors.New("recipient public key has low order")
	}
	return nil
}

// x25519AtLeastPrime reports if the little-endian value is at least 2^255 - 19.
func x25519AtLeastPrime(key []byte) bool {
	if key[31] != 0x7f || key[0] < 0xed {
		return false
	}
	for _, value := range key[1:31] {
		if value != 0xff {
			return false
		}
	}
	return true
}

// RecipientKeyID hashes C("stocat/v2/recipient-key-id", deployment, account_id, generation, recipient_public_key).
func RecipientKeyID(deployment []byte, accountID string, generation uint64, recipientPublicKey []byte) ([]byte, error) {
	var tuple Tuple
	err := errors.Join(
		tuple.String("stocat/v2/recipient-key-id"),
		tuple.BytesField(deployment),
		tuple.String(accountID),
		tuple.Counter(generation),
		tuple.BytesField(recipientPublicKey),
	)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(tuple.Bytes())
	return hash[:], nil
}
