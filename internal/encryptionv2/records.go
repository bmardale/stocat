package encryptionv2

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

const (
	Suite                   = 1
	HeaderSize              = 64
	TagSize                 = 16
	DefaultFrameSize        = 8 << 20
	MinFrameSize            = 64 << 10
	MaxFrameSize            = 8 << 20
	MaxMetadataCiphertext   = 64 << 10
	MaxKeyRecord            = 16 << 10
	Argon2MemoryKiB         = 65536
	Argon2Passes            = 3
	Argon2Lanes             = 4
	Argon2OutputBytes       = 32
	PasswordMinimumRunes    = 15
	PasswordMaximumUTF8Size = 1024
	MaxSafeInteger          = 1<<53 - 1
)

var (
	fileMagic        = [8]byte{'S', 'T', 'O', 'C', 'A', 'T', '0', '2'}
	ErrInvalidHeader = errors.New("invalid stocat-framed-v2 header")
)

type Tuple struct {
	bytes.Buffer
}

func (t *Tuple) BytesField(value []byte) error {
	if uint64(len(value)) > math.MaxUint32 {
		return errors.New("tuple field is too large")
	}
	if err := binary.Write(&t.Buffer, binary.BigEndian, uint32(len(value))); err != nil {
		return fmt.Errorf("write tuple field size: %w", err)
	}
	_, err := t.Write(value)
	return err
}

func (t *Tuple) String(value string) error {
	return t.BytesField([]byte(value))
}

func (t *Tuple) Counter(value uint64) error {
	return binary.Write(&t.Buffer, binary.BigEndian, value)
}

func (t *Tuple) Flag(value bool) error {
	if value {
		return t.WriteByte(1)
	}
	return t.WriteByte(0)
}

func DecodeBase64URL(value string, size int) ([]byte, error) {
	if bytes.ContainsRune([]byte(value), '=') {
		return nil, errors.New("base64url padding is not canonical")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode base64url: %w", err)
	}
	if len(decoded) != size {
		return nil, fmt.Errorf("decoded value has length %d, want %d", len(decoded), size)
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, errors.New("base64url value is not canonical")
	}
	return decoded, nil
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return errors.New("password is not valid UTF-8")
	}
	if utf8.RuneCountInString(password) < PasswordMinimumRunes {
		return fmt.Errorf("password must contain at least %d characters", PasswordMinimumRunes)
	}
	if len(password) > PasswordMaximumUTF8Size {
		return fmt.Errorf("password must not exceed %d UTF-8 bytes", PasswordMaximumUTF8Size)
	}
	return nil
}

type FileHeader struct {
	FrameSize     uint32
	PlaintextSize uint64
	ContentID     [16]byte
	NoncePrefix   [16]byte
}

func (h FileHeader) MarshalBinary() ([]byte, error) {
	if _, err := h.CiphertextSize(); err != nil {
		return nil, err
	}
	result := make([]byte, HeaderSize)
	copy(result, fileMagic[:])
	result[8] = 2
	result[9] = Suite
	binary.BigEndian.PutUint32(result[12:16], h.FrameSize)
	binary.BigEndian.PutUint64(result[16:24], h.PlaintextSize)
	copy(result[24:40], h.ContentID[:])
	copy(result[40:56], h.NoncePrefix[:])
	return result, nil
}

func ParseFileHeader(value []byte) (FileHeader, error) {
	var header FileHeader
	if len(value) != HeaderSize || !bytes.Equal(value[:8], fileMagic[:]) || value[8] != 2 || value[9] != Suite {
		return header, ErrInvalidHeader
	}
	if !allZero(value[10:12]) || !allZero(value[56:64]) {
		return header, ErrInvalidHeader
	}
	header.FrameSize = binary.BigEndian.Uint32(value[12:16])
	header.PlaintextSize = binary.BigEndian.Uint64(value[16:24])
	copy(header.ContentID[:], value[24:40])
	copy(header.NoncePrefix[:], value[40:56])
	if err := ValidateFrameSize(header.FrameSize); err != nil {
		return FileHeader{}, errors.Join(ErrInvalidHeader, err)
	}
	if _, err := header.CiphertextSize(); err != nil {
		return FileHeader{}, errors.Join(ErrInvalidHeader, err)
	}
	return header, nil
}

func ValidateFrameSize(size uint32) error {
	if size < MinFrameSize || size > MaxFrameSize || size&(size-1) != 0 {
		return errors.New("frame size is not a registered power of two")
	}
	return nil
}

func (h FileHeader) FrameCount() (uint64, error) {
	if err := ValidateFrameSize(h.FrameSize); err != nil {
		return 0, err
	}
	if h.PlaintextSize > MaxSafeInteger {
		return 0, errors.New("plaintext size exceeds the JavaScript safe integer range")
	}
	if h.PlaintextSize == 0 {
		return 1, nil
	}
	return 1 + (h.PlaintextSize-1)/uint64(h.FrameSize), nil
}

func (h FileHeader) CiphertextSize() (uint64, error) {
	frames, err := h.FrameCount()
	if err != nil {
		return 0, err
	}
	if h.PlaintextSize > MaxSafeInteger-HeaderSize || frames > (MaxSafeInteger-HeaderSize-h.PlaintextSize)/TagSize {
		return 0, errors.New("ciphertext size exceeds the JavaScript safe integer range")
	}
	return uint64(HeaderSize) + h.PlaintextSize + TagSize*frames, nil
}

func (h FileHeader) Nonce(frame uint64) [24]byte {
	var nonce [24]byte
	copy(nonce[:16], h.NoncePrefix[:])
	binary.BigEndian.PutUint64(nonce[16:], frame)
	return nonce
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
