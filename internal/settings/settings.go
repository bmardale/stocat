package settings

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bmardale/stocat/internal/db"
)

const registrationInviteOnlyKey = "registration_invite_only"

// RegistrationInviteOnly returns whether new accounts require an invite code.
func RegistrationInviteOnly(ctx context.Context, queries *db.Queries) (bool, error) {
	value, err := queries.GetSetting(ctx, registrationInviteOnlyKey)
	if err != nil {
		return false, fmt.Errorf("load registration setting: %w", err)
	}
	inviteOnly, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse registration setting: %w", err)
	}
	return inviteOnly, nil
}

// SetRegistrationInviteOnly stores whether new accounts require an invite code.
func SetRegistrationInviteOnly(ctx context.Context, queries *db.Queries, inviteOnly bool) error {
	_, err := queries.SetSetting(ctx, db.SetSettingParams{
		Key: registrationInviteOnlyKey, Value: strconv.FormatBool(inviteOnly),
	})
	if err != nil {
		return fmt.Errorf("store registration setting: %w", err)
	}
	return nil
}
