package files

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"github.com/bmardale/stocat/internal/auth"
	"github.com/bmardale/stocat/internal/db"
	"github.com/bmardale/stocat/internal/encryption"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

type EncryptedFileDetails struct {
	ID             string                  `json:"id"`
	LibraryID      string                  `json:"library_id"`
	VersionID      string                  `json:"version_id"`
	Revision       string                  `json:"revision"`
	Size           int64                   `json:"size" doc:"Plaintext size in bytes."`
	StoredSize     int64                   `json:"stored_size" doc:"Ciphertext size in bytes."`
	FileKey        string                  `json:"file_key"`
	Manifest       encryption.SignedRecord `json:"manifest"`
	CurrentVersion encryption.SignedRecord `json:"current_version"`
	ContentURL     string                  `json:"content_url"`
}

type encryptedFileInput struct {
	ID string `path:"id" maxLength:"64"`
}

type encryptedContentInput struct {
	ID    string `path:"id" maxLength:"64"`
	Range string `header:"Range"`
}

type encryptedDetailsOutput struct{ Body EncryptedFileDetails }

func (s *Service) RegisterV2(api huma.API) {
	group := huma.NewGroup(api, "/files")
	group.UseSimpleModifier(func(op *huma.Operation) { op.Tags = []string{"Encrypted files"} })
	s.registerV2Mutations(group)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-files-get", Method: http.MethodGet, Path: "/{id}",
		Summary: "Get the current version records of an encrypted file", Errors: []int{http.StatusNotFound},
	}, s.getV2)
	huma.Register(group, huma.Operation{
		OperationID: "encrypted-files-content", Method: http.MethodGet, Path: "/{id}/content",
		Summary:     "Stream the ciphertext of an encrypted file",
		Description: "Verify the manifest and every covering frame before you use the plaintext.",
		Errors:      []int{http.StatusNotFound, http.StatusRequestedRangeNotSatisfiable, http.StatusServiceUnavailable},
		Responses: map[string]*huma.Response{
			"206": {Description: "Partial content"},
		},
	}, s.contentV2)
}

func (s *Service) getV2(ctx context.Context, input *encryptedFileInput) (*encryptedDetailsOutput, error) {
	row, err := s.loadV2(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return &encryptedDetailsOutput{Body: EncryptedFileDetails{
		ID: row.NodePublicID, LibraryID: row.LibraryPublicID, VersionID: row.VersionPublicID,
		Revision: strconv.FormatInt(row.Revision, 10), Size: row.SizeBytes, StoredSize: row.StoredSizeBytes,
		FileKey:        base64.RawURLEncoding.EncodeToString(row.EncryptedContentKey),
		Manifest:       encryption.Encode(row.SignedManifest, row.ManifestSignature),
		CurrentVersion: encryption.Encode(row.CurrentVersionPointer, row.CurrentVersionSignature),
		ContentURL:     "/api/v2/files/" + row.NodePublicID + "/content",
	}}, nil
}

// contentV2 always sends an opaque attachment, because the server cannot read the name or media type.
func (s *Service) contentV2(ctx context.Context, input *encryptedContentInput) (*huma.StreamResponse, error) {
	row, err := s.loadV2(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return s.stream(ctx, streamSource{
		backendPublicID: row.BackendPublicID, objectKey: row.ObjectKey, storedSize: row.StoredSizeBytes,
		checksum: row.CiphertextSha256, filename: "download.bin", contentType: "application/octet-stream",
		disposition: "attachment",
	}, input.Range)
}

func (s *Service) loadV2(ctx context.Context, publicID string) (db.GetV2CurrentFileByPublicIDAndOwnerRow, error) {
	user, _ := auth.UserFromContext(ctx)
	row, err := s.queries.GetV2CurrentFileByPublicIDAndOwner(ctx, db.GetV2CurrentFileByPublicIDAndOwnerParams{
		NodePublicID: publicID, OwnerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, fileNotFound()
	}
	if err != nil {
		return row, s.internalError(ctx, "load encrypted file", err)
	}
	return row, nil
}
