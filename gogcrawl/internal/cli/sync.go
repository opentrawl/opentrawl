package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

type syncProgressEvent struct {
	Event string `json:"event"`
	Stage string `json:"stage"`
	Done  int    `json:"done"`
}

type syncCompleteEvent struct {
	Event       string `json:"event"`
	Stage       string `json:"stage"`
	Done        int    `json:"done"`
	Inserted    int    `json:"inserted"`
	ArchivePath string `json:"archive_path"`
	SyncedAt    string `json:"synced_at"`
}

func (r *runtime) runSync(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"sync"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("sync takes no arguments"))
	}
	st, err := archive.Open(r.ctx, r.archivePath)
	if err != nil {
		return commandErr("archive_open_failed", "cannot open the archive database", "check the --archive path", err)
	}
	defer func() { _ = st.Close() }()
	startedAt := time.Now().UTC()
	markers, err := st.SyncMarkers(r.ctx)
	if err != nil {
		return err
	}
	existing, err := st.CountMessages(r.ctx)
	if err != nil {
		return err
	}
	if err := st.MarkSyncStarted(r.ctx, startedAt); err != nil {
		return err
	}
	allowExistingPageStop := markers.HasCompleted && !markers.PreviousRunIncomplete && existing > 0
	done, inserted, err := r.syncMessages(st, allowExistingPageStop)
	if err != nil {
		return err
	}
	completedAt := time.Now().UTC()
	if err := st.MarkSyncCompleted(r.ctx, completedAt); err != nil {
		return err
	}
	event := syncCompleteEvent{
		Event:       "complete",
		Stage:       "messages",
		Done:        done,
		Inserted:    inserted,
		ArchivePath: st.Path(),
		SyncedAt:    completedAt.Local().Format(time.RFC3339),
	}
	if r.json {
		return json.NewEncoder(r.stdout).Encode(event)
	}
	return printSyncText(r.stdout, event)
}

func (r *runtime) syncMessages(st *archive.Store, allowExistingPageStop bool) (int, int, error) {
	pageToken := ""
	done := 0
	inserted := 0
	for {
		page, err := r.gog.SearchMessages(r.ctx, gog.SearchRequest{
			Query:     gog.DefaultArchiveQuery,
			Max:       gog.DefaultPageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return done, inserted, commandErr("gog_search_failed", fmt.Sprintf("Gmail page fetch failed: %v", err), "run gogcrawl doctor", err)
		}
		result, err := st.InsertMessages(r.ctx, archiveMessages(page.Messages))
		if err != nil {
			return done, inserted, err
		}
		done += result.Seen
		inserted += result.Inserted
		if err := r.progress(done); err != nil {
			return done, inserted, err
		}
		if allowExistingPageStop && result.Seen > 0 && result.Inserted == 0 {
			return done, inserted, nil
		}
		if page.NextPageToken == "" {
			return done, inserted, nil
		}
		pageToken = page.NextPageToken
	}
}

func (r *runtime) progress(done int) error {
	if r.json {
		return json.NewEncoder(r.stdout).Encode(syncProgressEvent{Event: "progress", Stage: "messages", Done: done})
	}
	_, err := fmt.Fprintf(r.stderr, "gogcrawl: synced %d messages\n", done)
	return err
}

func archiveMessages(messages []gog.Message) []archive.Message {
	out := make([]archive.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, archive.Message{
			ID:          msg.ID,
			ThreadID:    msg.ThreadID,
			Time:        msg.Time,
			FromName:    msg.FromName,
			FromAddress: msg.FromAddress,
			Subject:     msg.Subject,
			Body:        msg.Body,
			Labels:      msg.Labels,
		})
	}
	return out
}

func printSyncText(w io.Writer, event syncCompleteEvent) error {
	_, err := fmt.Fprintf(w, "Sync complete\n\nLocal archive:\n  Database: %s\n  Synced: %s\n\nMessages:\n  Seen: %d\n  New: %d\n", event.ArchivePath, event.SyncedAt, event.Done, event.Inserted)
	return err
}
