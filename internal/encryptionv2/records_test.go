package encryptionv2

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

func TestFileHeaderFixture(t *testing.T) {
	t.Parallel()
	header := FileHeader{FrameSize: DefaultFrameSize, PlaintextSize: 1}
	for index := range header.ContentID {
		header.ContentID[index] = byte(index)
		header.NoncePrefix[index] = byte(index + 16)
	}
	encoded, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	want := "53544f434154303202010000008000000000000000000001000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f0000000000000000"
	if hex.EncodeToString(encoded) != want {
		t.Fatalf("header = %x", encoded)
	}
	parsed, err := ParseFileHeader(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != header {
		t.Fatalf("parsed = %#v", parsed)
	}
	if size, err := parsed.CiphertextSize(); err != nil || size != 81 {
		t.Fatalf("ciphertext size = %d, %v", size, err)
	}
}

func TestTupleFixture(t *testing.T) {
	t.Parallel()
	var tuple Tuple
	if err := tuple.String("ab"); err != nil {
		t.Fatal(err)
	}
	if err := tuple.Counter(3); err != nil {
		t.Fatal(err)
	}
	if err := tuple.Flag(true); err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("000000026162000000000000000301")
	if !bytes.Equal(tuple.Bytes(), want) {
		t.Fatalf("tuple = %x", tuple.Bytes())
	}
}

func TestPasswordValidationPreservesInput(t *testing.T) {
	t.Parallel()
	if err := ValidatePassword("  fifteen chars "); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePassword("too short"); err == nil {
		t.Fatal("short password passed validation")
	}
}

func TestRejectsReservedHeaderBytes(t *testing.T) {
	t.Parallel()
	header, err := (FileHeader{FrameSize: MinFrameSize}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	header[10] = 1
	if _, err := ParseFileHeader(header); err == nil {
		t.Fatal("header with reserved bytes passed validation")
	}
}

func TestFileHeaderSizeBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		size  uint64
		frame uint32
		want  uint64
		valid bool
	}{
		{"empty", 0, MinFrameSize, 80, true},
		{"boundary", MinFrameSize, MinFrameSize, HeaderSize + MinFrameSize + TagSize, true},
		{"second frame", MinFrameSize + 1, MinFrameSize, HeaderSize + MinFrameSize + 1 + 2*TagSize, true},
		{"zero frame", 1, 0, 0, false},
		{"unregistered frame", 1, MinFrameSize + 1, 0, false},
		{"unsafe plaintext", MaxSafeInteger + 1, DefaultFrameSize, 0, false},
		{"unsafe ciphertext", MaxSafeInteger, DefaultFrameSize, 0, false},
		{"addition overflow", math.MaxUint64 - 63, DefaultFrameSize, 0, false},
		{"maximum uint64", math.MaxUint64, DefaultFrameSize, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			h := FileHeader{FrameSize: test.frame, PlaintextSize: test.size}
			size, err := h.CiphertextSize()
			if (err == nil) != test.valid || size != test.want {
				t.Fatalf("size=%d err=%v", size, err)
			}
			if _, err = h.MarshalBinary(); (err == nil) != test.valid {
				t.Fatalf("marshal error=%v", err)
			}
			raw := make([]byte, HeaderSize)
			copy(raw, fileMagic[:])
			raw[8] = 2
			raw[9] = Suite
			binary.BigEndian.PutUint32(raw[12:16], test.frame)
			binary.BigEndian.PutUint64(raw[16:24], test.size)
			if _, err = ParseFileHeader(raw); (err == nil) != test.valid {
				t.Fatalf("parse error=%v", err)
			}
		})
	}
}
