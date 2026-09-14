package encryptionv2

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/bmardale/stocat/internal/platform/id"
)

const Argon2Profile = "argon2id-v19-m65536-t3-p4-l32"
const MaxWireRecord = MaxMetadataCiphertext + 2048

// Record stores binary fields as base64url and counters as decimal strings.
type Record struct {
	Type         string            `json:"type"`
	DeploymentID string            `json:"deployment_id"`
	Fields       map[string]string `json:"fields"`
}

type wireField struct {
	name     string
	kind     string
	size     int
	optional bool
}

func resource(name, prefix string) wireField {
	return wireField{name: name, kind: prefix, size: len(prefix) + 27}
}
func counter(name string) wireField { return wireField{name: name, kind: "counter"} }
func binaryField(name string, size int) wireField {
	return wireField{name: name, kind: "binary", size: size}
}
func ciphertext(size int) wireField {
	return wireField{name: "ciphertext", kind: "ciphertext", size: size}
}
func optionalField(field wireField) wireField { field.optional = true; return field }

var recordSchemas = map[string][]wireField{
	"identity":            {resource("account_id", id.User), counter("generation"), binaryField("signing_public_key", 32), binaryField("recipient_public_key", 32), binaryField("recipient_key_id", 32)},
	"identity-continuity": {resource("account_id", id.User), counter("previous_generation"), counter("generation"), binaryField("previous_identity_hash", 32), binaryField("identity_hash", 32)},
	"contact-store":       {resource("account_id", id.User), counter("generation"), counter("revision"), optionalField(binaryField("previous_revision_hash", 32)), binaryField("nonce", 24), ciphertext(MaxMetadataCiphertext)},
	"account-password":    {resource("account_id", id.User), counter("generation"), counter("bundle_revision"), {name: "profile", kind: "profile", size: len(Argon2Profile)}, binaryField("salt", 16), binaryField("nonce", 24), binaryField("ciphertext", 48)},
	"account-recovery":    {resource("account_id", id.User), counter("generation"), counter("bundle_revision"), binaryField("nonce", 24), binaryField("ciphertext", 48)},
	"account-private":     {resource("account_id", id.User), counter("generation"), counter("bundle_revision"), binaryField("nonce", 24), ciphertext(MaxKeyRecord)},
	"owner-root":          {resource("account_id", id.User), resource("library_id", id.Library), resource("node_id", id.Node), counter("generation"), counter("node_epoch"), counter("revision"), binaryField("nonce", 24), binaryField("ciphertext", 48)},
	"parent-envelope":     {resource("account_id", id.User), resource("library_id", id.Library), resource("parent_id", id.Node), resource("child_id", id.Node), counter("parent_epoch"), counter("child_epoch"), counter("generation"), counter("revision"), binaryField("nonce", 24), binaryField("ciphertext", 48)},
	"node-metadata":       {resource("account_id", id.User), resource("library_id", id.Library), resource("node_id", id.Node), counter("node_epoch"), counter("revision"), binaryField("nonce", 24), ciphertext(MaxMetadataCiphertext)},
	"file-key":            {resource("account_id", id.User), resource("library_id", id.Library), resource("node_id", id.Node), resource("version_id", id.FileVersion), binaryField("content_id", 16), counter("node_epoch"), counter("generation"), binaryField("nonce", 24), binaryField("ciphertext", 48)},
	"version-manifest":    {resource("account_id", id.User), resource("library_id", id.Library), resource("node_id", id.Node), resource("version_id", id.FileVersion), binaryField("content_id", 16), counter("node_epoch"), counter("revision"), {name: "header", kind: "header", size: HeaderSize}, binaryField("ciphertext_hash", 32), binaryField("key_envelope_hash", 32)},
	"current-version":     {resource("account_id", id.User), resource("library_id", id.Library), resource("node_id", id.Node), counter("node_epoch"), counter("revision"), optionalField(resource("version_id", id.FileVersion))},
}

