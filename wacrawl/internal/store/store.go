package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/openclaw/wacrawl/internal/store/storedb"

	// C SQLite via cgo, matching crawlkit/store after the modernc production
	// incidents. Requires -tags sqlite_fts5; the monorepo devenv sets GOFLAGS.
	_ "github.com/mattn/go-sqlite3"
)

const (
	schemaVersion     = 1
	maxJSONUnixSecond = 253402300799 // 9999-12-31T23:59:59Z, the largest time.Time JSON can marshal.

	MessageRefPrefix = "wacrawl:msg/"
	ownerWhoKey      = "owner:me"

	// Sync-state lives in the one crawlkit state.Store (TRAWL-82). Scalar sync
	// markers sit under entity_type "sync"; the short-ref fingerprint, which is
	// derived from the archive, sits under "derived".
	syncSource        = "wacrawl"
	syncEntityType    = "sync"
	derivedEntityType = "derived"
	stateLastImportAt = "last_import_at"
	stateSourcePath   = "source_path"

	messageSelectColumns = `source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, '' as snippet`
	messageScanColumns   = `source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, snippet`
)

type Store struct {
	store *ckstore.Store
	db    *sql.DB
	q     *storedb.Queries
	path  string
	owned bool
}

type ImportStats struct {
	SourcePath      string        `json:"source_path"`
	DBPath          string        `json:"db_path"`
	Chats           int           `json:"chats"`
	Contacts        int           `json:"contacts"`
	Groups          int           `json:"groups"`
	Participants    int           `json:"participants"`
	Messages        int           `json:"messages"`
	MediaMessages   int           `json:"media_messages"`
	MediaCopied     int           `json:"media_copied,omitempty"`
	MediaMissing    int           `json:"media_missing,omitempty"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      time.Time     `json:"finished_at"`
	TotalElapsed    time.Duration `json:"-"`
	SnapshotElapsed time.Duration `json:"-"`
	ExtractElapsed  time.Duration `json:"-"`
	MediaElapsed    time.Duration `json:"-"`
	WriteElapsed    time.Duration `json:"-"`
}

type Status struct {
	DBPath         string    `json:"db_path"`
	Chats          int       `json:"chats"`
	UnreadChats    int       `json:"unread_chats"`
	UnreadMessages int       `json:"unread_messages"`
	Contacts       int       `json:"contacts"`
	Groups         int       `json:"groups"`
	Participants   int       `json:"participants"`
	Messages       int       `json:"messages"`
	MediaMessages  int       `json:"media_messages"`
	OldestMessage  time.Time `json:"oldest_message,omitzero"`
	NewestMessage  time.Time `json:"newest_message,omitzero"`
	LastImportAt   time.Time `json:"last_import_at,omitzero"`
	LastSource     string    `json:"last_source,omitempty"`
}

type Chat struct {
	JID            string
	Kind           string
	Name           string
	LastMessageAt  time.Time
	UnreadCount    int
	Archived       bool
	Removed        bool
	Hidden         bool
	RawSessionType int
	MessageCount   int
}

type ChatFilter struct {
	Limit      int
	OnlyUnread bool
}

type Contact struct {
	JID          string
	Phone        string
	FullName     string
	FirstName    string
	LastName     string
	BusinessName string
	Username     string
	LID          string
	AboutText    string
	UpdatedAt    time.Time
}

type Group struct {
	JID       string
	Name      string
	OwnerJID  string
	CreatedAt time.Time
}

type GroupParticipant struct {
	GroupJID    string
	UserJID     string
	ContactName string
	IsAdmin     bool
	IsActive    bool
}

type Message struct {
	SourcePK    int64     `json:"source_pk"`
	ChatJID     string    `json:"chat_jid"`
	ChatName    string    `json:"chat_name,omitempty"`
	MessageID   string    `json:"message_id"`
	SenderJID   string    `json:"sender_jid,omitempty"`
	SenderName  string    `json:"sender_name,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	FromMe      bool      `json:"from_me"`
	Text        string    `json:"text,omitempty"`
	RawType     int       `json:"raw_type"`
	MessageType string    `json:"message_type,omitempty"`
	MediaType   string    `json:"media_type,omitempty"`
	MediaTitle  string    `json:"media_title,omitempty"`
	MediaPath   string    `json:"media_path,omitempty"`
	MediaURL    string    `json:"media_url,omitempty"`
	MediaSize   int64     `json:"media_size,omitempty"`
	Starred     bool      `json:"starred,omitempty"`
	Snippet     string    `json:"snippet,omitempty"`
}

type MessageFilter struct {
	Query   string
	ChatJID string
	Sender  string
	Who     string
	WhoKeys []string
	Limit   int
	After   *time.Time
	Before  *time.Time
	// BeforePK tightens Before into a composite cursor: rows must have
	// ts < Before, or ts == Before with source_pk < BeforePK. Without it,
	// paging by timestamp alone can stall when a page boundary lands inside
	// a run of messages that share the same second.
	BeforePK int64
	FromMe   *bool
	HasMedia bool
	Asc      bool
}

type WhoResolution struct {
	ParticipantKeys []string
	DisplayNames    []string
	Candidates      []WhoCandidate
	matchRank       whomatch.Rank
}

type WhoCandidate struct {
	Who             string    `json:"who"`
	Identifiers     []string  `json:"identifiers"`
	LastSeen        time.Time `json:"last_seen"`
	Messages        int       `json:"messages"`
	ParticipantKeys []string  `json:"-"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("db path is required")
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		return nil, err
	}
	s := wrapStore(st, path, true)
	if err := s.migrate(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	return s, nil
}

// OpenReadOnly opens an existing archive for read commands. It never
// creates, migrates or checkpoints the database, so reads cannot change
// the archive file. A missing archive surfaces as os.ErrNotExist.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("db path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := checkSchemaVersion(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return wrapStore(st, path, true), nil
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	out := wrapStore(st, path, false)
	if err := out.migrate(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func UseExisting(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if err := checkSchemaVersion(ctx, st.DB()); err != nil {
		return nil, err
	}
	return wrapStore(st, path, false), nil
}

func wrapStore(st *ckstore.Store, path string, owned bool) *Store {
	return &Store{store: st, db: st.DB(), q: storedb.New(st.DB()), path: path, owned: owned}
}

func (s *Store) Close() error {
	if s == nil || s.store == nil || !s.owned {
		return nil
	}
	return s.store.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := s.migrateSyncState(ctx); err != nil {
		return err
	}
	if err := shortref.EnsureSchema(ctx, s.db); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

func checkSchemaVersion(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "pragma user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version != schemaVersion {
		return fmt.Errorf("archive schema is not current: got %d, want %d", version, schemaVersion)
	}
	return nil
}

// migrateSyncState tombstones the pre-state.Store key/value sync_state table
// (TRAWL-82). Its columns collide with crawlkit/state and everything it held —
// the last-import marker, source path and short-ref fingerprint — is re-derived
// by one sync, so we drop, never map (rules §1.17). The drop only fires on a
// pre-migration archive (no source_name column), so a canonical archive keeps
// its live state across the writable opens that read and rebuild short refs.
func (s *Store) migrateSyncState(ctx context.Context) error {
	canonical, err := tableHasColumn(ctx, s.db, "sync_state", "source_name")
	if err != nil {
		return err
	}
	if !canonical {
		if _, err := s.db.ExecContext(ctx, `drop table if exists sync_state`); err != nil {
			return fmt.Errorf("tombstone legacy sync_state: %w", err)
		}
	}
	return state.EnsureSchema(ctx, s.db)
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
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
