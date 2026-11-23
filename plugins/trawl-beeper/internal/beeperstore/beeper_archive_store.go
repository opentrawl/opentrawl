package beeperstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"

	// C SQLite via cgo with FTS5 enabled, matching trawlkit/store.
	_ "github.com/mattn/go-sqlite3"
)

const (
	// BeeperMessageRefPrefix is the canonical reference prefix for one archived Beeper message.
	BeeperMessageRefPrefix = "beeper:msg/"
	// BeeperChatRefPrefix is the canonical reference prefix for one archived Beeper conversation.
	BeeperChatRefPrefix = "beeper:chat/"

	beeperUpdateSource           = "beeper"
	beeperUpdateEntityType       = "update"
	beeperStateLastCompletedAt   = "last_completed_update_at"
	beeperStateLastSyncError     = "last_sync_error"
	beeperStateLastSyncAttemptAt = "last_sync_attempt_at"
)

// BeeperArchiveStore owns the source-native SQLite archive for trawl-beeper.
type BeeperArchiveStore struct {
	store *ckstore.Store
	db    *sql.DB
	path  string
	owned bool
}

// BeeperAccountRecord holds one archived Beeper account row.
type BeeperAccountRecord struct {
	AccountID    string
	Network      string
	Status       string
	StatusText   string
	LoginID      string
	SelfUserID   string
	SelfFullName string
	SelfUsername string
	UpdatedAt    time.Time
}

// BeeperArchivedChat holds one archived Beeper conversation row.
type BeeperArchivedChat struct {
	ChatID        string
	LocalChatID   string
	AccountID     string
	Network       string
	Title         string
	ChatType      string
	Description   string
	IsArchived    bool
	IsMuted       bool
	IsPinned      bool
	IsReadOnly    bool
	IsLowPriority bool
	UnreadCount   int
	LastActivity  time.Time
	UpdatedAt     time.Time
}

// BeeperArchivedParticipant holds one archived chat participant row.
type BeeperArchivedParticipant struct {
	ChatID       string
	UserID       string
	FullName     string
	Username     string
	PhoneNumber  string
	Email        string
	IsSelf       bool
	IsAdmin      bool
	IsPending    bool
	IsNetworkBot bool
}

// BeeperArchivedMessage holds one archived Beeper message row with chat context.
type BeeperArchivedMessage struct {
	RowID            int64
	MessageID        string
	ChatID           string
	AccountID        string
	Network          string
	ChatTitle        string
	ChatType         string
	SortKey          string
	SenderID         string
	SenderName       string
	IsSender         bool
	Timestamp        time.Time
	EditedTimestamp  time.Time
	MessageType      string
	Text             string
	LinkedMessageID  string
	IsDeleted        bool
	IsHidden         bool
	MediaKind        string
	MediaTitle       string
	MediaMimeType    string
	MediaURL         string
	MediaSizeBytes   int64
	MediaTranscript  string
	LinksSummary     string
	ReactionsSummary string
	SearchMatches    []BeeperMessageSearchMatch
}

// BeeperMessageSearchMatch holds highlighted FTS5 runs for one matched field.
type BeeperMessageSearchMatch struct {
	Field string
	Runs  []ckstore.FTS5TextRun
}

// BeeperChatSyncState tracks incremental high-water marks and resumable backfill
// cursors for one Beeper conversation.
type BeeperChatSyncState struct {
	ChatID             string
	NewestSortKey      string
	NewestMessageID    string
	OldestCursor       string
	BackfillComplete   bool
	LastSyncedActivity time.Time
	LastSyncedAt       time.Time
}

// BeeperArchiveStatus summarizes the local Beeper archive state.
type BeeperArchiveStatus struct {
	AccountCount    int
	ChatCount       int
	MessageCount    int
	OldestMessageAt time.Time
	NewestMessageAt time.Time
	LastCompletedAt time.Time
	LastSyncError   string
	LastAttemptAt   time.Time
}

// BeeperArchivedNetworkSummary summarizes archived volume for one Beeper network.
type BeeperArchivedNetworkSummary struct {
	Network           string
	AccountStatus     string
	ConversationCount int
	MessageCount      int
	NewestMessageAt   time.Time
}

// BeeperConversationParticipantIdentity pairs a display name with exact person
// filter identifiers for conversation listing.
type BeeperConversationParticipantIdentity struct {
	PersonDisplayName                                    string
	ExactPersonFilterIdentifiersObservedByTrawlerArchive []*person.ExactPersonFilterIdentifier
}

