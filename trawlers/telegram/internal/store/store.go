package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"

	// C SQLite via cgo, matching trawlkit/store after the modernc→mattn swap:
	// the pure-Go driver ran hot paths 10-100x slower. Requires
	// -tags sqlite_fts5; the monorepo devenv sets it via GOFLAGS.
	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	store *ckstore.Store
	db    *sql.DB
	path  string
	owned bool
}

type ImportStats struct {
	SourcePath             string    `json:"source_path"`
	DBPath                 string    `json:"db_path"`
	Chats                  int       `json:"chats"`
	Messages               int       `json:"messages"`
	MediaMessages          int       `json:"media_messages"`
	MediaFiles             int       `json:"media_files"`
	MediaBytes             int64     `json:"media_bytes"`
	RemoteMediaCandidates  int       `json:"remote_media_candidates,omitempty"`
	RemoteMediaAttempted   int       `json:"remote_media_attempted,omitempty"`
	RemoteMediaDownloads   int       `json:"remote_media_downloads,omitempty"`
	RemoteMediaMissing     int       `json:"remote_media_missing,omitempty"`
	RemoteMediaUnavailable int       `json:"remote_media_unavailable,omitempty"`
	RemoteMediaTimeouts    int       `json:"remote_media_timeouts,omitempty"`
	RemoteMediaErrors      int       `json:"remote_media_errors,omitempty"`
	StartedAt              time.Time `json:"started_at"`
	FinishedAt             time.Time `json:"finished_at"`
}

// MessageMediaUpdate attaches an archived file without rewriting the message
// projection that discovered it.
type MessageMediaUpdate struct {
	SourcePK  int64
	MediaPath string
	MediaSize int64
}

// UpdateMessageMedia fills missing attachment paths atomically. A concurrent
// update that already attached a file wins; this operation never replaces it.
func (s *Store) UpdateMessageMedia(ctx context.Context, updates []MessageMediaUpdate) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	updated := 0
	for _, update := range updates {
		path := strings.TrimSpace(update.MediaPath)
		if update.SourcePK == 0 || path == "" {
			continue
		}
		result, err := tx.ExecContext(ctx, `update messages set media_path=?, media_size=? where source_pk=? and media_type<>'' and coalesce(media_path,'')=''`, path, update.MediaSize, update.SourcePK)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += int(count)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

type Status struct {
	ArchiveMessageCountAfterLastSuccessfullyCompletedUpdate      int
	ArchiveConversationCountAfterLastSuccessfullyCompletedUpdate int
	ArchiveFolderCountAfterLastSuccessfullyCompletedUpdate       int
	ArchiveSourcePathUsedByLastSuccessfullyCompletedUpdate       string
	LastSuccessfullyCompletedArchiveUpdateTime                   time.Time
	HasSuccessfullyCompletedArchiveUpdate                        bool
	ArchiveCanAnswerCurrentCommands                              bool
}

type Chat struct {
	JID                                                                        string `json:"jid"`
	AccountScopedConversationIdentifierForConversationAcrossTelegramMigrations string
	Kind                                                                       string    `json:"kind"`
	Name                                                                       string    `json:"name,omitempty"`
	Username                                                                   string    `json:"username,omitempty"`
	LastMessageAt                                                              time.Time `json:"last_message_at,omitzero"`
	UnreadCount                                                                int       `json:"unread_count"`
	MessageCount                                                               int       `json:"message_count"`
	FolderID                                                                   string    `json:"folder_id,omitempty"`
	Forum                                                                      bool      `json:"forum,omitempty"`
}

type Folder struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	Emoticon  string `json:"emoticon,omitempty"`
	Color     int    `json:"color,omitempty"`
	FlagsJSON string `json:"flags_json,omitempty"`
}

type FolderChat struct {
	FolderID string `json:"folder_id"`
	ChatJID  string `json:"chat_jid"`
	Position int    `json:"position"`
}

