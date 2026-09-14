package encryptionv2

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"
)

type recordFixture struct {
	SigningPublicKeyHex string  `json:"signing_public_key_hex"`
	SignatureHex        string  `json:"signature_hex"`
	Record              Record  `json:"record"`
	Hex                 string  `json:"hex"`
	SignatureInputHex   string  `json:"signature_input_hex"`
	AADHex              *string `json:"aad_hex"`
}

func readFixtures(t *testing.T) []recordFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/records.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []recordFixture
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func TestRecordFixtures(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, fixture := range readFixtures(t) {
		seen[fixture.Record.Type] = true
		t.Run(fixture.Record.Type, func(t *testing.T) {
			t.Parallel()
			encoded, err := fixture.Record.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(encoded) != fixture.Hex {
				t.Fatalf("encoding=%x", encoded)
			}
			key, err := hex.DecodeString(fixture.SigningPublicKeyHex)
			if err != nil {
				t.Fatal(err)
			}
			signature, err := hex.DecodeString(fixture.SignatureHex)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := VerifyRecord(encoded, key, signature)
			if err != nil || !reflect.DeepEqual(decoded, fixture.Record) {
				t.Fatalf("record=%v err=%v", decoded, err)
			}
			input, err := SignatureInput(encoded)
			if err != nil || hex.EncodeToString(input) != fixture.SignatureInputHex {
				t.Fatalf("signature input=%x err=%v", input, err)
			}
			if fixture.AADHex != nil {
				aad, err := fixture.Record.AssociatedData()
				if err != nil || hex.EncodeToString(aad) != *fixture.AADHex {
					t.Fatalf("AAD=%x err=%v", aad, err)
				}
			}
			for size := 0; size < len(encoded); size++ {
				if _, err := ParseRecord(encoded[:size]); err == nil {
					t.Fatalf("truncation accepted at %d", size)
				}
			}
			if _, err := ParseRecord(append(encoded, 0)); err == nil {
				t.Fatal("trailing byte accepted")
			}
		})
	}
	if len(seen) != len(recordSchemas) {
		t.Fatal("a record schema has no fixture")
	}
}

func TestInvalidRecordFields(t *testing.T) {
	t.Parallel()
	fixture := readFixtures(t)[0].Record
	for _, test := range []struct{ name, field, value string }{
		{"zero generation", "generation", "0"},
		{"leading zero", "generation", "01"},
		{"negative counter", "generation", "-1"},
		{"counter overflow", "generation", "9223372036854775808"},
		{"short key", "signing_public_key", "AA"},
		{"padded key", "signing_public_key", fixture.Fields["signing_public_key"] + "="},
		{"unknown field", "extra", "value"},
		{"wrong account type", "account_id", "nod_00000000000000000000000001"},
		{"oversized field", "signing_public_key", strings.Repeat("A", MaxWireRecord)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := fixture
			record.Fields = maps.Clone(record.Fields)
			record.Fields[test.field] = test.value
			if _, err := record.MarshalBinary(); err == nil {
				t.Fatal("invalid field accepted")
			}
		})
	}
}

func TestRecordContextBinding(t *testing.T) {
	t.Parallel()
	for _, fixture := range readFixtures(t) {
		t.Run(fixture.Record.Type, func(t *testing.T) {
			t.Parallel()
			original, _ := hex.DecodeString(fixture.SignatureInputHex)
			record := fixture.Record
			record.DeploymentID = "_____________________w"
			encoded, err := record.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			input, err := SignatureInput(encoded)
			if err != nil || bytes.Equal(input, original) {
				t.Fatalf("deployment did not change signature input: %v", err)
			}
		})
	}
}

func TestRejectUnknownSuite(t *testing.T) {
	t.Parallel()
	raw, err := readFixtures(t)[0].Record.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	raw[4+len("stocat/v2/identity")+4+16] = 2
	if _, err = ParseRecord(raw); err == nil {
		t.Fatal("unknown suite accepted")
	}
}

func TestFrameAdditionalDataFixture(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/frame.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		HeaderHex string `json:"header_hex"`
		AADHex    string `json:"aad_hex"`
		NonceHex  string `json:"nonce_hex"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(fixture.HeaderHex)
	if err != nil {
		t.Fatal(err)
	}
	header, err := ParseFileHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	aad, err := header.FrameAdditionalData(0)
	if err != nil || hex.EncodeToString(aad) != fixture.AADHex {
		t.Fatalf("AAD=%x err=%v", aad, err)
	}
	nonce := header.Nonce(0)
	if hex.EncodeToString(nonce[:]) != fixture.NonceHex {
		t.Fatalf("nonce=%x", nonce)
	}
	if _, err = header.FrameAdditionalData(1); err == nil {
		t.Fatal("invalid frame index accepted")
	}
}

func TestSignedRecordSubstitution(t *testing.T) {
	t.Parallel()
	for _, fixture := range readFixtures(t) {
		t.Run(fixture.Record.Type, func(t *testing.T) {
			t.Parallel()
			key, err := hex.DecodeString(fixture.SigningPublicKeyHex)
			if err != nil {
				t.Fatal(err)
			}
			signature, err := hex.DecodeString(fixture.SignatureHex)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range recordSchemas[fixture.Record.Type] {
				if field.kind != "counter" && field.kind != "usr" && field.kind != "lib" && field.kind != "nod" && field.kind != "ver" {
					continue
				}
				record := fixture.Record
				record.Fields = maps.Clone(record.Fields)
				value, ok := record.Fields[field.name]
				if !ok {
					continue
				}
				if field.kind == "counter" {
					record.Fields[field.name] = "9"
				} else {
					record.Fields[field.name] = value[:len(value)-1] + "9"
				}
				encoded, err := record.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = VerifyRecord(encoded, key, signature); err == nil {
					t.Fatalf("substitution accepted: %s", field.name)
				}
			}
			encoded, err := fixture.Record.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			signature[0] ^= 1
			if _, err = VerifyRecord(encoded, key, signature); err == nil {
				t.Fatal("altered signature accepted")
			}
		})
	}
}
