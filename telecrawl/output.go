package telecrawl

import (
	"encoding/json"
	"fmt"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/telecrawl/internal/store"
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
	case control.Manifest:
		return r.printManifest(value)
	case doctorOutput:
		return r.printDoctor(value)
	case store.ImportStats:
		if _, err := fmt.Fprintf(r.stdout, "source_path: %s\ndb_path: %s\nchats: %d\nmessages: %d\nmedia_messages: %d\nmedia_files: %d\nmedia_bytes: %d\nstarted_at: %s\nfinished_at: %s\n",
			value.SourcePath, value.DBPath, value.Chats, value.Messages, value.MediaMessages, value.MediaFiles, value.MediaBytes, shortLocalTime(value.StartedAt), shortLocalTime(value.FinishedAt)); err != nil {
			return err
		}
		if hasRemoteMediaStats(value) {
			if _, err := fmt.Fprintf(
				r.stdout,
				"remote_media_candidates: %d\nremote_media_attempted: %d\nremote_media_downloads: %d\nremote_media_missing: %d\nremote_media_unavailable: %d\nremote_media_timeouts: %d\nremote_media_errors: %d\n",
				value.RemoteMediaCandidates,
				value.RemoteMediaAttempted,
				value.RemoteMediaDownloads,
				value.RemoteMediaMissing,
				value.RemoteMediaUnavailable,
				value.RemoteMediaTimeouts,
				value.RemoteMediaErrors,
			); err != nil {
				return err
			}
		}
		return nil
	case chatsEnvelope:
		return r.printChats(value)
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
