// Package files serves file metadata and content.
package files

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/storage"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const writeIdleTimeout = 30 * time.Second

// Only these types show inline. An HTML or SVG file must not render as a document of the application.
var inlineTypes = map[string]bool{
	"application/pdf": true,
	"text/plain":      true,
	"image/avif":      true,
	"image/bmp":       true,
	"image/gif":       true,
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"audio/aac":       true,
	"audio/flac":      true,
	"audio/mp4":       true,
	"audio/mpeg":      true,
	"audio/ogg":       true,
	"audio/wav":       true,
	"audio/x-wav":     true,
	"video/mp4":       true,
	"video/ogg":       true,
	"video/quicktime": true,
	"video/webm":      true,
}

type FileDetails struct {
	ID               string `json:"id"`
	LibraryID        string `json:"library_id"`
	VersionID        string `json:"version_id"`
	Revision         int64  `json:"revision"`
	Size             int64  `json:"size"`
	StoredSize       int64  `json:"stored_size"`
	EncryptionFormat string `json:"encryption_format,omitempty"`
	EncryptedFileKey []byte `json:"encrypted_file_key,omitempty"`
	ContentURL       string `json:"content_url"`
}

type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	stores  *storage.Service
	queue   *river.Client[pgx.Tx]
	log     *slog.Logger
}

type fileInput struct {
	ID string `path:"id" maxLength:"64"`
}

type contentInput struct {
	ID          string `path:"id" maxLength:"64"`
	Range       string `header:"Range"`
	Disposition string `query:"disposition" enum:"inline,attachment" default:"attachment"`
}

type detailsOutput struct{ Body FileDetails }

func New(pool *pgxpool.Pool, stores *storage.Service, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), stores: stores, log: logger}
}

func (s *Service) Register(api huma.API) {
	group := huma.NewGroup(api, "/files")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Files"} })
	huma.Register(group, huma.Operation{
		OperationID: "files-trash-list", Method: http.MethodGet, Path: "/trash", Summary: "List trashed files",
		Errors: []int{http.StatusUnprocessableEntity},
	}, s.listTrash)
	huma.Register(group, huma.Operation{
		OperationID: "files-get", Method: http.MethodGet, Path: "/{id}", Summary: "Get file metadata",
		Errors: []int{http.StatusNotFound},
	}, s.get)
	huma.Register(group, huma.Operation{
		OperationID: "files-rename", Method: http.MethodPatch, Path: "/{id}", Summary: "Rename a file",
		MaxBodyBytes: 65536, Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusUnprocessableEntity},
	}, s.rename)
	huma.Register(group, huma.Operation{
		OperationID: "files-delete", Method: http.MethodDelete, Path: "/{id}", Summary: "Move a file to trash",
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusNotFound},
	}, s.remove)
	huma.Register(group, huma.Operation{
		OperationID: "files-restore", Method: http.MethodPost, Path: "/{id}/restore", Summary: "Restore a trashed file",
		Errors: []int{http.StatusConflict, http.StatusNotFound},
	}, s.restore)
	huma.Register(group, huma.Operation{
		OperationID: "files-delete-permanently", Method: http.MethodDelete, Path: "/{id}/permanent",
		Summary: "Permanently delete a trashed file", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusConflict, http.StatusNotFound, http.StatusServiceUnavailable},
	}, s.deletePermanently)
	huma.Register(group, huma.Operation{
		OperationID: "files-content", Method: http.MethodGet, Path: "/{id}/content", Summary: "Stream file content",
		Errors: []int{http.StatusNotFound, http.StatusRequestedRangeNotSatisfiable, http.StatusServiceUnavailable},
		Responses: map[string]*huma.Response{
			"206": {Description: "Partial content"},
		},
	}, s.content)
	s.registerTags(group)
}

