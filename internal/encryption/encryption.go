// Package encryption serves v2 account encryption bundles and identities.
package encryption

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryptionv2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	stateAbsent     = "absent"
	stateConfigured = "configured"
	stateLegacy     = "legacy"
	formatV2        = 2
	maxBodyBytes    = 128 << 10
)

type Identity struct {
	Generation          string `json:"generation"`
	Record              string `json:"record"`
	Signature           string `json:"signature"`
	ContinuityRecord    string `json:"continuity_record,omitempty"`
	ContinuitySignature string `json:"continuity_signature,omitempty"`
}

type Bundle struct {
	State            string     `json:"state" enum:"absent,configured,legacy"`
	DeploymentID     string     `json:"deployment_id" doc:"Bind every record to this installation identifier."`
	AccountID        string     `json:"account_id"`
	Generation       string     `json:"generation,omitempty"`
	BundleRevision   string     `json:"bundle_revision,omitempty"`
	PasswordEnvelope string     `json:"password_envelope,omitempty"`
	RecoveryEnvelope string     `json:"recovery_envelope,omitempty"`
	PrivateEnvelope  string     `json:"private_envelope,omitempty"`
	Identities       []Identity `json:"identities" nullable:"false" doc:"All identity generations in ascending order."`
}

type bundleOutput struct {
	ETag string `header:"ETag"`
	Body Bundle
}

type initializeInput struct {
	Body struct {
		Identity         SignedRecord `json:"identity" doc:"Sign with the identity's own signing key."`
		PasswordEnvelope SignedRecord `json:"password_envelope"`
		RecoveryEnvelope SignedRecord `json:"recovery_envelope"`
		PrivateEnvelope  SignedRecord `json:"private_envelope"`
	}
}

type updateInput struct {
	IfMatch string `header:"If-Match" required:"true" doc:"Send the ETag of the current bundle."`
	Body    struct {
		PasswordEnvelope *SignedRecord `json:"password_envelope,omitempty"`
		RecoveryEnvelope *SignedRecord `json:"recovery_envelope,omitempty"`
		PrivateEnvelope  *SignedRecord `json:"private_envelope,omitempty"`
	}
}

type rotateInput struct {
	IfMatch string `header:"If-Match" required:"true" doc:"Send the ETag of the current bundle."`
	Body    struct {
		Identity         SignedRecord `json:"identity" doc:"Sign with the new identity's signing key."`
		Continuity       SignedRecord `json:"continuity" doc:"Sign with the current identity's signing key."`
		PasswordEnvelope SignedRecord `json:"password_envelope"`
		RecoveryEnvelope SignedRecord `json:"recovery_envelope"`
		PrivateEnvelope  SignedRecord `json:"private_envelope"`
	}
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	auth    *auth.Service
	log     *slog.Logger
}

