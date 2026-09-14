package files

import (
	"context"

	"github.com/bmardale/stocat/internal/audit"
	"github.com/bmardale/stocat/internal/db"
)

// recordFileEvent adds the tag to the event when tag is not nil.
func recordFileEvent(ctx context.Context, queries *db.Queries, action audit.Action, nodeID, actorID int64, tag *db.Tag) error {
	event, err := audit.FileEvent(ctx, queries, action, nodeID)
	if err != nil {
		return err
	}
	event.ActorID = actorID
	if tag != nil {
		event.Details.TagID, event.Details.TagName = tag.PublicID, tag.Name.String
	}
	return audit.Record(ctx, queries, event)
}
