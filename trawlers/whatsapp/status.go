package wacrawl

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus("whatsapp", "Archive has not been synced.")
	status.State = "missing"
	status.ConfigPath = req.Paths.Config
	status.DatabasePath = req.Paths.Archive
	if req.Store == nil {
		return &status, nil
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.DBPath
	status.LastSyncAt = contractTime(archiveStatus.LastImportAt)
	status.Counts = statusCounts(archiveStatus)
	switch archiveStatus.Messages {
	case 0:
		status.State = "empty"
		if archiveStatus.LastImportAt.IsZero() {
			status.Summary = "Archive is empty; run trawl sync whatsapp to populate it."
		} else {
			status.Summary = "Archive contains no messages from the last sync."
		}
	default:
		status.State = "ok"
		status.Summary = "Recently synced."
	}
	return &status, nil
}

func statusCounts(status store.Status) []control.Count {
	counts := []control.Count{
		control.NewCount("messages", "messages", int64(status.Messages)),
		control.NewCount("media_messages", "media messages", int64(status.MediaMessages)),
		control.NewCount("chats", "chats", int64(status.Chats)),
		control.NewCount("unread_chats", "unread chats", int64(status.UnreadChats)),
		control.NewCount("unread_messages", "unread messages", int64(status.UnreadMessages)),
		control.NewCount("contacts", "contacts", int64(status.Contacts)),
		control.NewCount("groups", "groups", int64(status.Groups)),
		control.NewCount("participants", "participants", int64(status.Participants)),
	}
	if !status.OldestMessage.IsZero() {
		counts = append(counts, control.NewCount("since", "since", int64(status.OldestMessage.In(time.Local).Year())))
	}
	return counts
}

func contractTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
