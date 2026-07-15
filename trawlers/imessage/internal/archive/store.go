package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/addressbook"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// Sync-state lives in the one trawlkit state.Store. Scalar sync
// markers are keyed under the "sync" entity type; derived-state bookkeeping
// under "derived".
const (
	syncSource            = "imessage"
	legacySyncSource      = "imsgcrawl"
	syncEntityType        = "sync"
	stateLastSyncAt       = "last_sync_at"
	stateSourcePath       = "source_path"
	stateSourceBytes      = "source_bytes"
	stateSourceModifiedAt = "source_modified_at"
)

type Store struct {
	store          *store.Store
	path           string
	schemaOutdated bool
	owned          bool
}

type SyncOptions struct {
	ArchivePath           string
	SourcePath            string
	AddressBookPaths      []string
	UseDefaultAddressBook bool
}

// DefaultPaths is the one archive path layout, from trawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/imessage.
func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: "imessage", BaseDir: "~/.opentrawl/imessage"}.DefaultPaths()
	return paths
}

func DefaultPath() string {
	return DefaultPaths().DBPath
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
	st, err := store.Open(ctx, store.Options{Path: path, Schema: schema + state.Schema, SchemaVersion: schemaVersion})
	if err != nil {
		return nil, err
	}
	out := &Store{store: st, path: path}
	if err := ensureArchiveSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	out.owned = true
	return out, nil
}

// ErrArchiveSync marks failures after source extraction and contact reads,
// when sync is opening or writing the archive.
var ErrArchiveSync = errors.New("archive sync failed")

// ErrSchemaOutdated means the archive predates a schema addition this
// binary's read queries need. Reads never migrate source-derived content, so
// the remedy is one sync, which upgrades the schema.
var ErrSchemaOutdated = errors.New("archive schema predates this version; run trawl imessage sync")

type archiveSyncError struct {
	err error
}

func (e archiveSyncError) Error() string {
	return e.err.Error()
}

func (e archiveSyncError) Unwrap() error {
	return e.err
}

func (e archiveSyncError) Is(target error) bool {
	return target == ErrArchiveSync
}

func archiveSyncErr(err error) error {
	if err == nil {
		return nil
	}
	return archiveSyncError{err: err}
}

func OpenForDerivedRepair(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if err := shortref.EnsureSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	outdated, err := detectSchemaOutdated(ctx, st.DB())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Store{store: st, path: path, schemaOutdated: outdated, owned: true}, nil
}

