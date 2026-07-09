package archive

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/projection"
)

func (s *Store) ApplySync(ctx context.Context, batch SyncBatch) (SyncStats, error) {
	stats := SyncStats{
		Notes:       len(batch.Notes),
		BodyReads:   len(batch.Bodies),
		ArchivePath: s.path,
		SyncedAt:    batch.LastSeenAt,
	}
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if batch.ReplaceNotes {
			if _, err := tx.ExecContext(ctx, "delete from notes"); err != nil {
				return err
			}
		}
		for _, note := range batch.Notes {
			var err error
			if batch.ReplaceNotes {
				err = upsertNote(ctx, tx, note, batch.LastSeenAt)
			} else {
				err = insertNoteIfMissing(ctx, tx, note, batch.LastSeenAt)
			}
			if err != nil {
				return err
			}
		}
		for _, table := range batch.TableData {
			if err := insertTableData(ctx, tx, table); err != nil {
				return err
			}
		}
		for _, body := range batch.Bodies {
			inserted, err := insertVersion(ctx, tx, body)
			if err != nil {
				return err
			}
			if inserted {
				stats.NewVersions++
			}
			if err := insertObservation(ctx, tx, body); err != nil {
				return err
			}
			stats.Observations++
		}
		for _, att := range batch.Attachments {
			if err := upsertAttachment(ctx, tx, att, batch.LastSeenAt); err != nil {
				return err
			}
			switch att.Status {
			case AttachmentStatusCopied:
				stats.AttachmentsCopied++
			case AttachmentStatusMissing:
				stats.AttachmentsMissing++
			case AttachmentStatusNoFile:
				stats.AttachmentsNoFile++
			}
		}
		for key, value := range batch.SyncState {
			if err := upsertSyncState(ctx, tx, key, value); err != nil {
				return err
			}
		}
		return nil
	})
	return stats, err
}

func upsertAttachment(ctx context.Context, tx *sql.Tx, att AttachmentInsert, seenAt string) error {
	_, err := tx.ExecContext(ctx, `
insert into attachments
  (attachment_id, note_id, media_id, name, type, archive_path, status, source_bytes, first_observed_at, last_seen_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(attachment_id) do update set
  note_id = excluded.note_id,
  media_id = excluded.media_id,
  name = excluded.name,
  type = excluded.type,
  archive_path = excluded.archive_path,
  status = excluded.status,
  source_bytes = excluded.source_bytes,
  last_seen_at = excluded.last_seen_at`,
		att.ID, att.NoteID, att.MediaID, att.Name, att.Type, att.ArchivePath, att.Status, att.SourceBytes, seenAt, seenAt)
	return err
}

func upsertNote(ctx context.Context, tx *sql.Tx, note Note, seenAt string) error {
	_, err := tx.ExecContext(ctx, `
insert into notes (note_id, title, folder, created_at, modified_at, last_seen_at)
values (?, ?, ?, ?, ?, ?)
on conflict(note_id) do update set
  title = excluded.title,
  folder = excluded.folder,
  created_at = excluded.created_at,
  modified_at = excluded.modified_at,
  last_seen_at = excluded.last_seen_at`,
		note.ID, note.Title, note.Folder, note.CreatedAt, note.ModifiedAt, seenAt)
	return err
}

func insertNoteIfMissing(ctx context.Context, tx *sql.Tx, note Note, seenAt string) error {
	_, err := tx.ExecContext(ctx, `
insert or ignore into notes (note_id, title, folder, created_at, modified_at, last_seen_at)
values (?, ?, ?, ?, ?, ?)`,
		note.ID, note.Title, note.Folder, note.CreatedAt, note.ModifiedAt, seenAt)
	return err
}

func insertTableData(ctx context.Context, tx *sql.Tx, table TableDataInsert) error {
	_, err := tx.ExecContext(ctx, `
insert into note_table_data (attachment_uuid, zdata, zdata_bytes, first_observed_at)
values (?, ?, ?, ?)
on conflict(attachment_uuid) do update set
  zdata = excluded.zdata,
  zdata_bytes = excluded.zdata_bytes`,
		table.AttachmentUUID, table.ZData, len(table.ZData), table.ObservedAt)
	return err
}

func insertVersion(ctx context.Context, tx *sql.Tx, body BodyInsert) (bool, error) {
	text, status, unsupported := decodeProjection(ctx, tx, body.ZData)
	res, err := tx.ExecContext(ctx, `
insert or ignore into note_versions
  (note_id, zdata_sha256, zdata, zdata_bytes, text, text_status, unsupported_reason,
   source_modified_at, first_observed_at, latest_observed_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		body.NoteID, body.ZDataSHA256, body.ZData, len(body.ZData), text, status, unsupported,
		body.SourceModifiedAt, body.ObservedAt, body.ObservedAt)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		_, err := tx.ExecContext(ctx, `
update note_versions
set latest_observed_at = ?
where note_id = ? and zdata_sha256 = ?`, body.ObservedAt, body.NoteID, body.ZDataSHA256)
		return false, err
	}
	if status == "decoded" {
		if _, err := tx.ExecContext(ctx, `
insert into notes_fts(note_id, zdata_sha256, title, body)
values (?, ?, ?, ?)`, body.NoteID, body.ZDataSHA256, body.Title, text); err != nil {
			return false, err
		}
	}
	return true, nil
}

func insertObservation(ctx context.Context, tx *sql.Tx, body BodyInsert) error {
	_, err := tx.ExecContext(ctx, `
insert into version_observations
  (note_id, zdata_sha256, source, source_detail, source_sequence, source_modified_at, observed_at)
values (?, ?, ?, ?, ?, ?, ?)`,
		body.NoteID, body.ZDataSHA256, body.Source, body.SourceDetail, body.SourceSequence, body.SourceModifiedAt, body.ObservedAt)
	return err
}

func upsertSyncState(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx, `
insert into sync_state (key, value)
values (?, ?)
on conflict(key) do update set value = excluded.value`, key, value)
	return err
}

func decodeProjection(ctx context.Context, tx *sql.Tx, zdata []byte) (text, status, unsupported string) {
	text, err := projection.DecodeMarkdown(zdata, tableResolver(ctx, tx))
	if err == nil {
		return strings.TrimSpace(text), "decoded", ""
	}
	return "", "unsupported", err.Error()
}

// tableResolver fetches a table's companion CRDT blob from note_table_data
// within the current transaction. A miss (no bytes captured for that UUID)
// returns ok=false, and the projector renders the "not captured" marker — not
// a decode failure.
func tableResolver(ctx context.Context, tx *sql.Tx) projection.TableResolver {
	return func(attachmentUUID string) ([]byte, bool) {
		var zdata []byte
		err := tx.QueryRowContext(ctx,
			`select zdata from note_table_data where attachment_uuid = ?`, attachmentUUID).Scan(&zdata)
		if err != nil {
			return nil, false
		}
		return zdata, true
	}
}

func SourcePathHint(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.Contains(path, "Group Containers") && strings.Contains(path, "group.com.apple.notes") {
		return "apple_notes_group_container"
	}
	if strings.Contains(path, "NoteStore.sqlite") {
		return "notestore_sqlite_copy"
	}
	return "explicit_local_store"
}
