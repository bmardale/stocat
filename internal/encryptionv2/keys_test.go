package encryptionv2

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"testing"
)

func TestValidateSigningPublicKey(t *testing.T) {
	valid, _ := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	identity := make([]byte, 32)
	identity[0] = 1
	nonCanonicalIdentity := bytes.Repeat([]byte{0xff}, 32)
	nonCanonicalIdentity[0], nonCanonicalIdentity[31] = 0xee, 0x7f
	for _, tc := range []struct {
		name  string
		key   []byte
		valid bool
	}{
		{"RFC 8032 key", valid, true},
		{"short key", valid[:31], false},
		{"identity point", identity, false},
		{"non-canonical identity point", nonCanonicalIdentity, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSigningPublicKey(tc.key); (err == nil) != tc.valid {
				t.Fatalf("ValidateSigningPublicKey() error = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestValidateRecipientPublicKey(t *testing.T) {
	valid, _ := hex.DecodeString("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	one := make([]byte, 32)
	one[0] = 1
	highBit := bytes.Clone(valid)
	highBit[31] |= 0x80
	prime := bytes.Repeat([]byte{0xff}, 32)
	prime[0], prime[31] = 0xed, 0x7f
	for _, tc := range []struct {
		name  string
		key   []byte
		valid bool
	}{
		{"RFC 7748 key", valid, true},
		{"short key", valid[:31], false},
		{"zero", make([]byte, 32), false},
		{"one", one, false},
		{"high bit", highBit, false},
		{"field prime", prime, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRecipientPublicKey(tc.key); (err == nil) != tc.valid {
				t.Fatalf("ValidateRecipientPublicKey() error = %v, want valid %v", err, tc.valid)
			}
		})
	}
}

func TestRecipientKeyIDFixture(t *testing.T) {
	for _, fixture := range readFixtures(t) {
		if fixture.Record.Type != "identity" {
			continue
		}
		fields := fixture.Record.Fields
		deployment, err := DecodeBase64URL(fixture.Record.DeploymentID, 16)
		if err != nil {
			t.Fatal(err)
		}
		recipientKey, err := DecodeBase64URL(fields["recipient_public_key"], 32)
		if err != nil {
			t.Fatal(err)
		}
		generation, err := strconv.ParseUint(fields["generation"], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RecipientKeyID(deployment, fields["account_id"], generation, recipientKey)
		if err != nil {
			t.Fatal(err)
		}
		want, err := DecodeBase64URL(fields["recipient_key_id"], 32)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("RecipientKeyID() = %x, want %x", got, want)
		}
		return
	}
	t.Fatal("fixtures contain no identity record")
}