func Use(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if _, err := st.DB().ExecContext(ctx, schema+state.Schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := st.EnsureSchemaVersion(ctx, schemaVersion); err != nil {
		return nil, err
	}
	if err := ensureArchiveSchema(ctx, st.DB()); err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
}

func UseExisting(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	outdated, err := detectSchemaOutdated(ctx, st.DB())
	if err != nil {
		return nil, err
	}
	return &Store{store: st, path: path, schemaOutdated: outdated}, nil
}

// detectSchemaOutdated reports whether the archive predates a schema change
// this binary's reads need. A missing display_name/contact_key column or a
// pre-migration key/value sync_state (no source_name column) all mean the
// remedy is one sync (rules §1.16/§1.17).
func detectSchemaOutdated(ctx context.Context, db *sql.DB) (bool, error) {
	hasDisplayName, err := tableHasColumn(ctx, db, "handles", "display_name")
	if err != nil {
		return false, err
	}
	hasContactKey, err := tableHasColumn(ctx, db, "contact_mappings", "contact_key")
	if err != nil {
		return false, err
	}
	hasCanonicalState, err := tableHasColumn(ctx, db, "sync_state", "source_name")
	if err != nil {
		return false, err
	}
	return !hasDisplayName || !hasContactKey || !hasCanonicalState, nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	if !s.owned {
		return nil
	}
	return s.store.Close()
}

func Sync(ctx context.Context, archivePath, sourcePath string) (SyncResult, error) {
	options := SyncOptions{ArchivePath: archivePath, SourcePath: sourcePath}
	if strings.TrimSpace(sourcePath) == "" || filepath.Clean(sourcePath) == filepath.Clean(messages.DefaultChatDBPath()) {
		options.UseDefaultAddressBook = true
	}
	return SyncWithOptions(ctx, options)
}

func SyncWithOptions(ctx context.Context, options SyncOptions) (SyncResult, error) {
	return syncWithStore(ctx, nil, options)
}

func SyncInto(ctx context.Context, opened *store.Store, options SyncOptions) (SyncResult, error) {
	return syncWithStore(ctx, opened, options)
}

func syncWithStore(ctx context.Context, opened *store.Store, options SyncOptions) (SyncResult, error) {
	totalStarted := time.Now()
	extractStarted := time.Now()
	data, err := messages.ExtractArchive(ctx, options.SourcePath)
	extractElapsed := time.Since(extractStarted)
	if err != nil {
		return SyncResult{}, err
	}
	contactsStarted := time.Now()
	contactNames, err := syncContactNames(ctx, options)
	contactsElapsed := time.Since(contactsStarted)
	if err != nil {
		return SyncResult{}, err
	}
	mapStarted := time.Now()
	contactMappings := applyContactNames(&data, contactNames)
	ownerHandles := applyOwnerHandles(&data, contactNames, contactMappings)
	mapElapsed := time.Since(mapStarted)
	var st *Store
	if opened != nil {
		st, err = Use(ctx, opened, options.ArchivePath)
	} else {
		st, err = Open(ctx, options.ArchivePath)
	}
	if err != nil {
		return SyncResult{}, archiveSyncErr(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Now().UTC()
	writeStarted := time.Now()
	if err := st.ReplaceAll(ctx, data, contactMappings, ownerHandles, now); err != nil {
		return SyncResult{}, archiveSyncErr(err)
	}
	writeElapsed := time.Since(writeStarted)
	return SyncResult{
		ArchivePath:      st.path,
		SourcePath:       data.SourcePath,
		SourceBytes:      data.SourceBytes,
		SourceModifiedAt: data.SourceModifiedAt.Format(time.RFC3339),
		SyncedAt:         now.Format(time.RFC3339),
		Handles:          len(data.Handles),
		NamedContacts:    len(contactMappings),
		Chats:            len(data.Chats),
		Participants:     len(data.Participants),
		ChatMessages:     len(data.ChatMessages),
		Messages:         len(data.Messages),
		ExtractElapsed:   extractElapsed,
		ContactsElapsed:  contactsElapsed,
		MapElapsed:       mapElapsed,
		WriteElapsed:     writeElapsed,
		TotalElapsed:     time.Since(totalStarted),
	}, nil
}

func (s *Store) ReplaceAll(ctx context.Context, data messages.ArchiveData, contactMappings []ContactMapping, ownerHandles []OwnerHandle, syncedAt time.Time) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := shortref.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		for _, table := range []string{"short_refs", "messages_fts", "messages", "chat_messages", "chat_participants", "chats", "handles", "contact_mappings", "owner_handles", "sync_state"} {
			if _, err := tx.ExecContext(ctx, "delete from "+table); err != nil {
				return err
			}
		}
		for _, h := range data.Handles {
			if _, err := tx.ExecContext(ctx, insertHandlesSQL, h.SourceRowID, h.ID, h.Service, h.UncanonicalizedID, h.DisplayName); err != nil {
				return err
			}
		}
		for _, mapping := range contactMappings {
			if _, err := tx.ExecContext(ctx, insertContactMappingSQL, mapping.Kind, mapping.NormalizedHandle, mapping.ContactKey, mapping.DisplayName); err != nil {
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
				m.SourceRowID, m.GUID, m.HandleRowID, m.Date, m.Service, m.Account, boolInt(m.IsFromMe), m.Text, boolInt(m.HasAttachments), boolInt(m.IsRead))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertMessagesFTSSQL, m.SourceRowID, m.Text); err != nil {
				return err
			}
		}
		for _, h := range ownerHandles {
			if _, err := tx.ExecContext(ctx, `insert or ignore into owner_handles(kind, normalized_handle) values(?, ?)`, h.Kind, h.NormalizedHandle); err != nil {
				return err
			}
		}
		return replaceSyncState(ctx, tx, data, syncedAt)
	})
}