func (r Record) MarshalBinary() ([]byte, error) {
	schema, ok := recordSchemas[r.Type]
	if !ok {
		return nil, errors.New("unknown v2 record type")
	}
	deployment, err := DecodeBase64URL(r.DeploymentID, 16)
	if err != nil {
		return nil, fmt.Errorf("deployment identifier: %w", err)
	}
	var tuple Tuple
	if err = tuple.String("stocat/v2/" + r.Type); err != nil {
		return nil, err
	}
	if err = tuple.BytesField(deployment); err != nil {
		return nil, err
	}
	if err = tuple.WriteByte(Suite); err != nil {
		return nil, err
	}
	count := 0
	for _, field := range schema {
		value, present := r.Fields[field.name]
		if field.optional {
			if err = tuple.Flag(present); err != nil {
				return nil, err
			}
			if !present {
				continue
			}
		}
		if !present {
			return nil, fmt.Errorf("missing field %s", field.name)
		}
		count++
		if err = encodeWireField(&tuple, field, value); err != nil {
			return nil, fmt.Errorf("%s: %w", field.name, err)
		}
	}
	if count != len(r.Fields) {
		return nil, errors.New("unknown record field")
	}
	limit := MaxKeyRecord
	if r.Type == "node-metadata" || r.Type == "contact-store" {
		limit = MaxWireRecord
	}
	if tuple.Len() > limit {
		return nil, errors.New("record is too large")
	}
	if err = r.validateContext(); err != nil {
		return nil, err
	}
	return tuple.Bytes(), nil
}

func encodeWireField(tuple *Tuple, field wireField, value string) error {
	if field.kind == "counter" {
		n, err := strconv.ParseUint(value, 10, 63)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != value {
			return errors.New("counter is not canonical or is out of range")
		}
		return tuple.Counter(n)
	}
	if len(value) > base64.RawURLEncoding.EncodedLen(field.size) {
		return errors.New("field is too large")
	}
	var data []byte
	switch field.kind {
	case "binary", "header", "ciphertext":
		size := field.size
		if field.kind == "ciphertext" {
			size = base64.RawURLEncoding.DecodedLen(len(value))
		}
		var err error
		data, err = DecodeBase64URL(value, size)
		if err != nil {
			return err
		}
		if field.kind == "ciphertext" && len(data) < TagSize {
			return errors.New("ciphertext has no complete tag")
		}
		if field.kind == "header" {
			if _, err = ParseFileHeader(data); err != nil {
				return err
			}
		}
	case "profile":
		if value != Argon2Profile {
			return errors.New("unsupported password profile")
		}
		data = []byte(value)
	default:
		if !id.Valid(field.kind, value) {
			return errors.New("invalid resource identifier")
		}
		data = []byte(value)
	}
	return tuple.BytesField(data)
}

func (r Record) validateContext() error {
	if r.Type == "parent-envelope" && r.Fields["parent_id"] == r.Fields["child_id"] {
		return errors.New("parent and child identifiers are equal")
	}
	if r.Type == "version-manifest" {
		raw, err := DecodeBase64URL(r.Fields["header"], HeaderSize)
		if err != nil {
			return err
		}
		header, err := ParseFileHeader(raw)
		if err != nil {
			return err
		}
		if base64.RawURLEncoding.EncodeToString(header.ContentID[:]) != r.Fields["content_id"] {
			return errors.New("manifest content identifier differs from header")
		}
	}
	return nil
}