func New(pool *pgxpool.Pool, authService *auth.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), auth: authService, log: logger}
}

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/encryption")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Encryption"} })
	huma.Register(group, huma.Operation{
		OperationID: "encryption-bundle-get", Method: http.MethodGet, Path: "/bundle",
		Summary: "Get the account encryption bundle",
	}, s.get)
	huma.Register(group, huma.Operation{
		OperationID: "encryption-bundle-create", Method: http.MethodPost, Path: "/bundle",
		Summary: "Set up account encryption", DefaultStatus: http.StatusCreated, MaxBodyBytes: maxBodyBytes,
		Description: "Requires a recent sign-in or password confirmation.",
		Errors:      []int{http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.initialize)
	huma.Register(group, huma.Operation{
		OperationID: "encryption-bundle-update", Method: http.MethodPut, Path: "/bundle",
		Summary: "Replace account encryption envelopes", MaxBodyBytes: maxBodyBytes,
		Description: "Requires a recent sign-in or password confirmation. " +
			"Each envelope uses the next bundle revision and a signature from the current identity.",
		Errors: []int{http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.update)
	huma.Register(group, huma.Operation{
		OperationID: "encryption-identity-rotate", Method: http.MethodPost, Path: "/identities/rotate",
		Summary: "Publish the next account encryption identity", MaxBodyBytes: maxBodyBytes,
		Description: "Requires a recent sign-in or password confirmation. " +
			"The envelopes use the new generation and bundle revision 1.",
		Errors: []int{http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity},
	}, s.rotate)
}

type account struct {
	id int64
	Binding
}

func (s *Service) account(ctx context.Context) (account, error) {
	user, _ := auth.UserFromContext(ctx)
	binding, err := LoadBinding(ctx, s.queries, user.PublicID)
	if err != nil {
		return account{}, s.internalError(ctx, "load encryption binding", err)
	}
	return account{id: user.ID, Binding: binding}, nil
}

func (s *Service) get(ctx context.Context, _ *struct{}) (*bundleOutput, error) {
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	output := &bundleOutput{Body: Bundle{
		State: stateAbsent, DeploymentID: acct.DeploymentID, AccountID: acct.AccountID, Identities: []Identity{},
	}}
	bundle, err := s.queries.GetUserKeyBundle(ctx, acct.id)
	if errors.Is(err, pgx.ErrNoRows) {
		return output, nil
	}
	if err != nil {
		return nil, s.internalError(ctx, "load encryption bundle", err)
	}
	if bundle.FormatVersion != formatV2 {
		output.Body.State = stateLegacy
		return output, nil
	}
	identities, err := s.queries.ListUserEncryptionIdentities(ctx, acct.id)
	if err != nil {
		return nil, s.internalError(ctx, "list encryption identities", err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	output.ETag = etag(bundle)
	output.Body.State = stateConfigured
	output.Body.Generation = strconv.FormatInt(bundle.IdentityGeneration.Int64, 10)
	output.Body.BundleRevision = strconv.FormatInt(bundle.BundleRevision, 10)
	output.Body.PasswordEnvelope = encode(bundle.EncryptedMasterKey)
	output.Body.RecoveryEnvelope = encode(bundle.RecoveryEncryptedMasterKey)
	output.Body.PrivateEnvelope = encode(bundle.PrivateKeyEnvelope)
	for _, row := range identities {
		output.Body.Identities = append(output.Body.Identities, Identity{
			Generation: strconv.FormatInt(row.Generation, 10), Record: encode(row.Certificate),
			Signature: encode(row.CertificateSignature), ContinuityRecord: encode(row.ContinuityCertificate),
			ContinuitySignature: encode(row.ContinuitySignature),
		})
	}
	return output, nil
}

func (s *Service) initialize(ctx context.Context, input *initializeInput) (*bundleOutput, error) {
	if err := s.auth.RequireRecentAuthentication(ctx); err != nil {
		return nil, err
	}
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	identity, err := acct.identity(input.Body.Identity, 1)
	if err != nil {
		return nil, err
	}
	envelopes, err := acct.envelopes(input.Body.PasswordEnvelope, input.Body.RecoveryEnvelope, input.Body.PrivateEnvelope,
		1, 1, identity.signingKey)
	if err != nil {
		return nil, err
	}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		if err := queries.CreateUserEncryptionIdentity(ctx, identity.params(acct.id, nil, nil)); err != nil {
			return err
		}
		if _, err := queries.CreateV2KeyBundle(ctx, db.CreateV2KeyBundleParams{
			UserID: acct.id, KdfSalt: envelopes.salt, EncryptedMasterKey: envelopes.password,
			RecoveryEncryptedMasterKey: envelopes.recovery, PrivateKeyEnvelope: envelopes.private,
			IdentityGeneration: pgtype.Int8{Int64: 1, Valid: true}, BundleRevision: 1,
		}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{Action: audit.EncryptionInitialized, ActorID: acct.id})
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return nil, huma.Error409Conflict("Account encryption is already set up.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "set up account encryption", err)
	}
	return s.get(ctx, nil)
}

func (s *Service) update(ctx context.Context, input *updateInput) (*bundleOutput, error) {
	body := input.Body
	if body.PasswordEnvelope == nil && body.RecoveryEnvelope == nil && body.PrivateEnvelope == nil {
		return nil, huma.Error422UnprocessableEntity("Send at least one envelope.")
	}
	if err := s.auth.RequireRecentAuthentication(ctx); err != nil {
		return nil, err
	}
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		bundle, current, err := lockBundle(ctx, queries, acct.id, input.IfMatch)
		if err != nil {
			return err
		}
		generation, revision := uint64(bundle.IdentityGeneration.Int64), uint64(bundle.BundleRevision)+1
		next := envelopeSet{
			salt: bundle.KdfSalt, password: bundle.EncryptedMasterKey,
			recovery: bundle.RecoveryEncryptedMasterKey, private: bundle.PrivateKeyEnvelope,
		}
		if body.PasswordEnvelope != nil {
			if next.password, next.salt, err = acct.passwordEnvelope(*body.PasswordEnvelope, generation, revision, current.SigningPublicKey); err != nil {
				return err
			}
		}
		if body.RecoveryEnvelope != nil {
			if next.recovery, err = acct.envelope("recovery_envelope", "account-recovery", *body.RecoveryEnvelope, generation, revision, current.SigningPublicKey); err != nil {
				return err
			}
		}
		if body.PrivateEnvelope != nil {
			if next.private, err = acct.envelope("private_envelope", "account-private", *body.PrivateEnvelope, generation, revision, current.SigningPublicKey); err != nil {
				return err
			}
		}
		if err = next.store(ctx, queries, bundle, generation, revision); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{Action: audit.EncryptionBundleUpdated, ActorID: acct.id})
	})
	if err != nil {
		return nil, s.result(ctx, "update encryption bundle", err)
	}
	return s.get(ctx, nil)
}

func (s *Service) rotate(ctx context.Context, input *rotateInput) (*bundleOutput, error) {
	if err := s.auth.RequireRecentAuthentication(ctx); err != nil {
		return nil, err
	}
	acct, err := s.account(ctx)
	if err != nil {
		return nil, err
	}
	body := input.Body
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		bundle, current, err := lockBundle(ctx, queries, acct.id, input.IfMatch)
		if err != nil {
			return err
		}
		generation := uint64(current.Generation) + 1
		identity, err := acct.identity(body.Identity, generation)
		if err != nil {
			return err
		}
		continuity, continuitySignature, err := acct.continuity(body.Continuity, current, identity)
		if err != nil {
			return err
		}
		envelopes, err := acct.envelopes(body.PasswordEnvelope, body.RecoveryEnvelope, body.PrivateEnvelope,
			generation, 1, identity.signingKey)
		if err != nil {
			return err
		}
		if err = queries.CreateUserEncryptionIdentity(ctx, identity.params(acct.id, continuity, continuitySignature)); err != nil {
			return err
		}
		if err = envelopes.store(ctx, queries, bundle, generation, 1); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{Action: audit.EncryptionIdentityRotated, ActorID: acct.id})
	})
	if err != nil {
		return nil, s.result(ctx, "rotate encryption identity", err)
	}
	return s.get(ctx, nil)
}

