package admin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/settings"
	"github.com/danielgtaylor/huma/v2"
)

const (
	inviteCodeLength   = 12
	inviteCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

type RegistrationSettings struct {
	InviteOnly bool `json:"invite_only"`
}

type InviteCode struct {
	ID        string     `json:"id"`
	Code      string     `json:"code"`
	CreatedAt time.Time  `json:"created_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

type registrationSettingsInput struct {
	Body RegistrationSettings
}

type registrationSettingsOutput struct {
	Body RegistrationSettings
}

type inviteCodeOutput struct {
	Body InviteCode
}

type inviteCodesOutput struct {
	Body []InviteCode `nullable:"false"`
}

type inviteCodeInput struct {
	ID string `path:"id" maxLength:"64"`
}

var errInviteCodeNotFound = errors.New("invite code not found")

func (s *Service) registerRegistration(api huma.API) {
	config := huma.NewGroup(api, "/config")
	config.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Admin"}
	})
	huma.Register(config, huma.Operation{
		OperationID: "admin-config-get", Method: http.MethodGet, Path: "",
		Summary: "Get registration settings",
	}, s.getRegistrationSettings)
	huma.Register(config, huma.Operation{
		OperationID: "admin-config-set", Method: http.MethodPut, Path: "",
		Summary: "Set registration settings", MaxBodyBytes: 4096,
	}, s.setRegistrationSettings)

	codes := huma.NewGroup(api, "/invite-codes")
	codes.UseSimpleModifier(func(op *huma.Operation) {
		op.Tags = []string{"Admin"}
	})
	huma.Register(codes, huma.Operation{
		OperationID: "admin-invite-codes-list", Method: http.MethodGet, Path: "",
		Summary: "List invite codes",
	}, s.listInviteCodes)
	huma.Register(codes, huma.Operation{
		OperationID: "admin-invite-codes-create", Method: http.MethodPost, Path: "",
		Summary: "Create an invite code", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusInternalServerError},
	}, s.createInviteCode)
	huma.Register(codes, huma.Operation{
		OperationID: "admin-invite-codes-delete", Method: http.MethodDelete, Path: "/{id}",
		Summary: "Revoke an unused invite code", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusNotFound},
	}, s.deleteInviteCode)
}

func (s *Service) getRegistrationSettings(ctx context.Context, _ *struct{}) (*registrationSettingsOutput, error) {
	inviteOnly, err := settings.RegistrationInviteOnly(ctx, s.queries)
	if err != nil {
		return nil, s.internalError(ctx, "get registration settings", err)
	}
	return &registrationSettingsOutput{Body: RegistrationSettings{InviteOnly: inviteOnly}}, nil
}

func (s *Service) setRegistrationSettings(
	ctx context.Context, input *registrationSettingsInput,
) (*registrationSettingsOutput, error) {
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		if err := settings.SetRegistrationInviteOnly(ctx, queries, input.Body.InviteOnly); err != nil {
			return err
		}
		admin, _ := auth.UserFromContext(ctx)
		inviteOnly := input.Body.InviteOnly
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.RegistrationSettingsUpdated, ActorID: admin.ID,
			Details: audit.Details{InviteOnly: &inviteOnly},
		})
	})
	if err != nil {
		return nil, s.internalError(ctx, "set registration settings", err)
	}
	return &registrationSettingsOutput{Body: input.Body}, nil
}

func (s *Service) listInviteCodes(ctx context.Context, _ *struct{}) (*inviteCodesOutput, error) {
	rows, err := s.queries.ListInviteCodes(ctx)
	if err != nil {
		return nil, s.internalError(ctx, "list invite codes", err)
	}
	output := &inviteCodesOutput{Body: make([]InviteCode, 0, len(rows))}
	for _, row := range rows {
		output.Body = append(output.Body, inviteCodeFromRow(row))
	}
	return output, nil
}

func (s *Service) createInviteCode(ctx context.Context, _ *struct{}) (*inviteCodeOutput, error) {
	code, err := newInviteCode()
	if err != nil {
		return nil, s.internalError(ctx, "create invite code", err)
	}
	admin, _ := auth.UserFromContext(ctx)
	var row db.InviteCode
	err = db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		row, err = queries.CreateInviteCode(ctx, db.CreateInviteCodeParams{
			PublicID: id.New(id.InviteCode), Code: code, CreatedBy: admin.ID,
		})
		if err != nil {
			return err
		}
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.InviteCodeCreated, ActorID: admin.ID, TargetID: row.PublicID,
		})
	})
	if err != nil {
		return nil, s.internalError(ctx, "create invite code", err)
	}
	return &inviteCodeOutput{Body: inviteCodeFromRow(row)}, nil
}

func (s *Service) deleteInviteCode(ctx context.Context, input *inviteCodeInput) (*struct{}, error) {
	var deleted int64
	err := db.InTx(ctx, s.pool, func(queries *db.Queries) error {
		var err error
		deleted, err = queries.DeleteInviteCode(ctx, input.ID)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return errInviteCodeNotFound
		}
		admin, _ := auth.UserFromContext(ctx)
		return audit.Record(ctx, queries, audit.Event{
			Action: audit.InviteCodeRevoked, ActorID: admin.ID, TargetID: input.ID,
		})
	})
	if errors.Is(err, errInviteCodeNotFound) {
		return nil, huma.Error404NotFound("The invite code does not exist or was already used.")
	}
	if err != nil {
		return nil, s.internalError(ctx, "delete invite code", err)
	}
	return &struct{}{}, nil
}

func inviteCodeFromRow(row db.InviteCode) InviteCode {
	code := InviteCode{ID: row.PublicID, Code: row.Code, CreatedAt: row.CreatedAt.Time}
	if row.UsedAt.Valid {
		usedAt := row.UsedAt.Time
		code.UsedAt = &usedAt
	}
	return code
}

func newInviteCode() (string, error) {
	code := make([]byte, inviteCodeLength)
	max := big.NewInt(int64(len(inviteCodeAlphabet)))
	for i := range code {
		value, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate invite code: %w", err)
		}
		code[i] = inviteCodeAlphabet[value.Int64()]
	}
	return string(code), nil
}
