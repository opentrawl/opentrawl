package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/openclaw/crawlkit/shortref"
	ckstore "github.com/openclaw/crawlkit/store"
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

const shortRefFingerprintKey = "short_refs_fingerprint"

var ErrShortRefIndexStale = errors.New("short ref index is stale")

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

func (r WhoResolution) OnlyCloseSpellingMatch() bool {
	return len(r.Candidates) == 1 && r.matchRank == whomatch.RankCloseSpelling
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

func (s *Store) ReplaceAll(ctx context.Context, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)

	for _, deleteQuery := range []func(context.Context) error{
		q.DeleteMessagesFTS,
		q.DeleteMessages,
		q.DeleteGroupParticipants,
		q.DeleteGroups,
		q.DeleteChats,
		q.DeleteContacts,
		q.DeleteSyncState,
	} {
		if err := deleteQuery(ctx); err != nil {
			return err
		}
	}
	for _, c := range contacts {
		err := q.InsertContact(ctx, storedb.InsertContactParams{
			Jid:          c.JID,
			Phone:        nullString(c.Phone),
			FullName:     nullString(c.FullName),
			FirstName:    nullString(c.FirstName),
			LastName:     nullString(c.LastName),
			BusinessName: nullString(c.BusinessName),
			Username:     nullString(c.Username),
			Lid:          nullString(c.LID),
			AboutText:    nullString(c.AboutText),
			UpdatedAt:    nullInt64(unix(c.UpdatedAt)),
		})
		if err != nil {
			return err
		}
	}
	for _, c := range chats {
		err := q.InsertChat(ctx, storedb.InsertChatParams{
			Jid:            c.JID,
			Kind:           c.Kind,
			Name:           nullString(c.Name),
			LastMessageAt:  nullInt64(unix(c.LastMessageAt)),
			UnreadCount:    int64(c.UnreadCount),
			Archived:       int64(boolInt(c.Archived)),
			Removed:        int64(boolInt(c.Removed)),
			Hidden:         int64(boolInt(c.Hidden)),
			RawSessionType: int64(c.RawSessionType),
		})
		if err != nil {
			return err
		}
	}
	for _, g := range groups {
		err := q.InsertGroup(ctx, storedb.InsertGroupParams{
			Jid:       g.JID,
			Name:      nullString(g.Name),
			OwnerJid:  nullString(g.OwnerJID),
			CreatedAt: nullInt64(unix(g.CreatedAt)),
		})
		if err != nil {
			return err
		}
	}
	for _, p := range participants {
		err := q.InsertParticipant(ctx, storedb.InsertParticipantParams{
			GroupJid:    p.GroupJID,
			UserJid:     p.UserJID,
			ContactName: nullString(p.ContactName),
			FirstName:   nullString(p.FirstName),
			IsAdmin:     int64(boolInt(p.IsAdmin)),
			IsActive:    int64(boolInt(p.IsActive)),
		})
		if err != nil {
			return err
		}
	}
	for _, m := range messages {
		err := q.InsertMessage(ctx, storedb.InsertMessageParams{
			SourcePk:    m.SourcePK,
			ChatJid:     m.ChatJID,
			ChatName:    nullString(m.ChatName),
			MsgID:       m.MessageID,
			SenderJid:   nullString(m.SenderJID),
			SenderName:  nullString(m.SenderName),
			Ts:          unix(m.Timestamp),
			FromMe:      int64(boolInt(m.FromMe)),
			Text:        nullString(m.Text),
			RawType:     int64(m.RawType),
			MessageType: nullString(m.MessageType),
			MediaType:   nullString(m.MediaType),
			MediaTitle:  nullString(m.MediaTitle),
			MediaPath:   nullString(m.MediaPath),
			MediaUrl:    nullString(m.MediaURL),
			MediaSize:   nullInt64(m.MediaSize),
			Starred:     int64(boolInt(m.Starred)),
		})
		if err != nil {
			return err
		}
		err = q.InsertMessageFTS(ctx, storedb.InsertMessageFTSParams{
			SourcePk: m.SourcePK,
			Text:     nullString(strings.TrimSpace(m.Text + " " + m.MediaTitle)),
			Chat:     nullString(m.ChatName),
			Sender:   nullString(m.SenderName),
			Media:    nullString(m.MediaType),
		})
		if err != nil {
			return err
		}
	}
	if err := replaceShortRefs(ctx, tx, messages); err != nil {
		return err
	}
	now := stats.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for key, value := range map[string]string{
		"last_import_at":       now.Format(time.RFC3339Nano),
		"source_path":          stats.SourcePath,
		shortRefFingerprintKey: shortRefsFingerprint(messageFullRefs(messages)),
	} {
		err := q.InsertSyncState(ctx, storedb.InsertSyncStateParams{
			Key:       key,
			Value:     value,
			UpdatedAt: unix(now),
		})
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func replaceShortRefs(ctx context.Context, tx *sql.Tx, messages []Message) error {
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	index := shortref.NewSQLiteIndex(tx)
	if err := index.Clear(ctx); err != nil {
		return err
	}
	entries, err := shortref.BuildSlice(messageFullRefs(messages))
	if err != nil {
		return err
	}
	return index.UpsertEntries(ctx, shortref.LookupEntries(entries))
}

func (s *Store) EnsureShortRefs(ctx context.Context) error {
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return err
	}
	if current {
		return nil
	}
	return s.RebuildShortRefs(ctx)
}

func (s *Store) RebuildShortRefs(ctx context.Context) error {
	refs, err := s.allMessageFullRefs(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	index := shortref.NewSQLiteIndex(tx)
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	if err := index.Clear(ctx); err != nil {
		return err
	}
	entries, err := shortref.BuildSlice(refs)
	if err != nil {
		return err
	}
	if err := index.UpsertEntries(ctx, shortref.LookupEntries(entries)); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
insert into sync_state(key, value, updated_at)
values(?, ?, ?)
on conflict(key) do update set value = excluded.value, updated_at = excluded.updated_at
`, shortRefFingerprintKey, shortRefsFingerprint(refs), unix(now)); err != nil {
		return fmt.Errorf("record short ref fingerprint: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	alias = strings.TrimSpace(alias)
	if !shortref.ValidAlias(alias) {
		return nil, nil
	}
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, ErrShortRefIndexStale
	}
	return shortref.NewSQLiteIndex(s.db).Lookup(ctx, alias)
}

func (s *Store) ShortRefAliases(ctx context.Context, fullRefs []string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	current, err := s.shortRefsCurrent(ctx)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, ErrShortRefIndexStale
	}
	args := make([]any, 0, len(fullRefs))
	for _, fullRef := range fullRefs {
		args = append(args, fullRef)
	}
	rows, err := s.db.QueryContext(ctx, `
select full_ref, alias
from short_refs
where full_ref in (`+queryPlaceholders(len(args))+`)
order by full_ref, length(alias) desc
`, args...)
	if err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	aliases := make(map[string]string, len(fullRefs))
	for rows.Next() {
		var fullRef, alias string
		if err := rows.Scan(&fullRef, &alias); err != nil {
			return nil, fmt.Errorf("scan short ref alias: %w", err)
		}
		if aliases[fullRef] == "" {
			aliases[fullRef] = alias
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	return aliases, nil
}

func (s *Store) shortRefsCurrent(ctx context.Context) (bool, error) {
	stored, err := s.q.GetSyncState(ctx, shortRefFingerprintKey)
	if err != nil {
		return false, nil
	}
	refs, err := s.allMessageFullRefs(ctx)
	if err != nil {
		return false, err
	}
	if stored != shortRefsFingerprint(refs) {
		return false, nil
	}
	if len(refs) == 0 {
		return true, nil
	}
	indexedRefs, err := s.shortRefFullRefs(ctx)
	if err != nil {
		return false, nil
	}
	if len(indexedRefs) != len(refs) {
		return false, nil
	}
	for i := range refs {
		if refs[i] != indexedRefs[i] {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) allMessageFullRefs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `select msg_id from messages where trim(msg_id) <> '' order by msg_id`)
	if err != nil {
		return nil, fmt.Errorf("read message refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan message ref: %w", err)
		}
		refs = append(refs, MessageRefPrefix+id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read message refs: %w", err)
	}
	return refs, nil
}

func (s *Store) shortRefFullRefs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `select distinct full_ref from short_refs order by full_ref`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func messageFullRefs(messages []Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		id := strings.TrimSpace(message.MessageID)
		if id != "" {
			refs = append(refs, MessageRefPrefix+id)
		}
	}
	sort.Strings(refs)
	return refs
}

func shortRefsFingerprint(refs []string) string {
	hash := sha256.New()
	for _, ref := range refs {
		_, _ = hash.Write([]byte(ref))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{DBPath: s.path}
	var err error
	if out.Chats, err = countInt(ctx, s.q.CountChats); err != nil {
		return out, err
	}
	if out.UnreadChats, err = countInt(ctx, s.q.CountUnreadChats); err != nil {
		return out, err
	}
	if out.UnreadMessages, err = countInt(ctx, s.q.CountUnreadMessages); err != nil {
		return out, err
	}
	if out.Contacts, err = countInt(ctx, s.q.CountContacts); err != nil {
		return out, err
	}
	if out.Groups, err = countInt(ctx, s.q.CountGroups); err != nil {
		return out, err
	}
	if out.Participants, err = countInt(ctx, s.q.CountParticipants); err != nil {
		return out, err
	}
	if out.Messages, err = countInt(ctx, s.q.CountMessages); err != nil {
		return out, err
	}
	if out.MediaMessages, err = countInt(ctx, s.q.CountMediaMessages); err != nil {
		return out, err
	}
	bounds, err := s.q.GetMessageTimeBounds(ctx)
	if err != nil {
		return out, err
	}
	out.OldestMessage = fromUnix(bounds.OldestTs)
	out.NewestMessage = fromUnix(bounds.NewestTs)
	lastImport, _ := s.q.GetSyncState(ctx, "last_import_at")
	if t, err := time.Parse(time.RFC3339Nano, lastImport); err == nil {
		out.LastImportAt = t
	}
	out.LastSource, _ = s.q.GetSyncState(ctx, "source_path")
	return out, nil
}

func (s *Store) ListChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit})
}

func (s *Store) ListUnreadChats(ctx context.Context, limit int) ([]Chat, error) {
	return s.listChats(ctx, ChatFilter{Limit: limit, OnlyUnread: true})
}

func (s *Store) listChats(ctx context.Context, filter ChatFilter) ([]Chat, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.OnlyUnread {
		rows, err := s.q.ListUnreadChats(ctx, int64(filter.Limit))
		if err != nil {
			return nil, err
		}
		out := make([]Chat, 0, len(rows))
		for _, row := range rows {
			out = append(out, unreadChatFromRow(row))
		}
		return out, nil
	}
	rows, err := s.q.ListChats(ctx, int64(filter.Limit))
	if err != nil {
		return nil, err
	}
	out := make([]Chat, 0, len(rows))
	for _, row := range rows {
		out = append(out, chatFromRow(row))
	}
	return out, nil
}

func (s *Store) Messages(ctx context.Context, filter MessageFilter) ([]Message, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	query, args := messageListQuery(filter)
	return scanMessages(ctx, s.db, query, args...)
}

func messageListQuery(filter MessageFilter) (string, []any) {
	validQuery, validArgs := filteredMessagesQuery(filter, "")
	validQuery += " and " + validUnixPredicate("ts")
	if filter.After != nil || filter.Before != nil {
		if filter.Asc {
			validQuery += " order by ts asc, source_pk asc limit ?"
		} else {
			validQuery += " order by ts desc, source_pk desc limit ?"
		}
		return validQuery, append(validArgs, filter.Limit)
	}

	if filter.Asc {
		validQuery, validArgs = filteredMessagesQuery(filter, ", 1 as sort_bucket, ts as sort_ts")
		validQuery += " and " + validUnixPredicate("ts")
		invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 0 as sort_bucket, 0 as sort_ts")
		invalidQuery += " and " + invalidUnixPredicate("ts")
		query := "select " + messageScanColumns + " from (select * from (" + invalidQuery + " order by source_pk asc limit ?) union all select * from (" + validQuery + " order by ts asc, source_pk asc limit ?)) order by sort_bucket asc, sort_ts asc, source_pk asc limit ?"
		args := append([]any{}, invalidArgs...)
		args = append(args, filter.Limit)
		args = append(args, validArgs...)
		args = append(args, filter.Limit, filter.Limit)
		return query, args
	}

	validQuery, validArgs = filteredMessagesQuery(filter, ", 0 as sort_bucket, ts as sort_ts")
	validQuery += " and " + validUnixPredicate("ts")
	invalidQuery, invalidArgs := filteredMessagesQuery(filter, ", 1 as sort_bucket, 0 as sort_ts")
	invalidQuery += " and " + invalidUnixPredicate("ts")
	query := "select " + messageScanColumns + " from (select * from (" + validQuery + " order by ts desc, source_pk desc limit ?) union all select * from (" + invalidQuery + " order by source_pk desc limit ?)) order by sort_bucket asc, sort_ts desc, source_pk desc limit ?"
	args := append([]any{}, validArgs...)
	args = append(args, filter.Limit)
	args = append(args, invalidArgs...)
	args = append(args, filter.Limit, filter.Limit)
	return query, args
}

func filteredMessagesQuery(filter MessageFilter, extraColumns string) (string, []any) {
	query := "select " + messageSelectColumns + extraColumns + " from messages where 1=1"
	return applyMessageFilters(query, nil, filter, false)
}

func (s *Store) Search(ctx context.Context, filter MessageFilter) ([]Message, error) {
	hasQuery := strings.TrimSpace(filter.Query) != ""
	if !hasQuery && !filterAllowsEmptyQuery(filter) {
		return nil, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if !hasQuery {
		query := "select " + messageSelectColumns + " from messages where 1=1"
		var args []any
		query, args = applyMessageFilters(query, args, filter, false)
		if filter.Asc {
			query += " order by ts asc, source_pk asc"
		} else {
			query += " order by ts desc, source_pk desc"
		}
		query += " limit ?"
		args = append(args, filter.Limit)
		messages, err := scanMessages(ctx, s.db, query, args...)
		if err != nil {
			return nil, err
		}
		return s.withCanonicalSenderNames(ctx, messages)
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return nil, err
	}
	query := `select m.source_pk, m.chat_jid, m.chat_name, m.msg_id, m.sender_jid, m.sender_name, m.ts, m.from_me, m.text, m.raw_type, m.message_type, m.media_type, m.media_title, m.media_path, m.media_url, m.media_size, m.starred, '' from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	query, args = applyMessageFilters(query, args, filter, true)
	query += " order by bm25(messages_fts) limit ?"
	args = append(args, filter.Limit)
	messages, err := scanMessages(ctx, s.db, query, args...)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		messages[i].Snippet = contractSnippet(messageSnippetText(messages[i]), filter.Query)
	}
	return s.withCanonicalSenderNames(ctx, messages)
}

func (s *Store) SearchCount(ctx context.Context, filter MessageFilter) (int, error) {
	hasQuery := strings.TrimSpace(filter.Query) != ""
	if !hasQuery && !filterAllowsEmptyQuery(filter) {
		return 0, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return 0, err
	}
	if !hasQuery {
		query := "select count(*) from messages where 1=1"
		var args []any
		query, args = applyMessageFilters(query, args, filter, false)
		var total int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
			return 0, err
		}
		return total, nil
	}
	ftsQuery, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return 0, err
	}
	query := `select count(*) from messages_fts f join messages m on m.rowid=f.rowid where messages_fts match ?`
	args := []any{ftsQuery}
	query, args = applyMessageFilters(query, args, filter, true)
	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func filterAllowsEmptyQuery(filter MessageFilter) bool {
	return filter.WhoKeys != nil || strings.TrimSpace(filter.Who) != "" || filter.After != nil || filter.Before != nil
}