type Topic struct {
	ChatJID              string    `json:"chat_jid"`
	TopicID              string    `json:"topic_id"`
	Title                string    `json:"title,omitempty"`
	TopMessageID         string    `json:"top_message_id,omitempty"`
	IconColor            int       `json:"icon_color,omitempty"`
	IconEmojiID          string    `json:"icon_emoji_id,omitempty"`
	UnreadCount          int       `json:"unread_count"`
	UnreadMentionsCount  int       `json:"unread_mentions_count"`
	UnreadReactionsCount int       `json:"unread_reactions_count"`
	Pinned               bool      `json:"pinned,omitempty"`
	Closed               bool      `json:"closed,omitempty"`
	Hidden               bool      `json:"hidden,omitempty"`
	LastMessageAt        time.Time `json:"last_message_at,omitzero"`
}

type Contact struct {
	JID          string    `json:"jid"`
	PeerType     string    `json:"peer_type,omitempty"`
	Phone        string    `json:"phone,omitempty"`
	FullName     string    `json:"full_name,omitempty"`
	FirstName    string    `json:"first_name,omitempty"`
	LastName     string    `json:"last_name,omitempty"`
	BusinessName string    `json:"business_name,omitempty"`
	Username     string    `json:"username,omitempty"`
	LID          string    `json:"lid,omitempty"`
	AboutText    string    `json:"about_text,omitempty"`
	AvatarPath   string    `json:"avatar_path,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

type ParticipantMatch struct {
	JID         string
	DisplayName string
}

type WhoCandidate struct {
	Who          string
	Identifiers  []string
	LastSeen     time.Time
	Messages     int
	Participants []ParticipantMatch

	aliases   []string
	matchRank int
}

type Group struct {
	JID       string    `json:"jid"`
	Name      string    `json:"name,omitempty"`
	OwnerJID  string    `json:"owner_jid,omitempty"`
	CreatedAt time.Time `json:"created_at,omitzero"`
}

type GroupParticipant struct {
	GroupJID    string `json:"group_jid"`
	UserJID     string `json:"user_jid"`
	ContactName string `json:"contact_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	IsAdmin     bool   `json:"is_admin,omitempty"`
	IsActive    bool   `json:"is_active,omitempty"`
}

type Message struct {
	SourcePK      int64                `json:"source_pk"`
	ChatJID       string               `json:"chat_jid"`
	ChatName      string               `json:"chat_name,omitempty"`
	ChatKind      string               `json:"chat_kind,omitempty"`
	TopicTitle    string               `json:"topic_title,omitempty"`
	MessageID     string               `json:"message_id"`
	SenderJID     string               `json:"sender_jid,omitempty"`
	SenderName    string               `json:"sender_name,omitempty"`
	Timestamp     time.Time            `json:"timestamp"`
	EditTime      time.Time            `json:"edit_timestamp,omitzero"`
	FromMe        bool                 `json:"from_me"`
	Text          string               `json:"text,omitempty"`
	RawType       int                  `json:"raw_type"`
	MessageType   string               `json:"message_type,omitempty"`
	MediaType     string               `json:"media_type,omitempty"`
	MediaTitle    string               `json:"media_title,omitempty"`
	MediaPath     string               `json:"media_path,omitempty"`
	MediaURL      string               `json:"media_url,omitempty"`
	MediaSize     int64                `json:"media_size,omitempty"`
	MetadataType  string               `json:"metadata_type,omitempty"`
	MetadataTitle string               `json:"metadata_title,omitempty"`
	MetadataURL   string               `json:"metadata_url,omitempty"`
	MetadataJSON  string               `json:"metadata_json,omitempty"`
	Starred       bool                 `json:"starred,omitempty"`
	TopicID       string               `json:"topic_id,omitempty"`
	ReplyToID     string               `json:"reply_to_message_id,omitempty"`
	ReplyToChat   string               `json:"reply_to_chat_id,omitempty"`
	ThreadID      string               `json:"thread_id,omitempty"`
	ForwardJSON   string               `json:"forward_json,omitempty"`
	ReactionsJSON string               `json:"reactions_json,omitempty"`
	Views         int                  `json:"views,omitempty"`
	Forwards      int                  `json:"forwards,omitempty"`
	RepliesCount  int                  `json:"replies_count,omitempty"`
	Pinned        bool                 `json:"pinned,omitempty"`
	Snippet       string               `json:"snippet,omitempty"`
	SearchMatches []MessageSearchMatch `json:"-"`
}

