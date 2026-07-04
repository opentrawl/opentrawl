package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/openclaw/wacrawl/internal/sqlitedsn"
	"github.com/openclaw/wacrawl/internal/store/storedb"
	_ "modernc.org/sqlite"
)

const (
	schemaVersion     = 1
	maxJSONUnixSecond = 253402300799 // 9999-12-31T23:59:59Z, the largest time.Time JSON can marshal.

	MessageRefPrefix = "wacrawl:msg/"
	ownerWhoKey      = "owner:me"

	messageSelectColumns = `source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, '' as snippet`
	messageScanColumns   = `source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, raw_type, message_type, media_type, media_title, media_path, media_url, media_size, starred, snippet`
)

type Store struct {
	db   *sql.DB
	q    *storedb.Queries
	path string
}

type ImportStats struct {
	SourcePath    string    `json:"source_path"`
	DBPath        string    `json:"db_path"`
	Chats         int       `json:"chats"`
	Contacts      int       `json:"contacts"`
	Groups        int       `json:"groups"`
	Participants  int       `json:"participants"`
	Messages      int       `json:"messages"`
	MediaMessages int       `json:"media_messages"`
	MediaCopied   int       `json:"media_copied,omitempty"`
	MediaMissing  int       `json:"media_missing,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
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
	FirstName   string
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	dsn := sqlitedsn.File(
		path,
		sqlitedsn.P("_pragma", "foreign_keys(1)"),
		sqlitedsn.P("_pragma", "journal_mode(WAL)"),
		sqlitedsn.P("_pragma", "synchronous(NORMAL)"),
		sqlitedsn.P("_pragma", "busy_timeout(5000)"),
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &Store{db: db, q: storedb.New(db), path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
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
	dsn := sqlitedsn.File(
		path,
		sqlitedsn.P("mode", "ro"),
		sqlitedsn.P("_pragma", "query_only(1)"),
		sqlitedsn.P("_pragma", "busy_timeout(5000)"),
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite read-only: %w", err)
	}
	return &Store{db: db, q: storedb.New(db), path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if err := shortref.EnsureSchema(ctx, s.db); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}
