// Package activity serves audit events to users and administrators.
package activity

import (
	"bytes"
	"context"
	"encoding/csv"
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

const (
	pageSize           = 50
	maxAuditExportRows = 10000
)

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
	Cursor string    `query:"cursor" maxLength:"64"`
	Limit  int32     `query:"limit" minimum:"1" maximum:"200" default:"50"`
	Action string    `query:"action" maxLength:"64"`
	User   string    `query:"user" maxLength:"64" doc:"Return the events that the user did or that affect the user."`
	From   time.Time `query:"from" format:"date-time" doc:"Include events at or after this time."`
	To     time.Time `query:"to" format:"date-time" doc:"Exclude events at or after this time."`
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
		OperationID: "audit-events-export", Method: http.MethodGet, Path: "/audit-events/export",
		Summary: "Export audit events as CSV", Tags: []string{"Audit"},
		Errors: []int{http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.export)
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
	filter, err := s.auditFilter(ctx, input)
	if err != nil {
		return nil, err
	}
	page, err := s.list(ctx, input.Cursor, input.Limit, filter)
	if err != nil {
		return nil, err
	}
	return &pageOutput{Body: page}, nil
}

func (s *Service) export(ctx context.Context, input *adminListInput) (*huma.StreamResponse, error) {
	filter, err := s.auditFilter(ctx, input)
	if err != nil {
		return nil, err
	}
	page, err := s.list(ctx, "", maxAuditExportRows, filter)
	if err != nil {
		return nil, err
	}
	if page.NextCursor != "" {
		return nil, huma.Error422UnprocessableEntity("Narrow the filters before exporting more than 10,000 events.")
	}
	data, err := auditCSV(page.Items)
	if err != nil {
		return nil, s.internalError(ctx, "encode audit export", err)
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		stream.SetHeader("Cache-Control", "no-store")
		stream.SetHeader("Content-Type", "text/csv; charset=utf-8")
		stream.SetHeader("Content-Disposition", `attachment; filename="audit-log.csv"`)
		if _, err := stream.BodyWriter().Write(data); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "write audit export", "error", err)
		}
	}}, nil
}

func (s *Service) auditFilter(ctx context.Context, input *adminListInput) (db.ListAuditEventsParams, error) {
	if input.Action != "" && !audit.Valid(audit.Action(input.Action)) {
		return db.ListAuditEventsParams{}, huma.Error422UnprocessableEntity("Use a known audit action.")
	}
	if !input.From.IsZero() && !input.To.IsZero() && !input.To.After(input.From) {
		return db.ListAuditEventsParams{}, huma.Error422UnprocessableEntity("The end time must be after the start time.")
	}
	filter := db.ListAuditEventsParams{
		Action:   input.Action,
		FromTime: pgtype.Timestamptz{Time: input.From, Valid: !input.From.IsZero()},
		ToTime:   pgtype.Timestamptz{Time: input.To, Valid: !input.To.IsZero()},
	}
	if input.User == "" {
		return filter, nil
	}
	user, err := s.queries.GetUserByPublicID(ctx, input.User)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.ListAuditEventsParams{}, huma.Error404NotFound("The user does not exist.")
	}
	if err != nil {
		return db.ListAuditEventsParams{}, s.internalError(ctx, "load audit user filter", err)
	}
	filter.UserID = user.ID
	return filter, nil
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

func auditCSV(events []AuditEvent) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"created_at", "actor", "actor_email", "action", "subject", "subject_email",
		"target_id", "details", "ip_address", "user_agent", "request_id",
	}); err != nil {
		return nil, err
	}
	for _, event := range events {
		details, err := json.Marshal(event.Details)
		if err != nil {
			return nil, fmt.Errorf("encode details for audit event %s: %w", event.ID, err)
		}
		actorName, actorEmail := "", ""
		if event.Actor != nil {
			actorName, actorEmail = event.Actor.Name, event.Actor.Email
		}
		subjectName, subjectEmail := "", ""
		if event.Subject != nil {
			subjectName, subjectEmail = event.Subject.Name, event.Subject.Email
		}
		if err := writer.Write([]string{
			event.CreatedAt.Format(time.RFC3339Nano), actorName, actorEmail, string(event.Action),
			subjectName, subjectEmail, event.TargetID, string(details), event.IPAddress,
			event.UserAgent, event.RequestID,
		}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("The audit log is unavailable.")
}
