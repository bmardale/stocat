// Package activity serves audit events to users and administrators.
package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pageSize = 50

type Service struct {
	queries *db.Queries
	log     *slog.Logger
}

// action documents the known actions as an enum.
type action string

func (action) Schema(huma.Registry) *huma.Schema {
	known := audit.Actions()
	values := make([]any, 0, len(known))
	for _, value := range known {
		values = append(values, string(value))
	}
	return &huma.Schema{Type: huma.TypeString, Enum: values}
}

type AuditDetails audit.Details

type AuditUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type AuditEvent struct {
	ID        string       `json:"id"`
	Action    action       `json:"action"`
	ActorType string       `json:"actor_type" enum:"user,system"`
	Actor     *AuditUser   `json:"actor,omitempty" doc:"A user actor has no value after the user is deleted."`
	Subject   *AuditUser   `json:"subject,omitempty" doc:"The user whose account or data the action affects."`
	TargetID  string       `json:"target_id,omitempty"`
	Details   AuditDetails `json:"details"`
	IPAddress string       `json:"ip_address,omitempty"`
	UserAgent string       `json:"user_agent,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

type AuditEventPage struct {
	Items      []AuditEvent `json:"items" nullable:"false"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type listInput struct {
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type adminListInput struct {
	Cursor string `query:"cursor" maxLength:"64"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Action string `query:"action" maxLength:"64"`
	User   string `query:"user" maxLength:"64" doc:"Return the events that the user did or that affect the user."`
}

type pageOutput struct{ Body AuditEventPage }

func New(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{queries: db.New(pool), log: logger}
}

func (s *Service) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "activity-list", Method: http.MethodGet, Path: "/account-events",
		Summary: "List the activity of the current user", Tags: []string{"Activity"},
		Errors: []int{http.StatusUnprocessableEntity},
	}, s.listOwn)
}

// RegisterAdmin adds the audit log route. The caller must restrict api to administrators.
func (s *Service) RegisterAdmin(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "audit-events-list", Method: http.MethodGet, Path: "/audit-events",
		Summary: "List audit events of all users", Tags: []string{"Audit"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.listAll)
}

func (s *Service) listOwn(ctx context.Context, input *listInput) (*pageOutput, error) {
	user, _ := auth.UserFromContext(ctx)
	page, err := s.list(ctx, input.Cursor, input.Limit, db.ListAuditEventsParams{UserID: user.ID})
	if err != nil {
		return nil, err
	}
	// An administrator can change the account of the user. Do not show the address of the administrator.
	for i := range page.Items {
		event := &page.Items[i]
		if event.Actor != nil && event.Actor.ID != user.PublicID {
			event.Actor.Email, event.IPAddress, event.UserAgent, event.RequestID = "", "", "", ""
		}
	}
	return &pageOutput{Body: page}, nil
}

func (s *Service) listAll(ctx context.Context, input *adminListInput) (*pageOutput, error) {
	filter := db.ListAuditEventsParams{Action: input.Action}
	if input.Action != "" && !audit.Valid(audit.Action(input.Action)) {
		return nil, huma.Error422UnprocessableEntity("Use a known audit action.")
	}
	if input.User != "" {
		user, err := s.queries.GetUserByPublicID(ctx, input.User)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("The user does not exist.")
		}
		if err != nil {
			return nil, s.internalError(ctx, "load audit user filter", err)
		}
		filter.UserID = user.ID
	}
	page, err := s.list(ctx, input.Cursor, input.Limit, filter)
	if err != nil {
		return nil, err
	}
	return &pageOutput{Body: page}, nil
}

func (s *Service) list(ctx context.Context, cursor string, limit int32, filter db.ListAuditEventsParams) (AuditEventPage, error) {
	page := AuditEventPage{Items: []AuditEvent{}}
	if cursor != "" {
		beforeID, err := s.queries.GetAuditEventCursor(ctx, cursor)
		if errors.Is(err, pgx.ErrNoRows) {
			return page, huma.Error422UnprocessableEntity("The audit cursor is not valid.")
		}
		if err != nil {
			return page, s.internalError(ctx, "load audit cursor", err)
		}
		filter.BeforeID = beforeID
	}
	if limit == 0 {
		limit = pageSize
	}
	filter.PageLimit = limit + 1
	rows, err := s.queries.ListAuditEvents(ctx, filter)
	if err != nil {
		return page, s.internalError(ctx, "list audit events", err)
	}
	if len(rows) > int(limit) {
		page.NextCursor = rows[limit-1].PublicID
		rows = rows[:limit]
	}
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return page, s.internalError(ctx, "decode audit event", err)
		}
		page.Items = append(page.Items, event)
	}
	return page, nil
}

func eventFromRow(row db.ListAuditEventsRow) (AuditEvent, error) {
	event := AuditEvent{
		ID: row.PublicID, Action: action(row.Action), ActorType: row.ActorType, TargetID: row.TargetID,
		Actor:     auditUser(row.ActorPublicID, row.ActorName, row.ActorEmail),
		Subject:   auditUser(row.SubjectPublicID, row.SubjectName, row.SubjectEmail),
		UserAgent: row.UserAgent, RequestID: row.RequestID, CreatedAt: row.CreatedAt.Time,
	}
	if row.IpAddress != nil {
		event.IPAddress = row.IpAddress.String()
	}
	if err := json.Unmarshal(row.Details, &event.Details); err != nil {
		return event, fmt.Errorf("decode details of audit event %s: %w", row.PublicID, err)
	}
	return event, nil
}

func auditUser(id, name, email pgtype.Text) *AuditUser {
	if !id.Valid {
		return nil
	}
	return &AuditUser{ID: id.String, Name: name.String, Email: email.String}
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("The audit log is unavailable.")
}
