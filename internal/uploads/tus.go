package uploads

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

func (s *Service) registerTus(api huma.API) {
	for _, route := range []struct {
		method  string
		handler func(huma.Context)
	}{
		{http.MethodHead, s.tusHead},
		{http.MethodPatch, s.tusPatch},
	} {
		op := &huma.Operation{
			OperationID: "tus-" + route.method, Method: route.method, Path: "/{id}/content",
			Hidden: true, Responses: map[string]*huma.Response{},
		}
		api.Adapter().Handle(op, api.Middlewares().Handler(op.Middlewares.Handler(route.handler)))
	}
}

func (s *Service) tusHead(ctx huma.Context) {
	if !s.validTusVersion(ctx) {
		return
	}
	session, ok := s.authorizedSession(ctx)
	if !ok {
		return
	}
	ctx.SetHeader("Tus-Resumable", tusVersion)
	ctx.SetHeader("Upload-Offset", strconv.FormatInt(session.UploadOffset, 10))
	ctx.SetHeader("Upload-Length", strconv.FormatInt(session.DeclaredSize, 10))
	ctx.SetHeader("Upload-Expires", session.ExpiresAt.Time.UTC().Format(http.TimeFormat))
	ctx.SetHeader("Cache-Control", "no-store")
	ctx.SetStatus(http.StatusNoContent)
}

func (s *Service) tusPatch(ctx huma.Context) {
	if !s.validTusVersion(ctx) {
		return
	}
	if err := ctx.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.writeTusError(ctx, http.StatusInternalServerError, "The server cannot extend the upload deadline.")
		return
	}
	// The server write timeout starts before the body is read, so a slow chunk cannot write its response.
	if w, ok := ctx.BodyWriter().(http.ResponseWriter); ok {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			s.writeTusError(ctx, http.StatusInternalServerError, "The server cannot extend the upload deadline.")
			return
		}
	}
	if ctx.Header("Content-Type") != "application/offset+octet-stream" {
		s.writeTusError(ctx, http.StatusUnsupportedMediaType, "Use application/offset+octet-stream.")
		return
	}
	session, ok := s.authorizedSession(ctx)
	if !ok {
		return
	}
	unlock := s.sessionLock(session.ID)
	defer unlock()
	user, _ := auth.UserFromContext(ctx.Context())
	session, err := s.queries.GetUploadSessionByPublicIDAndOwner(ctx.Context(), db.GetUploadSessionByPublicIDAndOwnerParams{
		PublicID: session.PublicID, OwnerID: user.ID,
	})
	if err != nil {
		s.writeTusError(ctx, http.StatusServiceUnavailable, "Uploads are unavailable.")
		return
	}
	offset, err := parseOffset(ctx.Header("Upload-Offset"))
	if err != nil || offset != session.UploadOffset {
		ctx.SetHeader("Upload-Offset", strconv.FormatInt(session.UploadOffset, 10))
		s.writeTusError(ctx, http.StatusConflict, "The upload offset does not match.")
		return
	}
	length, err := strconv.ParseInt(ctx.Header("Content-Length"), 10, 64)
	if err != nil || length < 0 {
		s.writeTusError(ctx, http.StatusLengthRequired, "Send a valid Content-Length header.")
		return
	}
	if session.State != stateCreated && session.State != stateUploading {
		s.writeTusError(ctx, http.StatusConflict, "The upload does not accept more bytes.")
		return
	}
	if time.Now().After(session.ExpiresAt.Time) {
		s.writeTusError(ctx, http.StatusGone, "The upload session expired.")
		return
	}
	if length > session.DeclaredSize-offset {
		s.writeTusError(ctx, http.StatusRequestEntityTooLarge, "The patch exceeds the declared upload size.")
		return
	}
	file, err := s.staging.OpenFile(session.StagingKey, os.O_RDWR, 0)
	if err != nil {
		s.writeTusError(ctx, http.StatusServiceUnavailable, "Upload staging is unavailable.")
		return
	}
	writeErr := s.writePatch(file, ctx.BodyReader(), offset, length)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		s.log.WarnContext(ctx.Context(), "write upload patch", "upload_id", session.PublicID, "error", err)
		s.writeTusError(ctx, http.StatusBadRequest, "The upload patch is incomplete.")
		return
	}
	nextOffset := offset + length
	session, err = s.queries.AdvanceUploadOffset(ctx.Context(), db.AdvanceUploadOffsetParams{
		ID: session.ID, CurrentOffset: offset, NextOffset: nextOffset,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeTusError(ctx, http.StatusConflict, "The upload offset changed.")
		return
	}
	if err != nil {
		s.writeTusError(ctx, http.StatusServiceUnavailable, "Uploads are unavailable.")
		return
	}
	if session.State == stateUploaded {
		publication, loadErr := s.queries.GetUploadPublication(ctx.Context(), session.ID)
		if loadErr != nil {
			s.writeTusError(ctx, http.StatusServiceUnavailable, "Uploads are unavailable.")
			return
		}
		if publication.EncryptionMode == EncryptionNone {
			err = s.startFinalization(ctx.Context(), session.ID, nil)
			if err != nil {
				s.writeTusError(ctx, http.StatusServiceUnavailable, "The publication job is unavailable.")
				return
			}
			session.State = stateFinalizing
		}
	}
	ctx.SetHeader("Tus-Resumable", tusVersion)
	ctx.SetHeader("Upload-Offset", strconv.FormatInt(session.UploadOffset, 10))
	ctx.SetStatus(http.StatusNoContent)
}

func (s *Service) writePatch(file *os.File, body io.Reader, offset, length int64) error {
	if err := file.Truncate(offset); err != nil {
		return fmt.Errorf("truncate staging file: %w", err)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return fmt.Errorf("seek staging file: %w", err)
	}
	if err := copyPatch(file, body, length); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync staging file: %w", err)
	}
	return nil
}

func (s *Service) authorizedSession(ctx huma.Context) (db.UploadSession, bool) {
	user, _ := auth.UserFromContext(ctx.Context())
	row, err := s.queries.GetUploadSessionByPublicIDAndOwner(ctx.Context(), db.GetUploadSessionByPublicIDAndOwnerParams{
		PublicID: ctx.Param("id"), OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		s.writeTusError(ctx, http.StatusNotFound, "The upload session does not exist.")
		return db.UploadSession{}, false
	}
	if err != nil {
		s.writeTusError(ctx, http.StatusServiceUnavailable, "Uploads are unavailable.")
		return db.UploadSession{}, false
	}
	return row, true
}

func (s *Service) validTusVersion(ctx huma.Context) bool {
	if ctx.Header("Tus-Resumable") == tusVersion {
		return true
	}
	ctx.SetHeader("Tus-Version", tusVersion)
	s.writeTusError(ctx, http.StatusPreconditionFailed, "Use Tus-Resumable 1.0.0.")
	return false
}

func (s *Service) writeTusError(ctx huma.Context, status int, message string) {
	ctx.SetHeader("Tus-Resumable", tusVersion)
	if err := huma.WriteErr(s.api, ctx, status, message); err != nil {
		s.log.ErrorContext(ctx.Context(), "write tus error", "error", err)
	}
}
