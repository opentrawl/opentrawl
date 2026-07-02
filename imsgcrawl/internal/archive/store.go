package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

type Store struct {
	store          *store.Store
	path           string
	schemaOutdated bool
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".imsgcrawl", "archive.db")
	}
	return filepath.Join(home, ".imsgcrawl", "archive.db")
}

func Exists(path string) bool {
	if path == "" {
		path = DefaultPath()
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	st, err := store.Open(ctx, store.Options{Path: path, Schema: schema, SchemaVersion: schemaVersion})
	if err != nil {
		return nil, err
	}
	out := &Store{store: st, path: path}
	if err := ensureArchiveSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return out, nil
}

// ErrSchemaOutdated means the archive predates a schema addition this
// binary's read queries need. Reads never migrate (they open the archive
// read-only), so the remedy is one sync, which upgrades the schema.
var ErrSchemaOutdated = errors.New("archive schema predates this version; run: imsgcrawl sync")

func OpenExisting(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	hasDisplayName, err := tableHasColumn(ctx, st.DB(), "handles", "display_name")
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Store{store: st, path: path, schemaOutdated: !hasDisplayName}, nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func Sync(ctx context.Context, archivePath, sourcePath string) (SyncResult, error) {
	data, err := messages.ExtractArchive(ctx, sourcePath)
	if err != nil {
		return SyncResult{}, err
	}
	st, err := Open(ctx, archivePath)
	if err != nil {
		return SyncResult{}, err
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	if err := st.ReplaceAll(ctx, data, now); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{
		ArchivePath:      st.path,
		SourcePath:       data.SourcePath,
		SourceBytes:      data.SourceBytes,
		SourceModifiedAt: data.SourceModifiedAt.Format(time.RFC3339),
		SyncedAt:         now.Format(time.RFC3339),
		Handles:          len(data.Handles),
		Chats:            len(data.Chats),
		Participants:     len(data.Participants),
		ChatMessages:     len(data.ChatMessages),
		Messages:         len(data.Messages),
	}, nil
}

func (s *Store) ReplaceAll(ctx context.Context, data messages.ArchiveData, syncedAt time.Time) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"messages_fts", "messages", "chat_messages", "chat_participants", "chats", "handles", "sync_state"} {
			if _, err := tx.ExecContext(ctx, "delete from "+table); err != nil {
				return err
			}
		}
		for _, h := range data.Handles {
			if _, err := tx.ExecContext(ctx, insertHandlesSQL, h.SourceRowID, h.ID, h.Service, h.UncanonicalizedID, h.DisplayName); err != nil {
				return err
			}
		}
		for _, c := range data.Chats {
			_, err := tx.ExecContext(ctx, insertChatsSQL,
				c.SourceRowID, c.GUID, c.ChatIdentifier, c.ServiceName, c.DisplayName, c.RoomName, boolInt(c.IsArchived))
			if err != nil {
				return err
			}
		}
		for _, p := range data.Participants {
			if _, err := tx.ExecContext(ctx, insertChatParticipantsSQL, p.ChatRowID, p.HandleRowID); err != nil {
				return err
			}
		}
		for _, cm := range data.ChatMessages {
			if _, err := tx.ExecContext(ctx, insertChatMessagesSQL, cm.ChatRowID, cm.MessageRowID); err != nil {
				return err
			}
		}
		for _, m := range data.Messages {
			_, err := tx.ExecContext(ctx, insertMessagesSQL,
				m.SourceRowID, m.GUID, m.HandleRowID, m.Date, m.Service, boolInt(m.IsFromMe), m.Text, boolInt(m.HasAttachments))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertMessagesFTSSQL, m.SourceRowID, m.Text); err != nil {
				return err
			}
		}
		return replaceSyncState(ctx, tx, data, syncedAt)
	})
}

func replaceSyncState(ctx context.Context, tx *sql.Tx, data messages.ArchiveData, syncedAt time.Time) error {
	state := map[string]string{
		"last_sync_at":        syncedAt.UTC().Format(time.RFC3339),
		"source_path":         data.SourcePath,
		"source_bytes":        strconv.FormatInt(data.SourceBytes, 10),
		"source_modified_at":  data.SourceModifiedAt.UTC().Format(time.RFC3339),
		"source_extracted_at": data.ExtractedAt.UTC().Format(time.RFC3339),
	}
	for key, value := range state {
		if _, err := tx.ExecContext(ctx, upsertSyncStateSQL, key, value); err != nil {
			return err
		}
	}
	return nil
}

func ensureArchiveSchema(ctx context.Context, db *sql.DB) error {
	hasDisplayName, err := tableHasColumn(ctx, db, "handles", "display_name")
	if err != nil {
		return err
	}
	if hasDisplayName {
		return nil
	}
	if _, err := db.ExecContext(ctx, `alter table handles add column display_name text`); err != nil {
		return fmt.Errorf("add handles.display_name: %w", err)
	}
	return nil
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