// BeeperMessageQueryFilter filters Beeper messages during search or list queries.
type BeeperMessageQueryFilter struct {
	Query                    string
	ChatID                   string
	Network                  string
	Sender                   string
	PersonFilter             trawlkit.SearchPersonFilter
	ResolvedParticipantIDs   []string
	ResolvedParticipantNames []string
	Limit                    int
	After                    *time.Time
	Before                   *time.Time
	IsSender                 *bool
	HasMedia                 bool
}

// BeeperMessageContextWindow holds messages surrounding an opened message.
type BeeperMessageContextWindow struct {
	Messages        []BeeperArchivedMessage
	BeforeTruncated bool
	AfterTruncated  bool
}

// BeeperWhoCandidate summarizes one resolved person across Beeper conversations.
type BeeperWhoCandidate struct {
	DisplayName  string
	Identifiers  []string
	UserIDs      []string
	Networks     []string
	PhoneNumbers []string
	Emails       []string
	LastSeen     time.Time
	MessageCount int
	MatchRank    whomatch.Rank
}

// BeeperWhoResolution holds candidate people resolved for a query.
type BeeperWhoResolution struct {
	Candidates []BeeperWhoCandidate
	MatchRank  whomatch.Rank
}

// OnlyCloseSpellingMatch reports whether every candidate matched only via fuzzy spelling.
func (r BeeperWhoResolution) OnlyCloseSpellingMatch() bool {
	return len(r.Candidates) > 0 && r.MatchRank == whomatch.RankCloseSpelling
}

// BeeperMessageRef formats the canonical record reference for one Beeper message.
func BeeperMessageRef(messageID string) string {
	trimmed := strings.TrimSpace(messageID)
	if trimmed == "" {
		return ""
	}
	return BeeperMessageRefPrefix + trimmed
}

// BeeperChatRef formats the canonical record reference for one Beeper conversation.
func BeeperChatRef(chatID string) string {
	trimmed := strings.TrimSpace(chatID)
	if trimmed == "" {
		return ""
	}
	return BeeperChatRefPrefix + trimmed
}

// FormatNetworkChatDisplayTitle returns a human-readable conversation title that
// always names the underlying Beeper network when known.
func FormatNetworkChatDisplayTitle(network string, chatTitle string) string {
	cleanNetwork := strings.TrimSpace(network)
	cleanTitle := strings.TrimSpace(chatTitle)
	if cleanNetwork == "" {
		if cleanTitle == "" {
			return "Beeper conversation"
		}
		return cleanTitle
	}
	if cleanTitle == "" {
		return cleanNetwork + " conversation"
	}
	lowerNet := strings.ToLower(cleanNetwork)
	lowerTitle := strings.ToLower(cleanTitle)
	if strings.HasPrefix(lowerTitle, lowerNet+" ·") || strings.HasPrefix(lowerTitle, "["+lowerNet+"]") {
		return cleanTitle
	}
	return cleanNetwork + " · " + cleanTitle
}

// Open opens or creates the Beeper SQLite archive at path.
func Open(ctx context.Context, path string) (*BeeperArchiveStore, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, errors.New("beeper archive path is required")
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: trimmed})
	if err != nil {
		return nil, err
	}
	wrapped := &BeeperArchiveStore{store: st, db: st.DB(), path: trimmed, owned: true}
	if err := wrapped.applySchema(ctx); err != nil {
		_ = st.Close()
		return nil, err
	}
	return wrapped, nil
}