func (s *Store) resolveMessageFilterWho(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if normalizeWhoIdentity(filter.Who) == "" || filter.WhoKeys != nil {
		return filter, nil
	}
	resolution, err := s.ResolveWhoIdentifier(ctx, filter.Who)
	if err != nil {
		return MessageFilter{}, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = s.ResolveWho(ctx, filter.Who)
		if err != nil {
			return MessageFilter{}, err
		}
	}
	filter.Who = normalizeWhoIdentity(filter.Who)
	if len(resolution.Candidates) != 1 || resolution.OnlyCloseSpellingMatch() {
		filter.WhoKeys = []string{}
		return filter, nil
	}
	filter.WhoKeys = resolution.ParticipantKeys
	return filter, nil
}

func (s *Store) WhoMatches(ctx context.Context, identity string) ([]string, error) {
	resolution, err := s.ResolveWho(ctx, identity)
	if err != nil {
		return nil, err
	}
	return resolution.DisplayNames, nil
}

func (s *Store) ResolveWho(ctx context.Context, identity string) (WhoResolution, error) {
	query := normalizeWhoIdentity(identity)
	if query == "" {
		return WhoResolution{}, nil
	}
	records, err := s.whoCandidateRecords(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	if strings.EqualFold(query, "me") {
		return ownerWhoResolution(records), nil
	}
	type rankedCandidate struct {
		record whoCandidateRecord
		rank   whomatch.Rank
	}
	var matches []rankedCandidate
	for _, record := range records {
		rank, ok := record.matchCandidate().MatchRank(query)
		if !ok {
			continue
		}
		matches = append(matches, rankedCandidate{record: record, rank: rank})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := matches[i]
		right := matches[j]
		if left.rank != right.rank {
			return left.rank.BetterThan(right.rank)
		}
		if !left.record.LastSeen.Equal(right.record.LastSeen) {
			return left.record.LastSeen.After(right.record.LastSeen)
		}
		if left.record.Messages != right.record.Messages {
			return left.record.Messages > right.record.Messages
		}
		return strings.ToLower(left.record.Who) < strings.ToLower(right.record.Who)
	})
	resolution := WhoResolution{
		ParticipantKeys: make([]string, 0, len(matches)),
		DisplayNames:    make([]string, 0, len(matches)),
		Candidates:      make([]WhoCandidate, 0, len(matches)),
	}
	if len(matches) > 0 {
		resolution.matchRank = matches[0].rank
	}
	for _, match := range matches {
		candidate := match.record.WhoCandidate
		resolution.ParticipantKeys = append(resolution.ParticipantKeys, candidate.ParticipantKeys...)
		resolution.DisplayNames = append(resolution.DisplayNames, candidate.Who)
		resolution.Candidates = append(resolution.Candidates, candidate)
	}
	return resolution, nil
}