func syncContactNames(ctx context.Context, options SyncOptions) ([]addressbook.ContactName, error) {
	if options.AddressBookPaths != nil {
		return addressbook.Extract(ctx, options.AddressBookPaths)
	}
	if options.UseDefaultAddressBook {
		return addressbook.ExtractDefault(ctx)
	}
	return nil, nil
}

func applyContactNames(data *messages.ArchiveData, names []addressbook.ContactName) []ContactMapping {
	if len(names) == 0 {
		return nil
	}
	lookup := addressbook.NewLookup(names)
	seen := map[string]ContactMapping{}
	for i := range data.Handles {
		name, ok := lookup.Match(data.Handles[i].ID)
		if !ok {
			continue
		}
		data.Handles[i].DisplayName = name.DisplayName
		key := name.Kind + ":" + name.Handle
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = ContactMapping{
			Kind:             name.Kind,
			NormalizedHandle: name.Handle,
			ContactKey:       name.ContactKey,
			DisplayName:      name.DisplayName,
		}
	}
	out := make([]ContactMapping, 0, len(seen))
	for _, mapping := range seen {
		out = append(out, mapping)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].NormalizedHandle < out[j].NormalizedHandle
	})
	return out
}

func replaceSyncState(ctx context.Context, tx *sql.Tx, data messages.ArchiveData, syncedAt time.Time) error {
	syncState := state.New(tx)
	entries := []struct{ id, value string }{
		{stateLastSyncAt, syncedAt.UTC().Format(time.RFC3339)},
		{stateSourcePath, data.SourcePath},
		{stateSourceBytes, strconv.FormatInt(data.SourceBytes, 10)},
		{stateSourceModifiedAt, data.SourceModifiedAt.UTC().Format(time.RFC3339)},
	}
	for _, entry := range entries {
		if err := syncState.Set(ctx, syncSource, syncEntityType, entry.id, entry.value); err != nil {
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
	if !hasDisplayName {
		if _, err := db.ExecContext(ctx, `alter table handles add column display_name text`); err != nil {
			return fmt.Errorf("add handles.display_name: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `create table if not exists contact_mappings (
  kind text not null,
  normalized_handle text not null,
  contact_key text not null default '',
  display_name text not null,
  primary key (kind, normalized_handle)
)`); err != nil {
		return fmt.Errorf("create contact_mappings: %w", err)
	}
	hasContactKey, err := tableHasColumn(ctx, db, "contact_mappings", "contact_key")
	if err != nil {
		return err
	}
	if !hasContactKey {
		if _, err := db.ExecContext(ctx, `alter table contact_mappings add column contact_key text not null default ''`); err != nil {
			return fmt.Errorf("add contact_mappings.contact_key: %w", err)
		}
	}
	hasAccount, err := tableHasColumn(ctx, db, "messages", "account")
	if err != nil {
		return err
	}
	if !hasAccount {
		if _, err := db.ExecContext(ctx, `alter table messages add column account text`); err != nil {
			return fmt.Errorf("add messages.account: %w", err)
		}
	}
	hasIsRead, err := tableHasColumn(ctx, db, "messages", "is_read")
	if err != nil {
		return err
	}
	if !hasIsRead {
		if _, err := db.ExecContext(ctx, `alter table messages add column is_read integer not null default 0`); err != nil {
			return fmt.Errorf("add messages.is_read: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `create table if not exists owner_handles (
  kind text not null,
  normalized_handle text not null,
  primary key (kind, normalized_handle)
)`); err != nil {
		return fmt.Errorf("create owner_handles: %w", err)
	}
	if err := shortref.EnsureSchema(ctx, db); err != nil {
		return err
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

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	rows, err := db.QueryContext(ctx, `select name from sqlite_master where type = 'table' and name = ?`, table)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	exists := rows.Next()
	if err := rows.Err(); err != nil {
		return false, err
	}
	return exists, nil
}
