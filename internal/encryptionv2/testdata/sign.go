package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
)

func main() {
	if err := generate(); err != nil {
		panic(err)
	}
}

func generate() error {
	const path = "internal/encryptionv2/testdata/records.json"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var fixtures []map[string]any
	if err = json.Unmarshal(data, &fixtures); err != nil {
		return err
	}
	seed, err := hex.DecodeString("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	if err != nil {
		return err
	}
	key := ed25519.NewKeyFromSeed(seed)
	for _, fixture := range fixtures {
		input, err := hex.DecodeString(fixture["signature_input_hex"].(string))
		if err != nil {
			return err
		}
		fixture["signing_public_key_hex"] = hex.EncodeToString(key.Public().(ed25519.PublicKey))
		fixture["signature_hex"] = hex.EncodeToString(ed25519.Sign(key, input))
	}
	data, err = json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