func ownerWhoResolution(records []whoCandidateRecord) WhoResolution {
	for _, record := range records {
		if !record.hasParticipantKey(ownerWhoKey) {
			continue
		}
		candidate := record.WhoCandidate
		return WhoResolution{
			ParticipantKeys: append([]string(nil), candidate.ParticipantKeys...),
			DisplayNames:    []string{candidate.Who},
			Candidates:      []WhoCandidate{candidate},
			matchRank:       whomatch.RankExact,
		}
	}
	return WhoResolution{}
}

func (s *Store) ResolveWhoIdentifier(ctx context.Context, identifier string) (WhoResolution, error) {
	identifier = normalizeWhoIdentity(identifier)
	if identifier == "" {
		return WhoResolution{}, nil
	}
	records, err := s.whoCandidateRecordsWithoutNameMerge(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	var matches []WhoCandidate
	for _, record := range records {
		for _, candidateIdentifier := range record.Identifiers {
			rank, ok := whomatch.MatchRank(identifier, []string{candidateIdentifier})
			if ok && rank == whomatch.RankExact {
				matches = append(matches, record.WhoCandidate)
				break
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if !matches[i].LastSeen.Equal(matches[j].LastSeen) {
			return matches[i].LastSeen.After(matches[j].LastSeen)
		}
		if matches[i].Messages != matches[j].Messages {
			return matches[i].Messages > matches[j].Messages
		}
		return strings.ToLower(matches[i].Who) < strings.ToLower(matches[j].Who)
	})
	resolution := WhoResolution{
		ParticipantKeys: make([]string, 0, len(matches)),
		DisplayNames:    make([]string, 0, len(matches)),
		Candidates:      matches,
	}
	if len(matches) > 0 {
		resolution.matchRank = whomatch.RankExact
	}
	for _, match := range matches {
		resolution.ParticipantKeys = append(resolution.ParticipantKeys, match.ParticipantKeys...)
		resolution.DisplayNames = append(resolution.DisplayNames, match.Who)
	}
	return resolution, nil
}

type whoCandidateRecord struct {
	WhoCandidate
	aliases []string
}

func (r whoCandidateRecord) hasParticipantKey(value string) bool {
	for _, key := range r.ParticipantKeys {
		if key == value {
			return true
		}
	}
	return false
}

func (r whoCandidateRecord) matchCandidate() whomatch.Candidate {
	return whomatch.Candidate{
		Who:         r.Who,
		Identifiers: r.Identifiers,
		Aliases:     r.aliases,
		LastSeen:    r.LastSeen,
		Messages:    int64(r.Messages),
	}
}

type whoCandidateBuilder struct {
	key             string
	names           map[string]*whoNameEvidence
	identifiers     map[string]string
	participantKeys map[string]string
	lastSeen        time.Time
	messages        int
}

type whoNameEvidence struct {
	value       string
	contactFull bool
	pushCount   int
}

func (s *Store) whoCandidateRecords(ctx context.Context) ([]whoCandidateRecord, error) {
	return s.readWhoCandidateRecords(ctx, true)
}

func (s *Store) whoCandidateRecordsWithoutNameMerge(ctx context.Context) ([]whoCandidateRecord, error) {
	return s.readWhoCandidateRecords(ctx, false)
}

func (s *Store) readWhoCandidateRecords(ctx context.Context, mergeSameName bool) ([]whoCandidateRecord, error) {
	builders, err := s.readWhoCandidateAliases(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.readWhoCandidateStats(ctx, builders); err != nil {
		return nil, err
	}
	records := make([]whoCandidateRecord, 0, len(builders))
	for _, builder := range builders {
		if builder.messages == 0 {
			continue
		}
		identifiers := sortedValues(builder.identifiers)
		participantKeys := sortedValues(builder.participantKeys)
		names := builder.nameValues()
		who := chooseWhoName(builder.names, identifiers)
		if who == "" || len(participantKeys) == 0 {
			continue
		}
		records = append(records, whoCandidateRecord{
			WhoCandidate: WhoCandidate{
				Who:             who,
				Identifiers:     identifiers,
				LastSeen:        builder.lastSeen,
				Messages:        builder.messages,
				ParticipantKeys: participantKeys,
			},
			aliases: uniqueStrings(names),
		})
	}
	if mergeSameName {
		records = mergeWhoCandidateRecords(records)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if !records[i].LastSeen.Equal(records[j].LastSeen) {
			return records[i].LastSeen.After(records[j].LastSeen)
		}
		if records[i].Messages != records[j].Messages {
			return records[i].Messages > records[j].Messages
		}
		return strings.ToLower(records[i].Who) < strings.ToLower(records[j].Who)
	})
	return records, nil
}

func mergeWhoCandidateRecords(records []whoCandidateRecord) []whoCandidateRecord {
	if len(records) == 0 {
		return nil
	}
	type extra struct {
		aliases         []string
		participantKeys []string
	}
	extras := map[string]*extra{}
	candidates := make([]whomatch.Candidate, 0, len(records))
	for _, record := range records {
		key := whomatch.Normalize(record.Who)
		if key == "" {
			continue
		}
		candidates = append(candidates, record.matchCandidate())
		group, ok := extras[key]
		if !ok {
			group = &extra{}
			extras[key] = group
		}
		group.aliases = append(group.aliases, record.aliases...)
		group.participantKeys = append(group.participantKeys, record.ParticipantKeys...)
	}
	merged := whomatch.MergeSameName(candidates)
	out := make([]whoCandidateRecord, 0, len(merged))
	for _, candidate := range merged {
		group := extras[whomatch.Normalize(candidate.Who)]
		if group == nil {
			continue
		}
		out = append(out, whoCandidateRecord{
			WhoCandidate: WhoCandidate{
				Who:             candidate.Who,
				Identifiers:     sortedUniqueValues(candidate.Identifiers),
				LastSeen:        candidate.LastSeen,
				Messages:        int(candidate.Messages),
				ParticipantKeys: sortedUniqueValues(group.participantKeys),
			},
			aliases: sortedUniqueValues(append(group.aliases, candidate.Aliases...)),
		})
	}
	return out
}

func (s *Store) readWhoCandidateAliases(ctx context.Context) (map[string]*whoCandidateBuilder, error) {
	rows, err := s.db.QueryContext(ctx, whoCandidateAliasesQuery())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	builders := map[string]*whoCandidateBuilder{}
	for rows.Next() {
		var key, participantKey, displayName, identifier, nameKind string
		if err := rows.Scan(&key, &participantKey, &displayName, &identifier, &nameKind); err != nil {
			return nil, err
		}
		builder := whoBuilder(builders, key)
		if value := normalizeWhoIdentity(participantKey); value != "" {
			builder.participantKeys[strings.ToLower(value)] = value
		}
		if value := normalizeWhoIdentity(displayName); value != "" {
			builder.addName(value, nameKind)
		}
		if value := normalizeWhoIdentifier(identifier); value != "" {
			builder.identifiers[strings.ToLower(value)] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return builders, nil
}

func (s *Store) readWhoCandidateStats(ctx context.Context, builders map[string]*whoCandidateBuilder) error {
	rows, err := s.db.QueryContext(ctx, whoCandidateStatsQuery())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var lastSeen int64
		var messages int
		if err := rows.Scan(&key, &lastSeen, &messages); err != nil {
			return err
		}
		builder := whoBuilder(builders, key)
		seen := fromUnix(lastSeen)
		if builder.lastSeen.IsZero() || seen.After(builder.lastSeen) {
			builder.lastSeen = seen
		}
		builder.messages = messages
	}
	return rows.Err()
}

func whoBuilder(builders map[string]*whoCandidateBuilder, key string) *whoCandidateBuilder {
	key = strings.TrimSpace(key)
	if builder, ok := builders[key]; ok {
		return builder
	}
	builder := &whoCandidateBuilder{
		key:             key,
		names:           map[string]*whoNameEvidence{},
		identifiers:     map[string]string{},
		participantKeys: map[string]string{},
	}
	builders[key] = builder
	return builder
}

func (b *whoCandidateBuilder) addName(value, kind string) {
	value = normalizeWhoIdentity(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	evidence, ok := b.names[key]
	if !ok {
		evidence = &whoNameEvidence{value: value}
		b.names[key] = evidence
	}
	switch kind {
	case "contact_full":
		evidence.contactFull = true
	case "push":
		evidence.pushCount++
	}
}

func (b *whoCandidateBuilder) nameValues() []string {
	return sortedWhoNameValues(b.names)
}

func chooseWhoName(names map[string]*whoNameEvidence, identifiers []string) string {
	if name := firstCleanContactFullName(names); name != "" {
		return name
	}
	if name := mostFrequentCleanPushName(names); name != "" {
		return name
	}
	for _, name := range sortedWhoNameValues(names) {
		if humanWhoName(name) {
			return name
		}
	}
	for _, identifier := range identifiers {
		if strings.HasPrefix(identifier, "@") {
			return identifier
		}
	}
	for _, identifier := range identifiers {
		if !strings.Contains(identifier, "@") {
			return identifier
		}
	}
	if len(identifiers) > 0 {
		return identifiers[0]
	}
	if names := sortedWhoNameValues(names); len(names) > 0 {
		return names[0]
	}
	return ""
}

func firstCleanContactFullName(names map[string]*whoNameEvidence) string {
	var choices []string
	for _, name := range names {
		if name.contactFull && humanWhoName(name.value) {
			choices = append(choices, name.value)
		}
	}
	sort.Strings(choices)
	if len(choices) == 0 {
		return ""
	}
	return choices[0]
}

func mostFrequentCleanPushName(names map[string]*whoNameEvidence) string {
	type choice struct {
		value string
		count int
	}
	var choices []choice
	for _, name := range names {
		if name.pushCount > 0 && humanWhoName(name.value) {
			choices = append(choices, choice{value: name.value, count: name.pushCount})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].count != choices[j].count {
			return choices[i].count > choices[j].count
		}
		left := strings.ToLower(choices[i].value)
		right := strings.ToLower(choices[j].value)
		if left != right {
			return left < right
		}
		return choices[i].value < choices[j].value
	})
	if len(choices) == 0 {
		return ""
	}
	return choices[0].value
}

func humanWhoName(value string) bool {
	value = normalizeWhoIdentity(value)
	if value == "" || strings.HasPrefix(value, "@") || strings.Contains(value, "@") || looksLikeIdentifierPhone(value) {
		return false
	}
	if looksLikeBase64Name(value) {
		return false
	}
	hasLetter := false
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	if !hasLetter {
		return false
	}
	return true
}

// HumanWhoName reports whether value is safe to display as a person's name.
func HumanWhoName(value string) bool {
	return humanWhoName(value)
}

func looksLikeBase64Name(value string) bool {
	if strings.Contains(value, " ") || len(value) < 4 {
		return false
	}
	hasBase64Punctuation := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '+', r == '/', r == '=':
			hasBase64Punctuation = true
		default:
			return false
		}
	}
	return hasBase64Punctuation
}

func looksLikeIdentifierPhone(value string) bool {
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}

func sortedWhoNameValues(names map[string]*whoNameEvidence) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name.value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i])
		right := strings.ToLower(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	return out
}

func sortedValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i])
		right := strings.ToLower(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	return out
}

func sortedUniqueValues(values []string) []string {
	byValue := map[string]string{}
	for _, value := range values {
		value = normalizeWhoIdentity(value)
		if value == "" {
			continue
		}
		byValue[strings.ToLower(value)] = value
	}
	return sortedValues(byValue)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeWhoIdentity(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (s *Store) withCanonicalSenderNames(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	names, err := s.canonicalSenderNames(ctx)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		if messages[i].FromMe {
			continue
		}
		if name := names.lookupMessage(messages[i]); name != "" {
			messages[i].SenderName = name
		}
	}
	return messages, nil
}

type canonicalSenderNames map[string]string

func (s *Store) canonicalSenderNames(ctx context.Context) (canonicalSenderNames, error) {
	records, err := s.whoCandidateRecordsWithoutNameMerge(ctx)
	if err != nil {
		return nil, err
	}
	names := canonicalSenderNames{}
	for _, record := range records {
		name := normalizeWhoIdentity(record.Who)
		if name == "" {
			continue
		}
		for _, key := range record.ParticipantKeys {
			names.addParticipantKey(key, name)
		}
		for _, identifier := range record.Identifiers {
			names.addIdentifier(identifier, name)
		}
	}
	return names, nil
}

func (n canonicalSenderNames) addParticipantKey(value, name string) {
	key := canonicalSenderParticipantKey(value)
	if key == "" || n[key] != "" {
		return
	}
	n[key] = name
}

func (n canonicalSenderNames) addIdentifier(value, name string) {
	key := canonicalSenderIdentifierKey(value)
	if key == "" || n[key] != "" {
		return
	}
	n[key] = name
}

func (n canonicalSenderNames) lookupMessage(message Message) string {
	for _, key := range canonicalSenderLookupKeys(message) {
		if name := n[key]; name != "" {
			return name
		}
	}
	return ""
}

func canonicalSenderLookupKeys(message Message) []string {
	var keys []string
	if senderJID := normalizeWhoIdentifier(message.SenderJID); senderJID != "" {
		keys = append(keys, canonicalSenderIdentifierKey(senderJID))
		keys = append(keys, canonicalSenderParticipantKey("jid:"+senderJID))
	}
	if senderName := normalizeWhoIdentity(message.SenderName); senderName != "" {
		keys = append(keys, canonicalSenderParticipantKey("sender:"+senderName))
	}
	return keys
}

func canonicalSenderParticipantKey(value string) string {
	value = normalizeWhoIdentity(value)
	if value == "" {
		return ""
	}
	return "participant:" + strings.ToLower(value)
}

func canonicalSenderIdentifierKey(value string) string {
	value = normalizeWhoIdentifier(value)
	if value == "" {
		return ""
	}
	return "identifier:" + strings.ToLower(value)
}

func (s *Store) MessageByID(ctx context.Context, messageID string) (Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return Message{}, errors.New("message id required")
	}
	messages, err := scanMessages(ctx, s.db, "select "+messageSelectColumns+" from messages where msg_id = ? order by ts desc, source_pk desc limit 1", messageID)
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, sql.ErrNoRows
	}
	messages, err = s.withCanonicalSenderNames(ctx, messages)
	if err != nil {
		return Message{}, err
	}
	return messages[0], nil
}

