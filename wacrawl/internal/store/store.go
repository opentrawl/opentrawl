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

	"github.com/openclaw/crawlkit/shortref"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/openclaw/wacrawl/internal/sqlitedsn"
	"github.com/openclaw/wacrawl/internal/store/storedb"
	_ "modernc.org/sqlite"
)

const (
	schemaVersion     = 1
	maxJSONUnixSecond = 253402300799 // 9999-12-31T23:59:59Z, the largest time.Time JSON can marshal.

	MessageRefPrefix = "wacrawl:msg/"

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
	if strings.TrimSpace(filter.Query) == "" {
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
	return messages, nil
}

func (s *Store) SearchCount(ctx context.Context, filter MessageFilter) (int, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return 0, errors.New("search query required")
	}
	var err error
	filter, err = s.resolveMessageFilterWho(ctx, filter)
	if err != nil {
		return 0, err
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

func (s *Store) resolveMessageFilterWho(ctx context.Context, filter MessageFilter) (MessageFilter, error) {
	if normalizeWhoIdentity(filter.Who) == "" || filter.WhoKeys != nil {
		return filter, nil
	}
	resolution, err := s.ResolveWho(ctx, filter.Who)
	if err != nil {
		return MessageFilter{}, err
	}
	filter.Who = normalizeWhoIdentity(filter.Who)
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
	identity = normalizeWhoIdentity(identity)
	if identity == "" {
		return WhoResolution{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
select participant_key, display_name
from (
	select distinct participant_key, trim(display_name) as display_name
	from (`+whoArchiveParticipantNamesQuery()+`)
)
where display_name <> ''
order by display_name, participant_key`)
	if err != nil {
		return WhoResolution{}, err
	}
	defer func() { _ = rows.Close() }()
	type whoCandidate struct {
		key  string
		name string
	}
	var matches []whoCandidate
	seenKeys := map[string]struct{}{}
	for rows.Next() {
		var candidate whoCandidate
		if err := rows.Scan(&candidate.key, &candidate.name); err != nil {
			return WhoResolution{}, err
		}
		candidate.name = strings.TrimSpace(candidate.name)
		if _, seen := seenKeys[candidate.key]; seen || !strings.EqualFold(candidate.name, identity) {
			continue
		}
		seenKeys[candidate.key] = struct{}{}
		matches = append(matches, candidate)
	}
	if err := rows.Err(); err != nil {
		return WhoResolution{}, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := strings.ToLower(matches[i].name)
		right := strings.ToLower(matches[j].name)
		if left != right {
			return left < right
		}
		if matches[i].name != matches[j].name {
			return matches[i].name < matches[j].name
		}
		return matches[i].key < matches[j].key
	})
	resolution := WhoResolution{
		ParticipantKeys: make([]string, 0, len(matches)),
		DisplayNames:    make([]string, 0, len(matches)),
	}
	for _, match := range matches {
		resolution.ParticipantKeys = append(resolution.ParticipantKeys, match.key)
		resolution.DisplayNames = append(resolution.DisplayNames, match.name)
	}
	return resolution, nil
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
	return out, nil
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

func whoArchiveParticipantNamesQuery() string {
	senderContact := contactJIDPredicate("c", "m.sender_jid")
	chatContact := contactJIDPredicate("c", "ch.jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end as participant_key, m.sender_name as display_name
from messages m
left join contacts c on ` + senderContact + `
where trim(m.sender_name) <> ''
union all
select 'jid:' || jid, full_name
from contacts
where trim(full_name) <> ''
union all
select 'jid:' || jid, business_name
from contacts
where trim(business_name) <> ''
union all
select 'jid:' || jid, trim(first_name || ' ' || last_name)
from contacts
where trim(first_name || ' ' || last_name) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), ch.name
from chats ch
left join contacts c on ` + chatContact + `
where ch.kind <> 'group' and trim(ch.name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.contact_name
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.contact_name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.first_name
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.first_name) <> ''
union all
select 'jid:' || c.jid, c.full_name
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.full_name) <> ''
union all
select 'jid:' || c.jid, c.business_name
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.business_name) <> ''
union all
select 'jid:' || c.jid, trim(c.first_name || ' ' || c.last_name)
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.first_name || ' ' || c.last_name) <> ''`
}

func whoMessageParticipantKeysQuery(prefix string) string {
	senderContact := contactJIDPredicate("c", prefix+"sender_jid")
	chatContact := contactJIDPredicate("c", prefix+"chat_jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select case when trim(` + prefix + `sender_jid) <> '' then 'jid:' || coalesce((select c.jid from contacts c where ` + senderContact + ` limit 1), ` + prefix + `sender_jid) else 'sender:' || trim(` + prefix + `sender_name) end as participant_key
where trim(` + prefix + `sender_jid) <> '' or trim(` + prefix + `sender_name) <> ''
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
