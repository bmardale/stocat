package uploads

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestValidateFraming(t *testing.T) {
	header := make([]byte, frameHeaderSize)
	copy(header, frameMagic[:])
	header[8] = 1
	binary.BigEndian.PutUint32(header[12:16], minFrameSize)
	binary.BigEndian.PutUint64(header[16:24], 70_000)
	data := append(header, make([]byte, 70_000+2*frameTagSize)...)
	size, err := validateFraming(bytes.NewReader(data), int64(len(data)))
	if err != nil || size != 70_000 {
		t.Fatalf("validate framing = %d, %v", size, err)
	}
	data[0] = 'X'
	if _, err := validateFraming(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("invalid magic passed validation")
	}
}
