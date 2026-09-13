package crypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func parseKey(t *testing.T, text string) Key {
	t.Helper()
	var key Key
	if err := key.UnmarshalText([]byte(text)); err != nil {
		t.Fatal(err)
	}
	return key
}

func newEncrypter(t *testing.T, key Key, previous ...Key) *Encrypter {
	t.Helper()
	e, err := New(key, previous...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestRoundTrip(t *testing.T) {
	e := newEncrypter(t, parseKey(t, GenerateKey()))
	for _, plaintext := range [][]byte{nil, []byte("secret"), bytes.Repeat([]byte{0xff}, 4096)} {
		payload := e.Encrypt(plaintext)
		if !strings.HasPrefix(payload, payloadPrefix) {
			t.Fatalf("payload %q has no version prefix", payload)
		}
		got, err := e.Decrypt(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
		}
	}
}

func TestEncryptUsesRandomNonce(t *testing.T) {
	e := newEncrypter(t, parseKey(t, GenerateKey()))
	first, second := e.Encrypt([]byte("secret")), e.Encrypt([]byte("secret"))
	if first == second {
		t.Fatal("equal plaintexts gave equal payloads")
	}
}

func TestDecryptRejectsInvalidPayloads(t *testing.T) {
	e := newEncrypter(t, parseKey(t, GenerateKey()))
	payload := e.Encrypt([]byte("secret"))
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(payload, payloadPrefix))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	tampered := payloadPrefix + base64.StdEncoding.EncodeToString(sealed)

	for _, tc := range []struct{ name, payload string }{
		{"empty", ""},
		{"unknown version", "v2:" + strings.TrimPrefix(payload, payloadPrefix)},
		{"invalid base64", payloadPrefix + "!!!"},
		{"too short", payloadPrefix + base64.StdEncoding.EncodeToString([]byte("short"))},
		{"tampered", tampered},
		{"other key", newEncrypter(t, parseKey(t, GenerateKey())).Encrypt([]byte("secret"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := e.Decrypt(tc.payload); !errors.Is(err, ErrDecrypt) {
				t.Fatalf("Decrypt() error = %v, want ErrDecrypt", err)
			}
		})
	}
}

func TestDecryptWithPreviousKey(t *testing.T) {
	oldKey, newKey := parseKey(t, GenerateKey()), parseKey(t, GenerateKey())
	payload := newEncrypter(t, oldKey).Encrypt([]byte("secret"))
	got, err := newEncrypter(t, newKey, oldKey).Decrypt(payload)
	if err != nil || string(got) != "secret" {
		t.Fatalf("Decrypt() = %q, %v, want secret", got, err)
	}
	if _, err := newEncrypter(t, newKey).Decrypt(payload); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("Decrypt() without the previous key error = %v, want ErrDecrypt", err)
	}
}

func TestKeyUnmarshalText(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, KeySize))
	for _, tc := range []struct {
		name, text string
		ok         bool
	}{
		{"valid", keyPrefix + valid, true},
		{"missing prefix", valid, false},
		{"invalid base64", keyPrefix + "!!!", false},
		{"short", keyPrefix + base64.StdEncoding.EncodeToString(make([]byte, 16)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var key Key
			err := key.UnmarshalText([]byte(tc.text))
			if (err == nil) != tc.ok {
				t.Fatalf("UnmarshalText() error = %v, want ok = %v", err, tc.ok)
			}
		})
	}
}

func TestNewRejectsInvalidKeys(t *testing.T) {
	if _, err := New(make(Key, 16)); err == nil {
		t.Fatal("New() accepted a short key")
	}
	if _, err := New(make(Key, KeySize), make(Key, 8)); err == nil {
		t.Fatal("New() accepted a short previous key")
	}
}

func TestKeyRedaction(t *testing.T) {
	key := parseKey(t, GenerateKey())
	var logs bytes.Buffer
	slog.New(slog.NewTextHandler(&logs, nil)).Info("key", "key", key)
	for _, output := range []string{fmt.Sprint(key), fmt.Sprintf("%v", key), logs.String()} {
		if !strings.Contains(output, "[REDACTED]") {
			t.Fatalf("output %q exposes the key", output)
		}
	}
}
