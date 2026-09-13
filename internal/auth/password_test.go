package auth

import (
	"strings"
	"testing"
)

func TestPassword(t *testing.T) {
	password := "a long test password 🐈"
	first := hashPassword(password)
	second := hashPassword(password)
	if first == second || !strings.HasPrefix(first, passwordPrefix) {
		t.Fatal("password hashes must use Argon2id and distinct salts")
	}
	for _, tc := range []struct {
		name, password, hash string
		want                 bool
	}{
		{"match", password, first, true},
		{"second salt", password, second, true},
		{"wrong password", "another password", first, false},
		{"empty hash", password, "", false},
		{"wrong algorithm", password, strings.Replace(first, "argon2id", "argon2i", 1), false},
		{"wrong version", password, strings.Replace(first, "v=19", "v=16", 1), false},
		{"excessive memory", password, strings.Replace(first, "m=19456", "m=4294967295", 1), false},
		{"missing key", password, passwordPrefix + "YWJj", false},
		{"invalid salt", password, passwordPrefix + "!$YWJj", false},
		{"short salt", password, passwordPrefix + "YWJj$YWJj", false},
		{"short key", password, first[:len(first)-4], false},
		{"extra field", password, first + "$extra", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyPassword(tc.password, tc.hash); got != tc.want {
				t.Fatalf("verify = %v, want %v", got, tc.want)
			}
		})
	}
}