func lockBundle(ctx context.Context, queries *db.Queries, userID int64, ifMatch string) (db.UserKeyBundle, db.UserEncryptionIdentity, error) {
	bundle, err := queries.GetUserKeyBundleForUpdate(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && bundle.FormatVersion != formatV2 {
		return bundle, db.UserEncryptionIdentity{}, huma.Error409Conflict("Set up account encryption first.")
	}
	if err != nil {
		return bundle, db.UserEncryptionIdentity{}, err
	}
	if ifMatch != etag(bundle) {
		return bundle, db.UserEncryptionIdentity{}, huma.Error409Conflict("The encryption bundle changed. Load it again.")
	}
	current, err := queries.GetUserEncryptionIdentity(ctx, db.GetUserEncryptionIdentityParams{
		UserID: userID, Generation: bundle.IdentityGeneration.Int64,
	})
	return bundle, current, err
}

type envelopeSet struct {
	salt, password, recovery, private []byte
}

func (e envelopeSet) store(ctx context.Context, queries *db.Queries, bundle db.UserKeyBundle, generation, revision uint64) error {
	_, err := queries.UpdateV2KeyBundle(ctx, db.UpdateV2KeyBundleParams{
		KdfSalt: e.salt, EncryptedMasterKey: e.password, RecoveryEncryptedMasterKey: e.recovery,
		PrivateKeyEnvelope: e.private, IdentityGeneration: pgtype.Int8{Int64: int64(generation), Valid: true},
		BundleRevision: int64(revision), UserID: bundle.UserID,
		ExpectedGeneration: bundle.IdentityGeneration, ExpectedRevision: bundle.BundleRevision,
	})
	return err
}

type verifiedIdentity struct {
	record, signature                        []byte
	generation                               uint64
	signingKey, recipientKey, recipientKeyID []byte
}

func (v verifiedIdentity) params(userID int64, continuity, continuitySignature []byte) db.CreateUserEncryptionIdentityParams {
	return db.CreateUserEncryptionIdentityParams{
		UserID: userID, Generation: int64(v.generation), SigningPublicKey: v.signingKey,
		RecipientPublicKey: v.recipientKey, RecipientKeyID: v.recipientKeyID,
		Certificate: v.record, CertificateSignature: v.signature,
		ContinuityCertificate: continuity, ContinuitySignature: continuitySignature,
	}
}

func (a account) identity(value SignedRecord, generation uint64) (verifiedIdentity, error) {
	const field = "identity"
	record, data, signature, err := a.Parse(field, "identity", value)
	if err != nil {
		return verifiedIdentity{}, err
	}
	result := verifiedIdentity{record: data, signature: signature, generation: generation}
	if Counter(record, "generation") != generation {
		return result, Invalid(field, fmt.Errorf("generation must be %d", generation))
	}
	result.signingKey, _ = encryptionv2.DecodeBase64URL(record.Fields["signing_public_key"], ed25519.PublicKeySize)
	result.recipientKey, _ = encryptionv2.DecodeBase64URL(record.Fields["recipient_public_key"], 32)
	result.recipientKeyID, _ = encryptionv2.DecodeBase64URL(record.Fields["recipient_key_id"], 32)
	if err = encryptionv2.ValidateSigningPublicKey(result.signingKey); err != nil {
		return result, Invalid(field, err)
	}
	if err = encryptionv2.ValidateRecipientPublicKey(result.recipientKey); err != nil {
		return result, Invalid(field, err)
	}
	keyID, err := encryptionv2.RecipientKeyID(a.Deployment, a.AccountID, generation, result.recipientKey)
	if err != nil {
		return result, Invalid(field, err)
	}
	if !bytes.Equal(keyID, result.recipientKeyID) {
		return result, Invalid(field, errors.New("recipient key identifier does not match the recipient public key"))
	}
	return result, Verify(field, data, result.signingKey, signature)
}

func (a account) continuity(value SignedRecord, current db.UserEncryptionIdentity, next verifiedIdentity) ([]byte, []byte, error) {
	const field = "continuity"
	record, data, signature, err := a.Parse(field, "identity-continuity", value)
	if err != nil {
		return nil, nil, err
	}
	if Counter(record, "previous_generation") != uint64(current.Generation) || Counter(record, "generation") != next.generation {
		return nil, nil, Invalid(field, fmt.Errorf("generations must be %d and %d", current.Generation, next.generation))
	}
	previousHash, nextHash := sha256.Sum256(current.Certificate), sha256.Sum256(next.record)
	if record.Fields["previous_identity_hash"] != base64.RawURLEncoding.EncodeToString(previousHash[:]) ||
		record.Fields["identity_hash"] != base64.RawURLEncoding.EncodeToString(nextHash[:]) {
		return nil, nil, Invalid(field, errors.New("identity hashes do not match the current and new identities"))
	}
	return data, signature, Verify(field, data, current.SigningPublicKey, signature)
}

func (a account) envelopes(password, recovery, private SignedRecord, generation, revision uint64, signingKey []byte) (envelopeSet, error) {
	var result envelopeSet
	var err error
	if result.password, result.salt, err = a.passwordEnvelope(password, generation, revision, signingKey); err != nil {
		return result, err
	}
	if result.recovery, err = a.envelope("recovery_envelope", "account-recovery", recovery, generation, revision, signingKey); err != nil {
		return result, err
	}
	result.private, err = a.envelope("private_envelope", "account-private", private, generation, revision, signingKey)
	return result, err
}

func (a account) passwordEnvelope(value SignedRecord, generation, revision uint64, signingKey []byte) ([]byte, []byte, error) {
	data, err := a.envelope("password_envelope", "account-password", value, generation, revision, signingKey)
	if err != nil {
		return nil, nil, err
	}
	record, err := encryptionv2.ParseRecord(data)
	if err != nil {
		return nil, nil, Invalid("password_envelope", err)
	}
	salt, err := encryptionv2.DecodeBase64URL(record.Fields["salt"], 16)
	return data, salt, err
}

func (a account) envelope(field, kind string, value SignedRecord, generation, revision uint64, signingKey []byte) ([]byte, error) {
	record, data, signature, err := a.Parse(field, kind, value)
	if err != nil {
		return nil, err
	}
	if Counter(record, "generation") != generation || Counter(record, "bundle_revision") != revision {
		return nil, Invalid(field, fmt.Errorf("record must use generation %d and bundle revision %d", generation, revision))
	}
	return data, Verify(field, data, signingKey, signature)
}

func etag(bundle db.UserKeyBundle) string {
	return fmt.Sprintf(`"%d.%d"`, bundle.IdentityGeneration.Int64, bundle.BundleRevision)
}

// result keeps client errors from inside a transaction and hides other errors.
func (s *Service) result(ctx context.Context, operation string, err error) error {
	if statusErr, ok := errors.AsType[huma.StatusError](err); ok {
		return statusErr
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" {
		return huma.Error409Conflict("The encryption bundle changed. Load it again.")
	}
	return s.internalError(ctx, operation, err)
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("Encryption is unavailable.")
}