func (s *Store) MessageWindow(ctx context.Context, target Message, eachSide int) ([]Message, error) {
	if eachSide < 0 {
		eachSide = 0
	}
	before, err := s.messagesBefore(ctx, target, eachSide)
	if err != nil {
		return nil, err
	}
	after, err := s.messagesAfter(ctx, target, eachSide)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(before)+1+len(after))
	out = append(out, before...)
	out = append(out, target)
	out = append(out, after...)
	return s.withCanonicalSenderNames(ctx, out)
}

func (s *Store) messagesBefore(ctx context.Context, target Message, limit int) ([]Message, error) {
	if limit == 0 {
		return nil, nil
	}
	if target.Timestamp.IsZero() {
		query := "select " + messageScanColumns + " from (select " + messageSelectColumns + " from messages where chat_jid = ? and source_pk < ? order by source_pk desc limit ?) order by source_pk asc"
		return scanMessages(ctx, s.db, query, target.ChatJID, target.SourcePK, limit)
	}
	query := "select " + messageScanColumns + " from (select " + messageSelectColumns + " from messages where chat_jid = ? and (ts < ? or (ts = ? and source_pk < ?)) order by ts desc, source_pk desc limit ?) order by ts asc, source_pk asc"
	ts := unix(target.Timestamp)
	return scanMessages(ctx, s.db, query, target.ChatJID, ts, ts, target.SourcePK, limit)
}