type MessageSearchMatch struct {
	Field string
	Runs  []ckstore.FTS5TextRun
}

type MessageFilter struct {
	Query        string
	ChatJID      string
	Sender       string
	PersonFilter trawlkit.SearchPersonFilter
	Limit        int
	After        *time.Time
	Before       *time.Time
	FromMe       *bool
	HasMedia     bool
	Pinned       bool
	Asc          bool

	ResolvedPersonFilterParticipants      []ParticipantMatch
	PersonFilterWasResolvedToParticipants bool
}

func (filter MessageFilter) AllowsFilterOnlySearch() bool {
	return filter.PersonFilter.UnresolvedPersonFilterText() != "" || filter.PersonFilter.ResolvedPersonFilter() != nil || filter.After != nil || filter.Before != nil
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("db path is required")
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		return nil, err
	}
	out, err := Use(ctx, st, path)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	out.owned = true
	return out, nil
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	db := st.DB()
	s := &Store{store: st, db: db, path: path}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, indexSQL); err != nil {
		return nil, err
	}
	if err := shortref.EnsureSchema(ctx, db); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `create index if not exists idx_short_refs_full_ref on short_refs(full_ref)`); err != nil {
		return nil, err
	}
	return s, nil
}

func UseExisting(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &Store{store: st, db: st.DB(), path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || !s.owned || s.store == nil {
		return nil
	}
	return s.store.Close()
}

func (s *Store) Path() string { return s.path }

func (s *Store) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, archiveSourcePath string, successfullyCompletedAt time.Time) error {
	archiveSourcePath = strings.TrimSpace(archiveSourcePath)
	if archiveSourcePath == "" {
		return errors.New("telegram archive source path is required")
	}
	if successfullyCompletedAt.IsZero() {
		return errors.New("telegram archive update completion time is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	archiveCanAnswerCurrentCommands, err :=
		archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReferenceUsingCurrentTransaction(ctx, tx)
	if err != nil {
		return err
	}
	if !archiveCanAnswerCurrentCommands {
		return errors.New("telegram archive cannot answer current commands after update")
	}
	_, err = tx.ExecContext(ctx, `
insert into last_successfully_completed_archive_update (
	last_successfully_completed_archive_update_id,
	archive_message_count,
	archive_conversation_count,
	archive_folder_count,
	archive_source_path,
	successfully_completed_at_unix_milliseconds
)
select 1,
	(select count(*) from messages),
	(select count(distinct account_scoped_conversation_identifier_for_conversation_across_telegram_migrations) from chats),
	(select count(*) from folders),
	?, ?
on conflict(last_successfully_completed_archive_update_id) do update set
	archive_message_count = excluded.archive_message_count,
	archive_conversation_count = excluded.archive_conversation_count,
	archive_folder_count = excluded.archive_folder_count,
	archive_source_path = excluded.archive_source_path,
	successfully_completed_at_unix_milliseconds = excluded.successfully_completed_at_unix_milliseconds`,
		archiveSourcePath,
		successfullyCompletedAt.UTC().UnixMilli(),
	)
	if err != nil {
		return err
	}
	if err := recordCurrentArchiveCommandReadiness(ctx, tx, true); err != nil {
		return err
	}
	return tx.Commit()
}

func recordCurrentArchiveCommandReadiness(ctx context.Context, tx *sql.Tx, archiveCanAnswerCurrentCommands bool) error {
	_, err := tx.ExecContext(ctx, `
insert into current_archive_command_readiness (
	current_archive_command_readiness_id,
	archive_can_answer_current_commands
)
values (1, ?)
on conflict(current_archive_command_readiness_id) do update set
	archive_can_answer_current_commands = excluded.archive_can_answer_current_commands`,
		boolInt(archiveCanAnswerCurrentCommands),
	)
	return err
}

// MergeObserved updates records returned by a partial acquisition without
// treating records absent from that acquisition as deleted from the source.
func (s *Store) MergeObserved(
	ctx context.Context,
	contacts []Contact,
	chats []Chat,
	folders []Folder,
	folderChats []FolderChat,
	topics []Topic,
	participants []GroupParticipant,
	messages []Message,
) (UpdateStats, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpdateStats{}, err
	}
	defer rollback(tx)
	shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction := make(
		[]trawlkit.ShortReferenceAssignmentCandidate,
		0,
		len(chats)+len(messages),
	)
	updateStats, changedMessages, err := observedMessageChanges(ctx, tx, messages)
	if err != nil {
		return UpdateStats{}, err
	}
	if err := insertContacts(ctx, tx, contacts); err != nil {
		return UpdateStats{}, err
	}
	for _, c := range chats {
		observedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations :=
			strings.TrimSpace(c.AccountScopedConversationIdentifierForConversationAcrossTelegramMigrations)
		accountScopedConversationIdentifierForConversationAcrossTelegramMigrations :=
			accountScopedConversationIdentifierAcrossTelegramMigrations(c)
		var storedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations string
		if err := tx.QueryRowContext(ctx, `insert into chats(id,account_scoped_conversation_identifier_for_conversation_across_telegram_migrations,kind,name,username,last_message_at,unread_count,message_count,folder_id,forum) values(?,?,?,?,?,?,?,?,?,?) on conflict(id) do update set account_scoped_conversation_identifier_for_conversation_across_telegram_migrations=case when ? <> '' then ? else chats.account_scoped_conversation_identifier_for_conversation_across_telegram_migrations end, kind=excluded.kind, name=excluded.name, username=excluded.username, last_message_at=excluded.last_message_at, unread_count=excluded.unread_count, message_count=excluded.message_count, folder_id=excluded.folder_id, forum=excluded.forum returning account_scoped_conversation_identifier_for_conversation_across_telegram_migrations`,
			parseInt64(c.JID),
			accountScopedConversationIdentifierForConversationAcrossTelegramMigrations,
			c.Kind,
			c.Name,
			c.Username,
			unix(c.LastMessageAt),
			c.UnreadCount,
			c.MessageCount,
			c.FolderID,
			boolInt(c.Forum),
			observedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations,
			observedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations,
		).Scan(&storedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations); err != nil {
			return UpdateStats{}, err
		}
		canonicalConversationRecordReference := ChatRef(
			storedAccountScopedConversationIdentifierForConversationAcrossTelegramMigrations,
		)
		if canonicalConversationRecordReference != "" {
			shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction = append(
				shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction,
				trawlkit.ShortReferenceAssignmentCandidate{
					StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
						canonicalConversationRecordReference,
					),
				},
			)
		}
	}
	for _, f := range folders {
		if _, err := tx.ExecContext(ctx, `insert into folders(id,title,emoticon,color,flags_json) values(?,?,?,?,?) on conflict(id) do update set title=excluded.title, emoticon=excluded.emoticon, color=excluded.color, flags_json=excluded.flags_json`,
			f.ID, f.Title, f.Emoticon, f.Color, f.FlagsJSON); err != nil {
			return UpdateStats{}, err
		}
	}
	for _, fc := range folderChats {
		if _, err := tx.ExecContext(ctx, `insert into folder_chats(folder_id,chat_jid,position) values(?,?,?) on conflict(folder_id,chat_jid) do update set position=excluded.position`,
			fc.FolderID, fc.ChatJID, fc.Position); err != nil {
			return UpdateStats{}, err
		}
	}
	for _, t := range topics {
		if _, err := tx.ExecContext(ctx, `insert into topics(chat_jid,topic_id,title,top_message_id,icon_color,icon_emoji_id,unread_count,unread_mentions_count,unread_reactions_count,pinned,closed,hidden,last_message_at) values(?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(chat_jid,topic_id) do update set title=excluded.title, top_message_id=excluded.top_message_id, icon_color=excluded.icon_color, icon_emoji_id=excluded.icon_emoji_id, unread_count=excluded.unread_count, unread_mentions_count=excluded.unread_mentions_count, unread_reactions_count=excluded.unread_reactions_count, pinned=excluded.pinned, closed=excluded.closed, hidden=excluded.hidden, last_message_at=excluded.last_message_at`,
			t.ChatJID, t.TopicID, t.Title, t.TopMessageID, t.IconColor, t.IconEmojiID, t.UnreadCount, t.UnreadMentionsCount, t.UnreadReactionsCount, boolInt(t.Pinned), boolInt(t.Closed), boolInt(t.Hidden), unix(t.LastMessageAt)); err != nil {
			return UpdateStats{}, err
		}
	}
	if err := insertGroupParticipants(ctx, tx, participants); err != nil {
		return UpdateStats{}, err
	}
	if err := upsertMessages(ctx, tx, changedMessages); err != nil {
		return UpdateStats{}, err
	}
	for _, message := range changedMessages {
		shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction = append(
			shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction,
			trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
					MessageRef(message.SourcePK),
				),
			},
		)
	}
	if err := trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(
		ctx,
		tx,
		shortReferenceAssignmentCandidatesForRecordsPublishedByTelegramTransaction,
	); err != nil {
		return UpdateStats{}, err
	}
	if len(chats) > 0 || len(changedMessages) > 0 {
		if err := recordCurrentArchiveCommandReadiness(ctx, tx, false); err != nil {
			return UpdateStats{}, err
		}
	}
	return updateStats, tx.Commit()
}

