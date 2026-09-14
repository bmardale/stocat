// Package id creates and validates prefixed ULID identifiers.
//
// An identifier has a prefix, an underscore and a ULID. The ULID uses the
// Crockford base32 alphabet and is 26 characters long. Its first 48 bits
// hold the creation time in milliseconds, so identifiers sort by age.
// Example: "usr_01K4W9T5V8QK3M7ZB0YHXC2FNE".
package id

import (
	"crypto/rand"
	"strings"

	"github.com/oklog/ulid/v2"
)

// User is the prefix of user identifiers.
const User = "usr"

// StorageBackend is the prefix of storage backend identifiers.
const StorageBackend = "stb"

// Session is the prefix of session identifiers.
const Session = "ses"

const (
	// Library is the prefix of library identifiers.
	Library = "lib"
	// Node is the prefix of file and folder identifiers.
	Node = "nod"
	// Blob is the prefix of blob identifiers.
	Blob = "blb"
	// BlobLocation is the prefix of blob location identifiers.
	BlobLocation = "loc"
	// FileVersion is the prefix of file version identifiers.
	FileVersion = "ver"
	// Upload is the prefix of upload identifiers.
	Upload = "upl"
	// Request is the prefix of request identifiers.
	Request = "req"
	// Passkey is the prefix of passkey identifiers.
	Passkey = "pky"
	// Replication is the prefix of replication identifiers.
	Replication = "rep"
)

const separator = "_"

// entropy gives random bytes to the ULID constructor. It reads from
// crypto/rand, because a public identifier must not be predictable.
type entropy struct{}

func (entropy) Read(p []byte) (int, error) { return rand.Read(p) }

// New returns a new identifier with the prefix.
func New(prefix string) string {
	// MustNew cannot panic here. crypto/rand.Read never returns an error.
	return prefix + separator + ulid.MustNew(ulid.Now(), entropy{}).String()
}

// Valid reports if s is an identifier with the prefix. It accepts only the
// canonical uppercase form.
func Valid(prefix, s string) bool {
	value, found := strings.CutPrefix(s, prefix+separator)
	if !found || value != strings.ToUpper(value) {
		return false
	}
	_, err := ulid.ParseStrict(value)
	return err == nil
}