func (s *Store) messagesAfter(ctx context.Context, target Message, limit int) ([]Message, error) {
	if limit == 0 {
		return nil, nil
	}
	if target.Timestamp.IsZero() {
		query := "select " + messageSelectColumns + " from messages where chat_jid = ? and source_pk > ? order by source_pk asc limit ?"
		return scanMessages(ctx, s.db, query, target.ChatJID, target.SourcePK, limit)
	}
	query := "select " + messageSelectColumns + " from messages where chat_jid = ? and (ts > ? or (ts = ? and source_pk > ?)) order by ts asc, source_pk asc limit ?"
	ts := unix(target.Timestamp)
	return scanMessages(ctx, s.db, query, target.ChatJID, ts, ts, target.SourcePK, limit)
}

func applyMessageFilters(query string, args []any, filter MessageFilter, joined bool) (string, []any) {
	prefix := ""
	if joined {
		prefix = "m."
	}
	if strings.TrimSpace(filter.ChatJID) != "" {
		query += " and " + prefix + "chat_jid = ?"
		args = append(args, filter.ChatJID)
	}
	if strings.TrimSpace(filter.Sender) != "" {
		query += " and " + prefix + "sender_jid = ?"
		args = append(args, filter.Sender)
	}
	if filter.After != nil {
		query += " and " + prefix + "ts >= ?"
		args = append(args, unix(*filter.After))
	}
	if filter.Before != nil {
		if filter.BeforePK > 0 {
			query += " and (" + prefix + "ts < ? or (" + prefix + "ts = ? and " + prefix + "source_pk < ?))"
			args = append(args, unix(*filter.Before), unix(*filter.Before), filter.BeforePK)
		} else {
			query += " and " + prefix + "ts <= ?"
			args = append(args, unix(*filter.Before))
		}
	}
	if filter.FromMe != nil {
		query += " and " + prefix + "from_me = ?"
		args = append(args, boolInt(*filter.FromMe))
	}
	if filter.HasMedia {
		query += " and (" + prefix + "media_type <> '' or " + prefix + "media_path <> '' or " + prefix + "media_url <> '')"
	}
	if filter.WhoKeys != nil {
		if len(filter.WhoKeys) == 0 {
			query += " and 0=1"
		} else {
			query += " and exists (select 1 from (" + whoMessageParticipantKeysQuery(prefix) + ") where participant_key in (" + queryPlaceholders(len(filter.WhoKeys)) + "))"
			for _, key := range filter.WhoKeys {
				args = append(args, key)
			}
		}
	}
	return query, args
}

func queryPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	placeholders := make([]string, count)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return strings.Join(placeholders, ",")
}

func normalizeWhoIdentity(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeWhoIdentifier(value string) string {
	value = normalizeWhoIdentity(value)
	for {
		lower := strings.ToLower(value)
		if !strings.HasSuffix(lower, "@lid@lid") {
			return value
		}
		value = value[:len(value)-len("@lid")]
	}
}

func whoCandidateAliasesQuery() string {
	senderContact := contactJIDPredicate("c", "m.sender_jid")
	chatContact := contactJIDPredicate("c", "ch.jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select participant_key as identity_key, participant_key, display_name, '' as identifier, name_kind
from (
select '` + ownerWhoKey + `' as participant_key, 'me' as display_name, 'owner' as name_kind
where exists (select 1 from messages where from_me = 1)
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end as participant_key, m.sender_name as display_name, 'push' as name_kind
from messages m
left join contacts c on ` + senderContact + `
where m.from_me = 0 and trim(m.sender_name) <> ''
union all
select 'jid:' || jid, full_name, 'contact_full'
from contacts
where trim(jid) <> '' and trim(full_name) <> ''
union all
select 'jid:' || jid, business_name, 'other'
from contacts
where trim(jid) <> '' and trim(business_name) <> ''
union all
select 'jid:' || jid, trim(first_name || ' ' || last_name), 'other'
from contacts
where trim(jid) <> '' and trim(first_name || ' ' || last_name) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), ch.name, 'other'
from chats ch
left join contacts c on ` + chatContact + `
where ch.kind <> 'group' and trim(ch.jid) <> '' and trim(ch.name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.contact_name, 'other'
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> '' and trim(gp.contact_name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.first_name, 'other'
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> '' and trim(gp.first_name) <> ''
union all
select 'jid:' || c.jid, c.full_name, 'contact_full'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.full_name) <> ''
union all
select 'jid:' || c.jid, c.business_name, 'other'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.business_name) <> ''
union all
select 'jid:' || c.jid, trim(c.first_name || ' ' || c.last_name), 'other'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.first_name || ' ' || c.last_name) <> ''
)
where trim(participant_key) <> '' and trim(display_name) <> ''
union all
select participant_key as identity_key, participant_key, '' as display_name, identifier, '' as name_kind
from (
select '` + ownerWhoKey + `' as participant_key, 'me' as identifier
where exists (select 1 from messages where from_me = 1)
union all
select 'jid:' || jid as participant_key, jid as identifier from contacts where trim(jid) <> ''
union all
select 'jid:' || jid, phone from contacts where trim(jid) <> '' and trim(phone) <> ''
union all
select 'jid:' || jid, username from contacts where trim(jid) <> '' and trim(username) <> ''
union all
select 'jid:' || jid, case when substr(username, 1, 1) = '@' then username else '@' || username end from contacts where trim(jid) <> '' and trim(username) <> ''
union all
select 'jid:' || jid, lid from contacts where trim(jid) <> '' and trim(lid) <> ''
union all
select 'jid:' || jid, lid || '@lid' from contacts where trim(jid) <> '' and trim(lid) <> ''
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end, m.sender_jid
from messages m
left join contacts c on ` + senderContact + `
where trim(m.sender_jid) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), ch.jid
from chats ch
left join contacts c on ` + chatContact + `
where ch.kind <> 'group' and trim(ch.jid) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.user_jid
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> ''
)
where trim(participant_key) <> '' and trim(identifier) <> ''`
}

func whoCandidateStatsQuery() string {
	senderContact := contactJIDPredicate("c", "m.sender_jid")
	chatContact := contactJIDPredicate("c", "ch.jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select participant_key, max(ts) as last_seen, count(distinct source_pk) as messages
from (
select '` + ownerWhoKey + `' as participant_key, source_pk, ts
from messages
where from_me = 1
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end as participant_key, m.source_pk, m.ts
from messages m
left join contacts c on ` + senderContact + `
where trim(m.sender_jid) <> '' or trim(m.sender_name) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), m.source_pk, m.ts
from messages m
join chats ch on ch.jid = m.chat_jid and ch.kind <> 'group'
left join contacts c on ` + chatContact + `
where trim(ch.jid) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), m.source_pk, m.ts
from messages m
join group_participants gp on gp.group_jid = m.chat_jid
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> ''
)
where trim(participant_key) <> ''
group by participant_key`
}

