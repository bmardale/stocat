package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const passwordPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

func validNewPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 15 && len(password) <= 1024
}

// NewPasswordHash returns a hash when the password meets the password policy.
func NewPasswordHash(password string) (string, bool) {
	if !validNewPassword(password) {
		return "", false
	}
	return hashPassword(password), true
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := passwordKey(password, salt)
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}

func passwordKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
}

func verifyPassword(password, encoded string) bool {
	value, ok := strings.CutPrefix(encoded, passwordPrefix)
	if !ok {
		return false
	}
	saltText, keyText, ok := strings.Cut(value, "$")
	if !ok {
		return false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(saltText)
	if err != nil || len(salt) != 16 {
		return false
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(keyText)
	if err != nil || len(key) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(passwordKey(password, salt), key) == 1
}
