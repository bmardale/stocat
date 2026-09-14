package audit

import (
	"context"
	"fmt"

	"github.com/bmardale/stocat/internal/db"
)

const encryptionE2EE = "e2ee"

// FileEvent returns an event for the file with a snapshot of its name and library.
// The library owner is the subject. The caller sets the actor.
func FileEvent(ctx context.Context, queries *db.Queries, action Action, nodeID int64) (Event, error) {
	row, err := queries.GetFileAuditTarget(ctx, nodeID)
	if err != nil {
		return Event{}, fmt.Errorf("load audit target for file %d: %w", nodeID, err)
	}
	return Event{
		Action: action, SubjectID: row.OwnerID, TargetID: row.PublicID,
		Details: Details{
			Name: row.Name.String, Encrypted: row.EncryptionMode == encryptionE2EE,
			LibraryID: row.LibraryPublicID, LibraryName: row.LibraryName,
		},
	}, nil
}
