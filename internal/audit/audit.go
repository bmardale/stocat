// Package audit records important user and administrator actions in the audit_events table.
//
// Record an event in the transaction that makes the change. Then a rolled-back change has no event,
// and a committed change always has one. To add an action, declare it with newAction in this file.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/platform/id"
	"github.com/bmardale/stocat/internal/platform/o11y"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	MethodPassword = "password"
	MethodPasskey  = "passkey"
)

type Action string

var actions []Action

func newAction(name string) Action {
	action := Action(name)
	actions = append(actions, action)
	return action
}

// Actions returns all actions in declaration order.
func Actions() []Action { return append([]Action(nil), actions...) }

func Valid(action Action) bool {
	for _, known := range actions {
		if known == action {
			return true
		}
	}
	return false
}

var (
	AccountRegistered      = newAction("account.registered")
	AccountSignedIn        = newAction("account.signed_in")
	AccountSignedOut       = newAction("account.signed_out")
	AccountUpdated         = newAction("account.updated")
	AccountPasswordChanged = newAction("account.password_changed")
	AccountAdminGranted    = newAction("account.admin_granted")
	AccountAdminRevoked    = newAction("account.admin_revoked")
	SessionRevoked         = newAction("session.revoked")
	SessionOthersRevoked   = newAction("session.others_revoked")
	PasskeyAdded           = newAction("passkey.added")
	PasskeyRenamed         = newAction("passkey.renamed")
	PasskeyDeleted         = newAction("passkey.deleted")
	LibraryCreated         = newAction("library.created")
	FolderCreated          = newAction("folder.created")
	FileUploaded           = newAction("file.uploaded")
	FileReplaced           = newAction("file.replaced")
	FileRenamed            = newAction("file.renamed")
	FileTrashed            = newAction("file.trashed")
	FileRestored           = newAction("file.restored")
	FileDeleted            = newAction("file.deleted")
	FileTagged             = newAction("file.tagged")
	FileUntagged           = newAction("file.untagged")
	TagCreated             = newAction("tag.created")
	TagUpdated             = newAction("tag.updated")
	TagDeleted             = newAction("tag.deleted")
	ReplicationCreated     = newAction("replication.created")
	ReplicationSynced      = newAction("replication.sync_requested")
	ReplicationDeleted     = newAction("replication.deleted")
	StorageBackendCreated  = newAction("storage_backend.created")
	StorageBackendUpdated  = newAction("storage_backend.updated")
	StorageBackendDeleted  = newAction("storage_backend.deleted")
	UserQuotaUpdated       = newAction("quota.user_updated")
	DefaultQuotaUpdated    = newAction("quota.default_updated")
)

// Details holds a snapshot of the target, because the target can change or disappear later.
// Do not put names from an encrypted library here. Set Encrypted instead.
type Details struct {
	Name                   string `json:"name,omitempty"`
	PreviousName           string `json:"previous_name,omitempty"`
	Email                  string `json:"email,omitempty"`
	PreviousEmail          string `json:"previous_email,omitempty"`
	Method                 string `json:"method,omitempty" enum:"password,passkey"`
	Encrypted              bool   `json:"encrypted,omitempty" doc:"The library encrypts names, so the event contains no file or tag names."`
	LibraryID              string `json:"library_id,omitempty"`
	LibraryName            string `json:"library_name,omitempty"`
	DestinationLibraryID   string `json:"destination_library_id,omitempty"`
	DestinationLibraryName string `json:"destination_library_name,omitempty"`
	TagID                  string `json:"tag_id,omitempty"`
	TagName                string `json:"tag_name,omitempty"`
	SizeBytes              int64  `json:"size_bytes,omitempty"`
	QuotaMode              string `json:"quota_mode,omitempty" enum:"inherit,unlimited,limited"`
	QuotaLimitBytes        *int64 `json:"quota_limit_bytes,omitempty"`
	BackendQuotaCount      int    `json:"backend_quota_count,omitempty"`
	BackendType            string `json:"backend_type,omitempty" enum:"local,s3"`
	BackendEnabled         *bool  `json:"backend_enabled,omitempty"`
}

type Event struct {
	Action Action
	// ActorID is the user who did the action. Zero means the system.
	ActorID int64
	// SubjectID is the user whose account or data the action affects. Zero means the actor.
	SubjectID int64
	// TargetID is the public identifier of the changed object.
	TargetID string
	Details  Details
}

// Record adds the event with the request metadata in ctx.
func Record(ctx context.Context, queries *db.Queries, event Event) error {
	details, err := json.Marshal(event.Details)
	if err != nil {
		return fmt.Errorf("encode audit details for %s: %w", event.Action, err)
	}
	actorType := "user"
	if event.ActorID == 0 {
		actorType = "system"
	}
	subjectID := event.SubjectID
	if subjectID == 0 {
		subjectID = event.ActorID
	}
	meta := requestFrom(ctx)
	err = queries.CreateAuditEvent(ctx, db.CreateAuditEventParams{
		PublicID: id.New(id.AuditEvent), Action: string(event.Action), ActorType: actorType,
		ActorID: optionalID(event.ActorID), SubjectID: optionalID(subjectID), TargetID: event.TargetID,
		Details: details, IpAddress: meta.ip, UserAgent: meta.userAgent, RequestID: o11y.RequestIDFrom(ctx),
	})
	if err != nil {
		return fmt.Errorf("record audit event %s: %w", event.Action, err)
	}
	return nil
}

func optionalID(value int64) pgtype.Int8 {
	return pgtype.Int8{Int64: value, Valid: value != 0}
}
