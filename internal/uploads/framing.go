package uploads

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

const (
	frameHeaderSize = 32
	frameTagSize    = 16
	minFrameSize    = 64 * 1024
	maxFrameSize    = 64 * 1024 * 1024
)

var frameMagic = [8]byte{'S', 'T', 'O', 'C', 'A', 'T', '0', '1'}

func validateFraming(reader io.ReaderAt, ciphertextSize int64) (int64, error) {
	var header [frameHeaderSize]byte
	if _, err := reader.ReadAt(header[:], 0); err != nil {
		return 0, fmt.Errorf("read encryption header: %w", err)
	}
	if !bytes.Equal(header[:len(frameMagic)], frameMagic[:]) || header[8] != 1 ||
		header[9] != 0 || header[10] != 0 || header[11] != 0 {
		return 0, fmt.Errorf("invalid encryption header")
	}
	frameSize := int64(binary.BigEndian.Uint32(header[12:16]))
	if frameSize < minFrameSize || frameSize > maxFrameSize {
		return 0, fmt.Errorf("invalid encryption frame size")
	}
	plaintextSizeValue := binary.BigEndian.Uint64(header[16:24])
	if plaintextSizeValue > math.MaxInt64 {
		return 0, fmt.Errorf("invalid plaintext size")
	}
	plaintextSize := int64(plaintextSizeValue)
	frameCount := int64(1)
	if plaintextSize > 0 {
		frameCount = (plaintextSize-1)/frameSize + 1
	}
	if plaintextSize > math.MaxInt64-frameHeaderSize-frameCount*frameTagSize {
		return 0, fmt.Errorf("encrypted file size overflows")
	}
	expected := int64(frameHeaderSize) + plaintextSize + frameCount*frameTagSize
	if ciphertextSize != expected {
		return 0, fmt.Errorf("encrypted file has %d bytes, want %d", ciphertextSize, expected)
	}
	return plaintextSize, nil
}