func whoMessageParticipantKeysQuery(prefix string) string {
	senderContact := contactJIDPredicate("c", prefix+"sender_jid")
	chatContact := contactJIDPredicate("c", prefix+"chat_jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select case when trim(` + prefix + `sender_jid) <> '' then 'jid:' || coalesce((select c.jid from contacts c where ` + senderContact + ` limit 1), ` + prefix + `sender_jid) else 'sender:' || trim(` + prefix + `sender_name) end as participant_key
where trim(` + prefix + `sender_jid) <> '' or trim(` + prefix + `sender_name) <> ''
union all
select '` + ownerWhoKey + `'
where ` + prefix + `from_me = 1
union all
select 'jid:' || coalesce(c.jid, ch.jid)
from chats ch
left join contacts c on ` + chatContact + `
where ch.jid = ` + prefix + `chat_jid and ch.kind <> 'group'
union all
select 'jid:' || coalesce(c.jid, gp.user_jid)
from group_participants gp
left join contacts c on ` + groupContact + `
where gp.group_jid = ` + prefix + `chat_jid`
}

func contactJIDPredicate(contactAlias, jidExpr string) string {
	return contactAlias + ".jid = " + jidExpr + " or " + contactAlias + ".lid = " + jidExpr + " or " + contactAlias + ".lid || '@lid' = " + jidExpr
}

func scanMessages(ctx context.Context, db *sql.DB, query string, args ...any) ([]Message, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		var fromMe, starred int
		if err := rows.Scan(&m.SourcePK, &m.ChatJID, &m.ChatName, &m.MessageID, &m.SenderJID, &m.SenderName, &ts, &fromMe, &m.Text, &m.RawType, &m.MessageType, &m.MediaType, &m.MediaTitle, &m.MediaPath, &m.MediaURL, &m.MediaSize, &starred, &m.Snippet); err != nil {
			return nil, err
		}
		m.Timestamp = fromUnix(ts)
		m.FromMe = fromMe != 0
		m.Starred = starred != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func messageSnippetText(message Message) string {
	return strings.TrimSpace(message.Text + " " + message.MediaTitle)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func fromUnix(v int64) time.Time {
	if !validUnixTimestamp(v) {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func validUnixTimestamp(v int64) bool {
	return v > 0 && v <= maxJSONUnixSecond
}

func validUnixPredicate(column string) string {
	return fmt.Sprintf("%s > 0 and %s <= %d", column, column, maxJSONUnixSecond)
}

func invalidUnixPredicate(column string) string {
	return fmt.Sprintf("(%s <= 0 or %s > %d)", column, column, maxJSONUnixSecond)
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: true}
}

func nullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func countInt(ctx context.Context, count func(context.Context) (int64, error)) (int, error) {
	v, err := count(ctx)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func chatFromRow(row storedb.ListChatsRow) Chat {
	return Chat{
		JID:            row.Jid,
		Kind:           row.Kind,
		Name:           row.Name,
		LastMessageAt:  fromUnix(row.LastMessageAt),
		UnreadCount:    int(row.UnreadCount),
		Archived:       row.Archived != 0,
		Removed:        row.Removed != 0,
		Hidden:         row.Hidden != 0,
		RawSessionType: int(row.RawSessionType),
		MessageCount:   int(row.MessageCount),
	}
}

func unreadChatFromRow(row storedb.ListUnreadChatsRow) Chat {
	return Chat{
		JID:            row.Jid,
		Kind:           row.Kind,
		Name:           row.Name,
		LastMessageAt:  fromUnix(row.LastMessageAt),
		UnreadCount:    int(row.UnreadCount),
		Archived:       row.Archived != 0,
		Removed:        row.Removed != 0,
		Hidden:         row.Hidden != 0,
		RawSessionType: int(row.RawSessionType),
		MessageCount:   int(row.MessageCount),
	}
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