// Use wraps an already-opened read-write trawlkit store and ensures the Beeper schema exists.
func Use(ctx context.Context, st *ckstore.Store, path string) (*BeeperArchiveStore, error) {
	if st == nil {
		return nil, errors.New("beeper archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	wrapped := &BeeperArchiveStore{store: st, db: st.DB(), path: path, owned: false}
	if err := wrapped.applySchema(ctx); err != nil {
		return nil, err
	}
	return wrapped, nil
}

// UseExisting wraps an already-opened read-only trawlkit store without modifying disk.
func UseExisting(st *ckstore.Store, path string) (*BeeperArchiveStore, error) {
	if st == nil {
		return nil, errors.New("beeper archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &BeeperArchiveStore{store: st, db: st.DB(), path: path, owned: false}, nil
}

// Close closes the underlying store if owned by this wrapper.
func (s *BeeperArchiveStore) Close() error {
	if s == nil || s.store == nil || !s.owned {
		return nil
	}
	return s.store.Close()
}

func (s *BeeperArchiveStore) applySchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, beeperArchiveSchemaSQL); err != nil {
		return fmt.Errorf("apply beeper archive schema: %w", err)
	}
	if err := state.EnsureSchema(ctx, s.db); err != nil {
		return fmt.Errorf("apply beeper update_state schema: %w", err)
	}
	return nil
}

// UpsertAccounts inserts or updates Beeper account records atomically.
func (s *BeeperArchiveStore) UpsertAccounts(ctx context.Context, accounts []BeeperAccountRecord) error {
	if len(accounts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
insert into beeper_accounts (
	account_id, network, status, status_text, login_id,
	self_user_id, self_full_name, self_username, updated_at_unix
) values (?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(account_id) do update set
	network = excluded.network,
	status = excluded.status,
	status_text = excluded.status_text,
	login_id = excluded.login_id,
	self_user_id = excluded.self_user_id,
	self_full_name = excluded.self_full_name,
	self_username = excluded.self_username,
	updated_at_unix = excluded.updated_at_unix
`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, acc := range accounts {
		if strings.TrimSpace(acc.AccountID) == "" {
			continue
		}
		if _, err := stmt.ExecContext(
			ctx,
			acc.AccountID,
			acc.Network,
			acc.Status,
			acc.StatusText,
			acc.LoginID,
			acc.SelfUserID,
			acc.SelfFullName,
			acc.SelfUsername,
			unixOrZero(acc.UpdatedAt),
		); err != nil {
			return fmt.Errorf("upsert beeper account %q: %w", acc.AccountID, err)
		}
	}
	return tx.Commit()
}

// UpsertChatAndParticipants writes one chat and its observed participants atomically.
func (s *BeeperArchiveStore) UpsertChatAndParticipants(
	ctx context.Context,
	chat BeeperArchivedChat,
	participants []BeeperArchivedParticipant,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
insert into beeper_chats (
	chat_id, local_chat_id, account_id, network, title, chat_type, description,
	is_archived, is_muted, is_pinned, is_read_only, is_low_priority,
	unread_count, last_activity_unix, updated_at_unix
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(chat_id) do update set
	local_chat_id = excluded.local_chat_id,
	account_id = excluded.account_id,
	network = excluded.network,
	title = excluded.title,
	chat_type = excluded.chat_type,
	description = excluded.description,
	is_archived = excluded.is_archived,
	is_muted = excluded.is_muted,
	is_pinned = excluded.is_pinned,
	is_read_only = excluded.is_read_only,
	is_low_priority = excluded.is_low_priority,
	unread_count = excluded.unread_count,
	last_activity_unix = excluded.last_activity_unix,
	updated_at_unix = excluded.updated_at_unix
`,
		chat.ChatID,
		chat.LocalChatID,
		chat.AccountID,
		chat.Network,
		chat.Title,
		chat.ChatType,
		chat.Description,
		boolToSQLiteInt(chat.IsArchived),
		boolToSQLiteInt(chat.IsMuted),
		boolToSQLiteInt(chat.IsPinned),
		boolToSQLiteInt(chat.IsReadOnly),
		boolToSQLiteInt(chat.IsLowPriority),
		chat.UnreadCount,
		unixOrZero(chat.LastActivity),
		unixOrZero(chat.UpdatedAt),
	); err != nil {
		return fmt.Errorf("upsert beeper chat %q: %w", chat.ChatID, err)
	}

	if len(participants) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
insert into beeper_chat_participants (
	chat_id, user_id, full_name, username, phone_number, email,
	is_self, is_admin, is_pending, is_network_bot
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(chat_id, user_id) do update set
	full_name = case when excluded.full_name <> '' then excluded.full_name else beeper_chat_participants.full_name end,
	username = case when excluded.username <> '' then excluded.username else beeper_chat_participants.username end,
	phone_number = case when excluded.phone_number <> '' then excluded.phone_number else beeper_chat_participants.phone_number end,
	email = case when excluded.email <> '' then excluded.email else beeper_chat_participants.email end,
	is_self = excluded.is_self,
	is_admin = excluded.is_admin,
	is_pending = excluded.is_pending,
	is_network_bot = excluded.is_network_bot
`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()

		for _, participant := range participants {
			if strings.TrimSpace(participant.UserID) == "" {
				continue
			}
			if _, err := stmt.ExecContext(
				ctx,
				chat.ChatID,
				participant.UserID,
				participant.FullName,
				participant.Username,
				participant.PhoneNumber,
				participant.Email,
				boolToSQLiteInt(participant.IsSelf),
				boolToSQLiteInt(participant.IsAdmin),
				boolToSQLiteInt(participant.IsPending),
				boolToSQLiteInt(participant.IsNetworkBot),
			); err != nil {
				return fmt.Errorf("upsert beeper participant %q: %w", participant.UserID, err)
			}
		}
	}

	return tx.Commit()
}

// UpsertMessagesAndSyncState atomically commits a batch of messages (plus their
// FTS5 rows) and updates the chat's incremental sync state.
func (s *BeeperArchiveStore) UpsertMessagesAndSyncState(
	ctx context.Context,
	messages []BeeperArchivedMessage,
	chatTitle string,
	syncState *BeeperChatSyncState,
) (insertedCount int, updatedCount int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, msg := range messages {
		if strings.TrimSpace(msg.MessageID) == "" || strings.TrimSpace(msg.ChatID) == "" {
			continue
		}
		var existingRowID int64
		lookupErr := tx.QueryRowContext(
			ctx,
			`select rowid from beeper_messages where message_id = ?`,
			msg.MessageID,
		).Scan(&existingRowID)

		var targetRowID int64
		switch {
		case errors.Is(lookupErr, sql.ErrNoRows):
			res, execErr := tx.ExecContext(ctx, `
insert into beeper_messages (
	message_id, chat_id, account_id, network, sort_key,
	sender_id, sender_name, is_sender, timestamp_unix, edited_timestamp_unix,
	message_type, text, linked_message_id, is_deleted, is_hidden,
	media_kind, media_title, media_mime_type, media_url, media_size_bytes,
	media_transcript, links_summary, reactions_summary
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
				msg.MessageID,
				msg.ChatID,
				msg.AccountID,
				msg.Network,
				msg.SortKey,
				msg.SenderID,
				msg.SenderName,
				boolToSQLiteInt(msg.IsSender),
				unixOrZero(msg.Timestamp),
				unixOrZero(msg.EditedTimestamp),
				msg.MessageType,
				msg.Text,
				msg.LinkedMessageID,
				boolToSQLiteInt(msg.IsDeleted),
				boolToSQLiteInt(msg.IsHidden),
				msg.MediaKind,
				msg.MediaTitle,
				msg.MediaMimeType,
				msg.MediaURL,
				msg.MediaSizeBytes,
				msg.MediaTranscript,
				msg.LinksSummary,
				msg.ReactionsSummary,
			)
			if execErr != nil {
				return 0, 0, fmt.Errorf("insert beeper message %q: %w", msg.MessageID, execErr)
			}
			lastID, idErr := res.LastInsertId()
			if idErr != nil {
				return 0, 0, idErr
			}
			targetRowID = lastID
			insertedCount++
		case lookupErr != nil:
			return 0, 0, lookupErr
		default:
			if _, execErr := tx.ExecContext(ctx, `
update beeper_messages set
	account_id = ?,
	network = ?,
	sort_key = ?,
	sender_id = ?,
	sender_name = ?,
	is_sender = ?,
	timestamp_unix = ?,
	edited_timestamp_unix = ?,
	message_type = ?,
	text = ?,
	linked_message_id = ?,
	is_deleted = ?,
	is_hidden = ?,
	media_kind = ?,
	media_title = ?,
	media_mime_type = ?,
	media_url = ?,
	media_size_bytes = ?,
	media_transcript = ?,
	links_summary = ?,
	reactions_summary = ?
where rowid = ?
`,
				msg.AccountID,
				msg.Network,
				msg.SortKey,
				msg.SenderID,
				msg.SenderName,
				boolToSQLiteInt(msg.IsSender),
				unixOrZero(msg.Timestamp),
				unixOrZero(msg.EditedTimestamp),
				msg.MessageType,
				msg.Text,
				msg.LinkedMessageID,
				boolToSQLiteInt(msg.IsDeleted),
				boolToSQLiteInt(msg.IsHidden),
				msg.MediaKind,
				msg.MediaTitle,
				msg.MediaMimeType,
				msg.MediaURL,
				msg.MediaSizeBytes,
				msg.MediaTranscript,
				msg.LinksSummary,
				msg.ReactionsSummary,
				existingRowID,
			); execErr != nil {
				return 0, 0, fmt.Errorf("update beeper message %q: %w", msg.MessageID, execErr)
			}
			if _, delErr := tx.ExecContext(ctx, `delete from beeper_messages_fts where rowid = ?`, existingRowID); delErr != nil {
				return 0, 0, delErr
			}
			targetRowID = existingRowID
			updatedCount++
		}

		ftsText := searchableMessageBody(msg)
		ftsMedia := searchableMessageMedia(msg)
		if _, ftsErr := tx.ExecContext(
			ctx,
			`insert into beeper_messages_fts(rowid, text, chat, sender, network, media) values (?, ?, ?, ?, ?, ?)`,
			targetRowID,
			ftsText,
			chatTitle,
			msg.SenderName,
			msg.Network,
			ftsMedia,
		); ftsErr != nil {
			return 0, 0, fmt.Errorf("index beeper message %q in fts5: %w", msg.MessageID, ftsErr)
		}
	}

	if syncState != nil && strings.TrimSpace(syncState.ChatID) != "" {
		if _, err := tx.ExecContext(ctx, `
insert into beeper_chat_sync_state (
	chat_id, newest_sort_key, newest_message_id, oldest_cursor,
	backfill_complete, last_synced_activity_unix, last_synced_at_unix
) values (?, ?, ?, ?, ?, ?, ?)
on conflict(chat_id) do update set
	newest_sort_key = excluded.newest_sort_key,
	newest_message_id = excluded.newest_message_id,
	oldest_cursor = excluded.oldest_cursor,
	backfill_complete = excluded.backfill_complete,
	last_synced_activity_unix = excluded.last_synced_activity_unix,
	last_synced_at_unix = excluded.last_synced_at_unix
`,
			syncState.ChatID,
			syncState.NewestSortKey,
			syncState.NewestMessageID,
			syncState.OldestCursor,
			boolToSQLiteInt(syncState.BackfillComplete),
			unixOrZero(syncState.LastSyncedActivity),
			unixOrZero(syncState.LastSyncedAt),
		); err != nil {
			return 0, 0, fmt.Errorf("save sync state for chat %q: %w", syncState.ChatID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return insertedCount, updatedCount, nil
}

// ReadChatSyncState returns the persisted incremental sync state for one chat.
func (s *BeeperArchiveStore) ReadChatSyncState(ctx context.Context, chatID string) (BeeperChatSyncState, bool, error) {
	var stateRow BeeperChatSyncState
	var backfillComplete int
	var lastActivityUnix, lastSyncedUnix int64
	err := s.db.QueryRowContext(ctx, `
select chat_id, newest_sort_key, newest_message_id, oldest_cursor,
       backfill_complete, last_synced_activity_unix, last_synced_at_unix
from beeper_chat_sync_state
where chat_id = ?
`, chatID).Scan(
		&stateRow.ChatID,
		&stateRow.NewestSortKey,
		&stateRow.NewestMessageID,
		&stateRow.OldestCursor,
		&backfillComplete,
		&lastActivityUnix,
		&lastSyncedUnix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return BeeperChatSyncState{}, false, nil
	}
	if err != nil {
		return BeeperChatSyncState{}, false, err
	}
	stateRow.BackfillComplete = backfillComplete != 0
	stateRow.LastSyncedActivity = timeFromUnix(lastActivityUnix)
	stateRow.LastSyncedAt = timeFromUnix(lastSyncedUnix)
	return stateRow, true, nil
}

// MessageExists reports whether messageID is already archived in beeper_messages.
func (s *BeeperArchiveStore) MessageExists(ctx context.Context, messageID string) (bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return false, nil
	}
	var marker int
	err := s.db.QueryRowContext(ctx, `select 1 from beeper_messages where message_id = ? limit 1`, messageID).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RecordUpdateCompleted persists the last successful update timestamp and clears
// any previous sync error marker.
func (s *BeeperArchiveStore) RecordUpdateCompleted(ctx context.Context, completedAt time.Time) error {
	st := state.New(s.db)
	ts := completedAt.UTC().Format(time.RFC3339Nano)
	if err := st.Set(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastCompletedAt, ts); err != nil {
		return err
	}
	if err := st.Set(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncAttemptAt, ts); err != nil {
		return err
	}
	return st.Set(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncError, "")
}

// RecordSyncAttemptError records a failed sync attempt timestamp and short reason
// without altering any archived accounts, chats, or messages.
func (s *BeeperArchiveStore) RecordSyncAttemptError(ctx context.Context, attemptedAt time.Time, errText string) error {
	st := state.New(s.db)
	ts := attemptedAt.UTC().Format(time.RFC3339Nano)
	if err := st.Set(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncAttemptAt, ts); err != nil {
		return err
	}
	return st.Set(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncError, strings.TrimSpace(errText))
}

// OptimizeSearchIndex compacts the FTS5 index after a write run.
func (s *BeeperArchiveStore) OptimizeSearchIndex(ctx context.Context) error {
	return ckstore.OptimizeFTS5(ctx, s.db, "beeper_messages_fts")
}

// Status reads aggregate counts and timestamps from the Beeper archive.
func (s *BeeperArchiveStore) Status(ctx context.Context) (BeeperArchiveStatus, error) {
	var out BeeperArchiveStatus
	if err := s.db.QueryRowContext(ctx, `select count(*) from beeper_accounts`).Scan(&out.AccountCount); err != nil {
		if isMissingTableError(err) {
			return out, nil
		}
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from beeper_chats`).Scan(&out.ChatCount); err != nil {
		return out, err
	}
	var oldestUnix, newestUnix sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
select count(*), min(nullif(timestamp_unix, 0)), max(nullif(timestamp_unix, 0))
from beeper_messages
`).Scan(&out.MessageCount, &oldestUnix, &newestUnix); err != nil {
		return out, err
	}
	if oldestUnix.Valid {
		out.OldestMessageAt = timeFromUnix(oldestUnix.Int64)
	}
	if newestUnix.Valid {
		out.NewestMessageAt = timeFromUnix(newestUnix.Int64)
	}

	st := state.New(s.db)
	if rec, ok, err := st.Get(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastCompletedAt); err == nil && ok {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, rec.Value); parseErr == nil {
			out.LastCompletedAt = parsed.UTC()
		}
	}
	if rec, ok, err := st.Get(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncError); err == nil && ok {
		out.LastSyncError = strings.TrimSpace(rec.Value)
	}
	if rec, ok, err := st.Get(ctx, beeperUpdateSource, beeperUpdateEntityType, beeperStateLastSyncAttemptAt); err == nil && ok {
		if parsed, parseErr := time.Parse(time.RFC3339Nano, rec.Value); parseErr == nil {
			out.LastAttemptAt = parsed.UTC()
		}
	}
	return out, nil
}

// ListNetworkSummaries returns per-network counts ordered by message volume.
func (s *BeeperArchiveStore) ListNetworkSummaries(ctx context.Context) ([]BeeperArchivedNetworkSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
select
	n.network,
	coalesce((select group_concat(distinct status) from beeper_accounts a where lower(a.network) = lower(n.network) and status <> ''), ''),
	(select count(*) from beeper_chats c where lower(c.network) = lower(n.network)),
	(select count(*) from beeper_messages m where lower(m.network) = lower(n.network)),
	coalesce((select max(timestamp_unix) from beeper_messages m where lower(m.network) = lower(n.network)), 0)
from (
	select network from beeper_chats where trim(network) <> ''
	union
	select network from beeper_messages where trim(network) <> ''
	union
	select network from beeper_accounts where trim(network) <> ''
) n
group by lower(n.network)
order by 4 desc, 3 desc, lower(n.network) asc
`)
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var summaries []BeeperArchivedNetworkSummary
	for rows.Next() {
		var row BeeperArchivedNetworkSummary
		var newestUnix int64
		if err := rows.Scan(&row.Network, &row.AccountStatus, &row.ConversationCount, &row.MessageCount, &newestUnix); err != nil {
			return nil, err
		}
		row.NewestMessageAt = timeFromUnix(newestUnix)
		summaries = append(summaries, row)
	}
	return summaries, rows.Err()
}

func searchableMessageBody(msg BeeperArchivedMessage) string {
	var parts []string
	if trimmed := strings.TrimSpace(msg.Text); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(msg.MediaTranscript); trimmed != "" && !strings.Contains(msg.Text, trimmed) {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(msg.LinksSummary); trimmed != "" && !strings.Contains(msg.Text, trimmed) {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, "\n")
}

func searchableMessageMedia(msg BeeperArchivedMessage) string {
	var parts []string
	if trimmed := strings.TrimSpace(msg.MediaTitle); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(msg.MediaKind); trimmed != "" {
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, " ")
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func timeFromUnix(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func isMissingTableError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}