func ParseRecord(data []byte) (Record, error) {
	if len(data) > MaxWireRecord {
		return Record{}, errors.New("record is too large")
	}
	reader := bytes.NewReader(data)
	domain, err := readWireBytes(reader, 64)
	if err != nil {
		return Record{}, err
	}
	kind, ok := strings.CutPrefix(string(domain), "stocat/v2/")
	schema, known := recordSchemas[kind]
	if !ok || !known {
		return Record{}, errors.New("unknown v2 record type")
	}
	deployment, err := readWireBytes(reader, 16)
	if err != nil {
		return Record{}, err
	}
	suite, err := reader.ReadByte()
	if err != nil || suite != Suite {
		return Record{}, errors.New("unsupported v2 suite")
	}
	record := Record{Type: kind, DeploymentID: base64.RawURLEncoding.EncodeToString(deployment), Fields: make(map[string]string)}
	for _, field := range schema {
		if field.optional {
			flag, readErr := reader.ReadByte()
			if readErr != nil || flag > 1 {
				return Record{}, errors.New("invalid optional-field flag")
			}
			if flag == 0 {
				continue
			}
		}
		value, readErr := decodeWireField(reader, field)
		if readErr != nil {
			return Record{}, fmt.Errorf("%s: %w", field.name, readErr)
		}
		record.Fields[field.name] = value
	}
	if reader.Len() != 0 {
		return Record{}, errors.New("trailing record bytes")
	}
	canonical, err := record.MarshalBinary()
	if err != nil {
		return Record{}, err
	}
	if !bytes.Equal(canonical, data) {
		return Record{}, errors.New("record is not canonical")
	}
	return record, nil
}

func readWireBytes(reader *bytes.Reader, limit int) ([]byte, error) {
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return nil, err
	}
	if uint64(size) > uint64(limit) || uint64(size) > uint64(reader.Len()) {
		return nil, errors.New("invalid field length")
	}
	value := make([]byte, int(size))
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeWireField(reader *bytes.Reader, field wireField) (string, error) {
	if field.kind == "counter" {
		var value uint64
		if err := binary.Read(reader, binary.BigEndian, &value); err != nil {
			return "", err
		}
		if value == 0 || value > math.MaxInt64 {
			return "", errors.New("counter is out of range")
		}
		return strconv.FormatUint(value, 10), nil
	}
	value, err := readWireBytes(reader, field.size)
	if err != nil {
		return "", err
	}
	switch field.kind {
	case "binary", "header", "ciphertext":
		return base64.RawURLEncoding.EncodeToString(value), nil
	default:
		return string(value), nil
	}
}

// AssociatedData excludes the final ciphertext field and its length prefix.
func (r Record) AssociatedData() ([]byte, error) {
	encoded, err := r.MarshalBinary()
	if err != nil {
		return nil, err
	}
	schema := recordSchemas[r.Type]
	if schema[len(schema)-1].name != "ciphertext" {
		return nil, errors.New("record has no ciphertext")
	}
	size := base64.RawURLEncoding.DecodedLen(len(r.Fields["ciphertext"]))
	return encoded[:len(encoded)-4-size], nil
}

func SignatureInput(data []byte) ([]byte, error) {
	record, err := ParseRecord(data)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	var tuple Tuple
	if err = tuple.String("stocat/v2/signature"); err != nil {
		return nil, err
	}
	if err = tuple.String(record.Type); err != nil {
		return nil, err
	}
	if err = tuple.BytesField(hash[:]); err != nil {
		return nil, err
	}
	return tuple.Bytes(), nil
}

func (h FileHeader) FrameAdditionalData(index uint64) ([]byte, error) {
	if _, err := h.CiphertextSize(); err != nil {
		return nil, err
	}
	count, err := h.FrameCount()
	if err != nil {
		return nil, err
	}
	if index >= count {
		return nil, errors.New("frame index is out of range")
	}
	length := min(uint64(h.FrameSize), h.PlaintextSize-index*uint64(h.FrameSize))
	header, err := h.MarshalBinary()
	if err != nil {
		return nil, err
	}
	var tuple Tuple
	if err = tuple.String("stocat/v2/file-frame"); err != nil {
		return nil, err
	}
	if err = tuple.BytesField(header); err != nil {
		return nil, err
	}
	if err = tuple.Counter(index); err != nil {
		return nil, err
	}
	if err = tuple.Counter(length); err != nil {
		return nil, err
	}
	return tuple.Bytes(), nil
}

func VerifyRecord(data, publicKey, signature []byte) (Record, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return Record{}, errors.New("invalid signing public key length")
	}
	input, err := SignatureInput(data)
	if err != nil {
		return Record{}, err
	}
	if !ed25519.Verify(publicKey, input, signature) {
		return Record{}, errors.New("invalid record signature")
	}
	return ParseRecord(data)
}
