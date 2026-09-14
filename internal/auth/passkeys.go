package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	ceremonyLifetime     = 5 * time.Minute
	ceremonyRegistration = "registration"
	ceremonyLogin        = "login"
	passkeyHandleBytes   = 64
)

type WebAuthnConfig struct {
	RPID    string
	RPName  string
	Origins []string
}

type Passkey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Synced     bool       `json:"synced" doc:"The authenticator backs up the passkey, for example to a cloud account."`
}

type passkeyOptionsOutput struct {
	Body json.RawMessage
}

type passkeyLoginInput struct {
	Body json.RawMessage
}

type passkeyRegistrationOptionsInput struct {
	Body struct {
		Password string `json:"password" writeOnly:"true"`
	}
}

type createPasskeyInput struct {
	Body struct {
		Name       string          `json:"name" minLength:"1" maxLength:"100"`
		Credential json.RawMessage `json:"credential" doc:"The RegistrationResponseJSON value from PublicKeyCredential.toJSON()."`
	}
}

type renamePasskeyInput struct {
	ID   string `path:"id" maxLength:"64"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
	}
}

type deletePasskeyInput struct {
	ID string `path:"id" maxLength:"64"`
}

type passkeyOutput struct {
	Body Passkey
}

type passkeysOutput struct {
	Body []Passkey `nullable:"false"`
}

type passkeyUser struct {
	user        User
	handle      []byte
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.user.Email }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.user.Name }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func newWebAuthn(cfg WebAuthnConfig) (*webauthn.WebAuthn, error) {
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyLifetime, TimeoutUVD: ceremonyLifetime}
	requireResidentKey := true
	return webauthn.New(&webauthn.Config{
		RPID: cfg.RPID, RPDisplayName: cfg.RPName, RPOrigins: cfg.Origins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: &requireResidentKey,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

func (s *Service) passkeysAvailable() error {
	if s.webauthn == nil {
		return huma.Error503ServiceUnavailable("Passkeys are not configured.")
	}
	return nil
}

func (s *Service) passkeyLoginOptions(ctx context.Context, _ *struct{}) (*passkeyOptionsOutput, error) {
	if err := s.passkeysAvailable(); err != nil {
		return nil, err
	}
	assertion, session, err := s.webauthn.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, s.internalError(ctx, "begin passkey login", err)
	}
	if err := s.saveCeremony(ctx, ceremonyLogin, pgtype.Int8{}, session); err != nil {
		return nil, s.internalError(ctx, "save passkey login", err)
	}
	return optionsOutput(ctx, s, assertion.Response)
}

func (s *Service) passkeyLogin(ctx context.Context, input *passkeyLoginInput) (*sessionOutput, error) {
	if err := s.passkeysAvailable(); err != nil {
		return nil, err
	}
	rejected := huma.Error401Unauthorized("The passkey is not valid. Try again or sign in with your password.")
	parsed, err := protocol.ParseCredentialRequestResponseBytes(input.Body)
	if err != nil {
		s.logPasskeyRejection(ctx, "parse passkey login", err)
		return nil, rejected
	}
	session, err := s.consumeCeremony(ctx, ceremonyLogin, parsed.Response.CollectedClientData.Challenge, pgtype.Int8{})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error401Unauthorized("The passkey request expired. Try again.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "load passkey login", err)
	}
	var owner *passkeyUser
	var lookupErr error
	_, credential, err := s.webauthn.ValidatePasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
		row, err := s.queries.GetPasskeyUserByHandle(ctx, handle)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				lookupErr = err
			}
			return nil, err
		}
		owner, err = s.passkeyUser(ctx, s.queries, publicUser(row.User), row.Handle)
		lookupErr = err
		return owner, err
	}, session, parsed)
	if lookupErr != nil {
		return nil, s.internalError(ctx, "load passkey owner", lookupErr)
	}
	if err != nil {
		s.logPasskeyRejection(ctx, "verify passkey login", err)
		return nil, rejected
	}
	// A sign counter that does not increase can indicate a cloned authenticator.
	if credential.Authenticator.CloneWarning {
		s.log.WarnContext(ctx, "passkey sign counter did not increase", "user", owner.user.PublicID)
		return nil, rejected
	}
	output := &sessionOutput{Body: owner.user}
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		updated, err := queries.UpdatePasskeyUsage(ctx, db.UpdatePasskeyUsageParams{
			SignCount: int64(credential.Authenticator.SignCount), Flags: int16(credential.Flags.ProtocolValue()),
			RpID: s.rpID, CredentialID: credential.ID,
		})
		if err != nil {
			return err
		}
		if updated == 0 {
			return errInvalidCredentials
		}
		if output.SetCookie, err = s.createSession(ctx, queries, owner.user.ID); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.AccountSignedIn, ActorID: owner.user.ID, Details: audit.Details{Method: audit.MethodPasskey},
		})
	})
	if errors.Is(err, errInvalidCredentials) {
		return nil, rejected
	}
	if err != nil {
		return nil, s.internalError(ctx, "login user with passkey", err)
	}
	return output, nil
}

func (s *Service) listPasskeys(ctx context.Context, _ *struct{}) (*passkeysOutput, error) {
	user, _ := UserFromContext(ctx)
	rows, err := s.queries.ListPasskeys(ctx, db.ListPasskeysParams{UserID: user.ID, RpID: s.rpID})
	if err != nil {
		return nil, s.internalError(ctx, "list passkeys", err)
	}
	output := &passkeysOutput{Body: make([]Passkey, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, publicPasskey(row))
	}
	return output, nil
}

func (s *Service) passkeyRegistrationOptions(ctx context.Context, input *passkeyRegistrationOptionsInput) (*passkeyOptionsOutput, error) {
	if err := s.passkeysAvailable(); err != nil {
		return nil, err
	}
	if input.Body.Password == "" || len(input.Body.Password) > 1024 {
		return nil, huma.Error422UnprocessableEntity("The current password is incorrect.")
	}
	current, _ := UserFromContext(ctx)
	stored, err := s.queries.GetUserByID(ctx, current.ID)
	if err != nil {
		return nil, s.internalError(ctx, "load user", err)
	}
	if !verifyPassword(input.Body.Password, stored.PasswordHash) {
		return nil, huma.Error422UnprocessableEntity("The current password is incorrect.")
	}
	account, err := s.ensurePasskeyUser(ctx, current)
	if err != nil {
		return nil, s.internalError(ctx, "load passkey user", err)
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(account.credentials))
	for _, credential := range account.credentials {
		exclusions = append(exclusions, credential.Descriptor())
	}
	creation, session, err := s.webauthn.BeginRegistration(account, webauthn.WithExclusions(exclusions))
	if err != nil {
		return nil, s.internalError(ctx, "begin passkey registration", err)
	}
	if err := s.saveCeremony(ctx, ceremonyRegistration, pgtype.Int8{Int64: current.ID, Valid: true}, session); err != nil {
		return nil, s.internalError(ctx, "save passkey registration", err)
	}
	return optionsOutput(ctx, s, creation.Response)
}

func (s *Service) createPasskey(ctx context.Context, input *createPasskeyInput) (*passkeyOutput, error) {
	if err := s.passkeysAvailable(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a passkey name.")
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(input.Body.Credential)
	if err != nil {
		s.logPasskeyRejection(ctx, "parse passkey registration", err)
		return nil, huma.Error422UnprocessableEntity("The passkey response is not valid.")
	}
	current, _ := UserFromContext(ctx)
	session, err := s.consumeCeremony(ctx, ceremonyRegistration, parsed.Response.CollectedClientData.Challenge,
		pgtype.Int8{Int64: current.ID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error422UnprocessableEntity("The passkey request expired. Try again.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "load passkey registration", err)
	}
	account, err := s.ensurePasskeyUser(ctx, current)
	if err != nil {
		return nil, s.internalError(ctx, "load passkey user", err)
	}
	credential, err := s.webauthn.CreateCredential(account, session, parsed)
	if err != nil {
		s.logPasskeyRejection(ctx, "verify passkey registration", err)
		return nil, huma.Error422UnprocessableEntity("The passkey is not valid.")
	}
	params, err := passkeyParams(current.ID, s.rpID, name, credential)
	if err != nil {
		return nil, s.internalError(ctx, "encode passkey", err)
	}
	var row db.Passkey
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		if row, err = queries.CreatePasskey(ctx, params); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.PasskeyAdded, ActorID: current.ID, TargetID: row.PublicID, Details: audit.Details{Name: row.Name},
		})
	})
	if isPasskeyConflict(err) {
		return nil, huma.Error409Conflict("This passkey is already registered.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "create passkey", err)
	}
	return &passkeyOutput{Body: publicPasskey(row)}, nil
}

func (s *Service) renamePasskey(ctx context.Context, input *renamePasskeyInput) (*passkeyOutput, error) {
	name := strings.TrimSpace(input.Body.Name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("Enter a passkey name.")
	}
	user, _ := UserFromContext(ctx)
	var row db.Passkey
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		if row, err = queries.RenamePasskey(ctx, db.RenamePasskeyParams{Name: name, PublicID: input.ID, UserID: user.ID}); err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.PasskeyRenamed, ActorID: user.ID, TargetID: row.PublicID, Details: audit.Details{Name: row.Name},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The passkey does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "rename passkey", err)
	}
	return &passkeyOutput{Body: publicPasskey(row)}, nil
}

func (s *Service) deletePasskey(ctx context.Context, input *deletePasskeyInput) (*struct{}, error) {
	user, _ := UserFromContext(ctx)
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		name, err := queries.DeletePasskey(ctx, db.DeletePasskeyParams{PublicID: input.ID, UserID: user.ID})
		if err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.PasskeyDeleted, ActorID: user.ID, TargetID: input.ID, Details: audit.Details{Name: name},
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("The passkey does not exist.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete passkey", err)
	}
	return &struct{}{}, nil
}

func optionsOutput(ctx context.Context, s *Service, options any) (*passkeyOptionsOutput, error) {
	body, err := json.Marshal(options)
	if err != nil {
		return nil, s.internalError(ctx, "encode passkey options", err)
	}
	return &passkeyOptionsOutput{Body: body}, nil
}

func (s *Service) ensurePasskeyUser(ctx context.Context, user User) (*passkeyUser, error) {
	handle := make([]byte, passkeyHandleBytes)
	_, _ = rand.Read(handle)
	handle, err := s.queries.EnsurePasskeyUser(ctx, db.EnsurePasskeyUserParams{UserID: user.ID, Handle: handle})
	if err != nil {
		return nil, err
	}
	return s.passkeyUser(ctx, s.queries, user, handle)
}

func (s *Service) passkeyUser(ctx context.Context, queries *db.Queries, user User, handle []byte) (*passkeyUser, error) {
	rows, err := queries.ListPasskeys(ctx, db.ListPasskeysParams{UserID: user.ID, RpID: s.rpID})
	if err != nil {
		return nil, err
	}
	account := &passkeyUser{user: user, handle: handle, credentials: make([]webauthn.Credential, 0, len(rows))}
	for _, row := range rows {
		credential, err := storedCredential(row)
		if err != nil {
			return nil, err
		}
		account.credentials = append(account.credentials, credential)
	}
	return account, nil
}

func (s *Service) saveCeremony(ctx context.Context, kind string, userID pgtype.Int8, session *webauthn.SessionData) error {
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode %s ceremony: %w", kind, err)
	}
	if err := s.queries.DeleteExpiredWebAuthnCeremonies(ctx); err != nil {
		return fmt.Errorf("delete expired ceremonies: %w", err)
	}
	return s.queries.CreateWebAuthnCeremony(ctx, db.CreateWebAuthnCeremonyParams{
		Challenge: session.Challenge, Kind: kind, UserID: userID, SessionData: data,
		ExpiresAt: pgtype.Timestamptz{Time: session.Expires, Valid: true},
	})
}

// consumeCeremony deletes the ceremony before verification, so each challenge allows one attempt.
func (s *Service) consumeCeremony(ctx context.Context, kind, challenge string, userID pgtype.Int8) (webauthn.SessionData, error) {
	var session webauthn.SessionData
	data, err := s.queries.ConsumeWebAuthnCeremony(ctx, db.ConsumeWebAuthnCeremonyParams{
		Challenge: challenge, Kind: kind, UserID: userID,
	})
	if err != nil {
		return session, err
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return session, fmt.Errorf("decode %s ceremony: %w", kind, err)
	}
	return session, nil
}

func (s *Service) logPasskeyRejection(ctx context.Context, operation string, err error) {
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) {
		s.log.InfoContext(ctx, operation, "error", protocolErr.Details, "debug", protocolErr.DevInfo)
		return
	}
	s.log.InfoContext(ctx, operation, "error", err)
}

func passkeyParams(userID int64, rpID, name string, credential *webauthn.Credential) (db.CreatePasskeyParams, error) {
	attestation, err := json.Marshal(credential.Attestation)
	if err != nil {
		return db.CreatePasskeyParams{}, fmt.Errorf("encode attestation: %w", err)
	}
	extensions, err := json.Marshal(credential.Extensions)
	if err != nil {
		return db.CreatePasskeyParams{}, fmt.Errorf("encode extensions: %w", err)
	}
	transports := make([]string, 0, len(credential.Transport))
	for _, transport := range credential.Transport {
		transports = append(transports, string(transport))
	}
	return db.CreatePasskeyParams{
		PublicID: id.New(id.Passkey), UserID: userID, RpID: rpID, CredentialID: credential.ID, Name: name,
		PublicKey: credential.PublicKey, AttestationType: credential.AttestationType,
		AttestationFormat: credential.AttestationFormat, Attestation: attestation, Extensions: extensions,
		Transports: transports, Aaguid: credential.Authenticator.AAGUID,
		Attachment: string(credential.Authenticator.Attachment),
		SignCount:  int64(credential.Authenticator.SignCount), Flags: int16(credential.Flags.ProtocolValue()),
	}, nil
}

func storedCredential(row db.Passkey) (webauthn.Credential, error) {
	credential := webauthn.Credential{
		ID: row.CredentialID, PublicKey: row.PublicKey,
		AttestationType: row.AttestationType, AttestationFormat: row.AttestationFormat,
		Flags: webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(row.Flags)),
		Authenticator: webauthn.Authenticator{
			AAGUID: row.Aaguid, SignCount: uint32(row.SignCount),
			Attachment: protocol.AuthenticatorAttachment(row.Attachment),
		},
	}
	for _, transport := range row.Transports {
		credential.Transport = append(credential.Transport, protocol.AuthenticatorTransport(transport))
	}
	if err := json.Unmarshal(row.Attestation, &credential.Attestation); err != nil {
		return webauthn.Credential{}, fmt.Errorf("decode passkey %s attestation: %w", row.PublicID, err)
	}
	if err := json.Unmarshal(row.Extensions, &credential.Extensions); err != nil {
		return webauthn.Credential{}, fmt.Errorf("decode passkey %s extensions: %w", row.PublicID, err)
	}
	return credential, nil
}

func publicPasskey(row db.Passkey) Passkey {
	passkey := Passkey{
		ID: row.PublicID, Name: row.Name, CreatedAt: row.CreatedAt.Time,
		Synced: protocol.AuthenticatorFlags(row.Flags).HasBackupState(),
	}
	if row.LastUsedAt.Valid {
		lastUsed := row.LastUsedAt.Time
		passkey.LastUsedAt = &lastUsed
	}
	return passkey
}

func isPasskeyConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "passkeys_rp_id_credential_id_key"
}
