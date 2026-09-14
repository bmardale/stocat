package encryption

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

var ErrNotConfigured = errors.New("account encryption is not set up")

type SignedRecord struct {
	Record    string `json:"record" minLength:"1" maxLength:"88780" pattern:"^[A-Za-z0-9_-]+$" doc:"Canonical binary record in unpadded base64url."`
	Signature string `json:"signature" minLength:"86" maxLength:"86" pattern:"^[A-Za-z0-9_-]+$" doc:"Detached Ed25519 signature in unpadded base64url."`
}

// Binding holds the installation and account identifiers that every accepted record must contain.
type Binding struct {
	AccountID    string
	Deployment   []byte
	DeploymentID string
}

func LoadBinding(ctx context.Context, queries *db.Queries, accountID string) (Binding, error) {
	deployment, err := queries.GetEncryptionDeployment(ctx)
	if err != nil {
		return Binding{}, fmt.Errorf("load encryption deployment: %w", err)
	}
	return Binding{
		AccountID: accountID, Deployment: deployment.Bytes[:],
		DeploymentID: base64.RawURLEncoding.EncodeToString(deployment.Bytes[:]),
	}, nil
}

// SigningKey returns the signing public key and generation of the current account identity.
func SigningKey(ctx context.Context, queries *db.Queries, userID int64) ([]byte, uint64, error) {
	bundle, err := queries.GetUserKeyBundle(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && bundle.FormatVersion != formatV2 {
		return nil, 0, ErrNotConfigured
	}
	if err != nil {
		return nil, 0, fmt.Errorf("load encryption bundle: %w", err)
	}
	identity, err := queries.GetUserEncryptionIdentity(ctx, db.GetUserEncryptionIdentityParams{
		UserID: userID, Generation: bundle.IdentityGeneration.Int64,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("load encryption identity: %w", err)
	}
	return identity.SigningPublicKey, uint64(identity.Generation), nil
}

// Parse decodes a signed record and checks its type, deployment, and account.
// It does not verify the signature.
func (b Binding) Parse(field, kind string, value SignedRecord) (encryptionv2.Record, []byte, []byte, error) {
	record, data, err := b.ParseRecord(field, kind, value.Record)
	if err != nil {
		return record, nil, nil, err
	}
	signature, err := encryptionv2.DecodeBase64URL(value.Signature, ed25519.SignatureSize)
	if err != nil {
		return record, nil, nil, Invalid(field, err)
	}
	return record, data, signature, nil
}

func (b Binding) ParseRecord(field, kind, value string) (encryptionv2.Record, []byte, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != value {
		return encryptionv2.Record{}, nil, Invalid(field, errors.New("record is not canonical base64url"))
	}
	record, err := encryptionv2.ParseRecord(data)
	switch {
	case err != nil:
	case record.Type != kind:
		err = fmt.Errorf("record type must be %s", kind)
	case record.DeploymentID != b.DeploymentID:
		err = errors.New("record belongs to another deployment")
	case record.Fields["account_id"] != b.AccountID:
		err = errors.New("record belongs to another account")
	}
	if err != nil {
		return record, nil, Invalid(field, err)
	}
	return record, data, nil
}

func Verify(field string, data, signingKey, signature []byte) error {
	if _, err := encryptionv2.VerifyRecord(data, signingKey, signature); err != nil {
		return Invalid(field, err)
	}
	return nil
}

// Counter reads a counter that ParseRecord has already validated.
func Counter(record encryptionv2.Record, name string) uint64 {
	value, _ := strconv.ParseUint(record.Fields[name], 10, 64)
	return value
}

func Invalid(field string, err error) error {
	return huma.Error422UnprocessableEntity("The "+field+" record is not valid.", err)
}

func Encode(data, signature []byte) SignedRecord {
	return SignedRecord{
		Record:    base64.RawURLEncoding.EncodeToString(data),
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}
}

// MaxMetadataRecord is the largest complete node-metadata record that the database stores.
const MaxMetadataRecord = 64 << 10

// ParseSigned parses a signed record, checks its binding, and verifies it with signingKey.
func (b Binding) ParseSigned(field, kind string, value SignedRecord, signingKey []byte) (encryptionv2.Record, []byte, []byte, error) {
	record, data, signature, err := b.Parse(field, kind, value)
	if err != nil {
		return record, nil, nil, err
	}
	if kind == "node-metadata" && len(data) > MaxMetadataRecord {
		return record, nil, nil, Invalid(field, errors.New("record is too large"))
	}
	return record, data, signature, Verify(field, data, signingKey, signature)
}

// Expect compares record fields with expected values. Pairs contain a field name and its value.
func Expect(field string, record encryptionv2.Record, pairs ...string) error {
	for index := 0; index+1 < len(pairs); index += 2 {
		if record.Fields[pairs[index]] != pairs[index+1] {
			return Invalid(field, fmt.Errorf("%s must be %s", pairs[index], pairs[index+1]))
		}
	}
	return nil
}
