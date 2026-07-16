package telecrawl

import (
	"encoding/json"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (r *runtime) print(v any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case statusEnvelope:
		return r.printStatus(value)
	case store.ImportStats:
		if _, err := fmt.Fprintf(r.stdout, "source_path: %s\ndb_path: %s\nchats: %s\nmessages: %s\nmedia_messages: %s\nmedia_files: %s\nmedia_bytes: %s\nstarted_at: %s\nfinished_at: %s\n",
			value.SourcePath,
			value.DBPath,
			render.FormatInteger(int64(value.Chats)),
			render.FormatInteger(int64(value.Messages)),
			render.FormatInteger(int64(value.MediaMessages)),
			render.FormatInteger(int64(value.MediaFiles)),
			render.FormatInteger(value.MediaBytes),
			shortLocalTime(value.StartedAt),
			shortLocalTime(value.FinishedAt)); err != nil {
			return err
		}
		if hasRemoteMediaStats(value) {
			if _, err := fmt.Fprintf(
				r.stdout,
				"remote_media_candidates: %s\nremote_media_attempted: %s\nremote_media_downloads: %s\nremote_media_missing: %s\nremote_media_unavailable: %s\nremote_media_timeouts: %s\nremote_media_errors: %s\n",
				render.FormatInteger(int64(value.RemoteMediaCandidates)),
				render.FormatInteger(int64(value.RemoteMediaAttempted)),
				render.FormatInteger(int64(value.RemoteMediaDownloads)),
				render.FormatInteger(int64(value.RemoteMediaMissing)),
				render.FormatInteger(int64(value.RemoteMediaUnavailable)),
				render.FormatInteger(int64(value.RemoteMediaTimeouts)),
				render.FormatInteger(int64(value.RemoteMediaErrors)),
			); err != nil {
				return err
			}
		}
		return nil
	case topicsEnvelope:
		return r.printTopics(value)
	case messagesEnvelope:
		return r.printMessages(value)
	case contactsEnvelope:
		return r.printContacts(value)
	case foldersEnvelope:
		return r.printFolders(value)
	case searchEnvelope:
		return r.printSearch(value)
	case whoEnvelope:
		return r.printWho(value)
	case openEnvelope:
		return r.printOpen(value)
	case contactExport:
		return r.printContactExport(value)
	default:
		return fmt.Errorf("internal: no human renderer for %T", v)
	}
}

func hasRemoteMediaStats(stats store.ImportStats) bool {
	return stats.RemoteMediaCandidates != 0 ||
		stats.RemoteMediaAttempted != 0 ||
		stats.RemoteMediaDownloads != 0 ||
		stats.RemoteMediaMissing != 0 ||
		stats.RemoteMediaUnavailable != 0 ||
		stats.RemoteMediaTimeouts != 0 ||
		stats.RemoteMediaErrors != 0
}