func accountScopedConversationIdentifierAcrossTelegramMigrations(chat Chat) string {
	accountScopedConversationIdentifierForConversationAcrossTelegramMigrations :=
		strings.TrimSpace(chat.AccountScopedConversationIdentifierForConversationAcrossTelegramMigrations)
	if accountScopedConversationIdentifierForConversationAcrossTelegramMigrations != "" {
		return accountScopedConversationIdentifierForConversationAcrossTelegramMigrations
	}
	return strings.TrimSpace(chat.JID)
}

func insertContacts(ctx context.Context, tx *sql.Tx, contacts []Contact) error {
	for _, c := range contacts {
		if strings.TrimSpace(c.JID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `insert into contacts(jid,peer_type,phone,full_name,first_name,last_name,business_name,username,lid,about_text,avatar_path,updated_at) values(?,?,?,?,?,?,?,?,?,?,?,?) on conflict(jid) do update set peer_type=excluded.peer_type, phone=excluded.phone, full_name=excluded.full_name, first_name=excluded.first_name, last_name=excluded.last_name, business_name=excluded.business_name, username=excluded.username, lid=excluded.lid, about_text=excluded.about_text, avatar_path=excluded.avatar_path, updated_at=excluded.updated_at`,
			c.JID, c.PeerType, c.Phone, c.FullName, c.FirstName, c.LastName, c.BusinessName, c.Username, c.LID, c.AboutText, c.AvatarPath, unix(c.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}

func insertGroupParticipants(ctx context.Context, tx *sql.Tx, participants []GroupParticipant) error {
	for _, p := range mergeGroupParticipants(participants) {
		if _, err := tx.ExecContext(ctx, `insert into group_participants(group_jid,user_jid,contact_name,first_name,is_admin,is_active) values(?,?,?,?,?,?) on conflict(group_jid,user_jid) do update set contact_name=excluded.contact_name, first_name=excluded.first_name, is_admin=excluded.is_admin, is_active=excluded.is_active`,
			p.GroupJID, p.UserJID, p.ContactName, p.FirstName, boolInt(p.IsAdmin), boolInt(p.IsActive)); err != nil {
			return err
		}
	}
	return nil
}

func mergeGroupParticipants(participants []GroupParticipant) []GroupParticipant {
	byKey := map[string]GroupParticipant{}
	for _, p := range participants {
		p.GroupJID = strings.TrimSpace(p.GroupJID)
		p.UserJID = strings.TrimSpace(p.UserJID)
		p.ContactName = strings.Join(strings.Fields(p.ContactName), " ")
		p.FirstName = strings.Join(strings.Fields(p.FirstName), " ")
		if p.GroupJID == "" || p.UserJID == "" {
			continue
		}
		key := p.GroupJID + "\x00" + p.UserJID
		existing := byKey[key]
		if existing.ContactName == "" {
			existing.ContactName = p.ContactName
		}
		if existing.FirstName == "" {
			existing.FirstName = p.FirstName
		}
		existing.GroupJID = p.GroupJID
		existing.UserJID = p.UserJID
		existing.IsAdmin = existing.IsAdmin || p.IsAdmin
		existing.IsActive = existing.IsActive || p.IsActive
		byKey[key] = existing
	}
	out := make([]GroupParticipant, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupJID != out[j].GroupJID {
			return out[i].GroupJID < out[j].GroupJID
		}
		return out[i].UserJID < out[j].UserJID
	})
	return out
}

func upsertMessages(ctx context.Context, tx *sql.Tx, messages []Message) error {
	for _, m := range messages {
		if _, err := tx.ExecContext(ctx, `delete from messages_fts where rowid = (select rowid from messages where source_pk = ?)`, m.SourcePK); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into messages(source_pk,chat_jid,chat_name,msg_id,sender_jid,sender_name,ts,from_me,text,raw_type,message_type,media_type,media_title,media_path,media_url,media_size,metadata_type,metadata_title,metadata_url,metadata_json,starred,topic_id,reply_to_msg_id,reply_to_chat_jid,thread_id,edit_ts,forward_json,reactions_json,views,forwards,replies_count,pinned) values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) on conflict(source_pk) do update set chat_jid=excluded.chat_jid, chat_name=excluded.chat_name, msg_id=excluded.msg_id, sender_jid=excluded.sender_jid, sender_name=excluded.sender_name, ts=excluded.ts, from_me=excluded.from_me, text=excluded.text, raw_type=excluded.raw_type, message_type=excluded.message_type, media_type=excluded.media_type, media_title=excluded.media_title, media_path=excluded.media_path, media_url=excluded.media_url, media_size=excluded.media_size, metadata_type=excluded.metadata_type, metadata_title=excluded.metadata_title, metadata_url=excluded.metadata_url, metadata_json=excluded.metadata_json, starred=excluded.starred, topic_id=excluded.topic_id, reply_to_msg_id=excluded.reply_to_msg_id, reply_to_chat_jid=excluded.reply_to_chat_jid, thread_id=excluded.thread_id, edit_ts=excluded.edit_ts, forward_json=excluded.forward_json, reactions_json=excluded.reactions_json, views=excluded.views, forwards=excluded.forwards, replies_count=excluded.replies_count, pinned=excluded.pinned`,
			m.SourcePK, m.ChatJID, m.ChatName, m.MessageID, m.SenderJID, m.SenderName, unix(m.Timestamp), boolInt(m.FromMe), m.Text, m.RawType, m.MessageType, m.MediaType, m.MediaTitle, m.MediaPath, m.MediaURL, m.MediaSize, m.MetadataType, m.MetadataTitle, m.MetadataURL, m.MetadataJSON, boolInt(m.Starred), m.TopicID, m.ReplyToID, m.ReplyToChat, m.ThreadID, unix(m.EditTime), m.ForwardJSON, m.ReactionsJSON, m.Views, m.Forwards, m.RepliesCount, boolInt(m.Pinned)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into messages_fts(rowid,text,chat,sender,media) values((select rowid from messages where source_pk=?),?,?,?,?)`,
			m.SourcePK, m.Text, m.ChatName, m.SenderName, strings.TrimSpace(m.MediaTitle+" "+m.MetadataTitle)); err != nil {
			return err
		}
	}
	return nil
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
	if v <= 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}
func rollback(tx *sql.Tx) { _ = tx.Rollback() }

func parseInt64(s string) int64 {
	var out int64
	_, _ = fmt.Sscan(s, &out)
	return out
}