func (s *Service) get(ctx context.Context, input *fileInput) (*detailsOutput, error) {
	row, err := s.load(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return &detailsOutput{Body: detailsFromRow(row)}, nil
}

func (s *Service) content(ctx context.Context, input *contentInput) (*huma.StreamResponse, error) {
	row, err := s.load(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	byteRange, err := parseRange(input.Range, row.StoredSizeBytes)
	if err != nil {
		return nil, huma.ErrorWithHeaders(
			huma.Error416RequestedRangeNotSatisfiable("The byte range is not valid for this file."),
			http.Header{"Content-Range": {fmt.Sprintf("bytes */%d", row.StoredSizeBytes)}},
		)
	}
	store, err := s.stores.ObjectStore(ctx, row.BackendPublicID)
	if err != nil {
		return nil, s.storageError(ctx, "open file backend", err)
	}
	object, err := store.Open(ctx, row.ObjectKey, byteRange)
	if err != nil {
		_ = store.Close()
		return nil, s.storageError(ctx, "open file content", err)
	}
	name, contentType, disposition := presentation(row.Name, input.Disposition)
	status := http.StatusOK
	if byteRange != nil {
		status = http.StatusPartialContent
	}
	return &huma.StreamResponse{Body: func(stream huma.Context) {
		defer func() {
			if closeErr := errors.Join(object.Body.Close(), store.Close()); closeErr != nil {
				s.log.WarnContext(ctx, "close file content", "error", closeErr)
			}
		}()
		stream.SetHeader("Accept-Ranges", "bytes")
		stream.SetHeader("Cache-Control", "private, no-cache")
		stream.SetHeader("Content-Type", contentType)
		stream.SetHeader("Content-Length", strconv.FormatInt(object.Size, 10))
		stream.SetHeader("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
		stream.SetHeader("ETag", `"`+hex.EncodeToString(row.CiphertextSha256)+`"`)
		stream.SetHeader("X-Content-Type-Options", "nosniff")
		// Chromium does not render a PDF in a sandboxed document.
		if mediaType(contentType) != "application/pdf" {
			stream.SetHeader("Content-Security-Policy", "sandbox")
		}
		if byteRange != nil {
			stream.SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/%d", byteRange.Offset, byteRange.Offset+object.Size-1, object.ObjectSize))
		}
		stream.SetStatus(status)
		if _, copyErr := io.Copy(newDeadlineWriter(stream.BodyWriter()), object.Body); copyErr != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "stream file content", "error", copyErr)
		}
	}}, nil
}

func (s *Service) load(ctx context.Context, publicID string) (db.GetCurrentFileByPublicIDAndOwnerRow, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetCurrentFileByPublicIDAndOwner(ctx, db.GetCurrentFileByPublicIDAndOwnerParams{
		NodePublicID: publicID,
		OwnerID:      user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, fileNotFound()
	}
	if err != nil {
		return row, s.internalError(ctx, "load file", err)
	}
	return row, nil
}

func detailsFromRow(row db.GetCurrentFileByPublicIDAndOwnerRow) FileDetails {
	details := FileDetails{
		ID: row.NodePublicID, LibraryID: row.LibraryPublicID, VersionID: row.VersionPublicID,
		Revision: row.Revision, Size: row.SizeBytes, StoredSize: row.StoredSizeBytes,
		EncryptedFileKey: row.EncryptedFileKey, ContentURL: "/api/v1/files/" + row.NodePublicID + "/content",
	}
	if row.EncryptionFormat.Valid {
		details.EncryptionFormat = row.EncryptionFormat.String
	}
	return details
}

// An encrypted library has no plaintext name, so its content is always an opaque attachment.
func presentation(name pgtype.Text, requested string) (filename, contentType, disposition string) {
	filename, contentType, disposition = "download.bin", "application/octet-stream", "attachment"
	if name.Valid {
		filename = name.String
		if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); detected != "" {
			contentType = detected
		}
	}
	if requested == "inline" && inlineTypes[mediaType(contentType)] {
		disposition = "inline"
	}
	return filename, contentType, disposition
}

func mediaType(contentType string) string {
	value, _, _ := mime.ParseMediaType(contentType)
	return value
}

func fileNotFound() error { return huma.Error404NotFound("The file does not exist.") }

func (s *Service) internalError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	return huma.Error500InternalServerError("File storage is unavailable.")
}

func (s *Service) storageError(ctx context.Context, operation string, err error) error {
	s.log.ErrorContext(ctx, operation, "error", err)
	if errors.Is(err, storage.ErrMissing) {
		return huma.Error503ServiceUnavailable("The stored file is temporarily unavailable.")
	}
	return huma.Error503ServiceUnavailable("File storage is temporarily unavailable.")
}

// deadlineWriter moves the write deadline forward before each write.
// A slow client can receive a large file, but a stalled client releases the connection.
type deadlineWriter struct {
	w          io.Writer
	controller *http.ResponseController
}

func newDeadlineWriter(w io.Writer) io.Writer {
	rw, ok := w.(http.ResponseWriter)
	if !ok {
		return w
	}
	return deadlineWriter{w: w, controller: http.NewResponseController(rw)}
}

func (d deadlineWriter) Write(p []byte) (int, error) {
	if err := d.controller.SetWriteDeadline(time.Now().Add(writeIdleTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, fmt.Errorf("extend write deadline: %w", err)
	}
	return d.w.Write(p)
}

func parseRange(value string, size int64) (*storage.ByteRange, error) {
	if value == "" {
		return nil, nil
	}
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return nil, errors.New("invalid byte range")
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 {
		return nil, errors.New("invalid byte range")
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return nil, errors.New("invalid byte range")
		}
		suffix = min(suffix, size)
		return &storage.ByteRange{Offset: size - suffix, Length: suffix}, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return nil, errors.New("invalid byte range")
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return nil, errors.New("invalid byte range")
		}
		end = min(end, size-1)
	}
	return &storage.ByteRange{Offset: start, Length: end - start + 1}, nil
}
