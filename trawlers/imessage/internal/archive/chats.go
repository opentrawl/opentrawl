package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// ErrNoReadState means the archive stores no read state, so an unread-only
// listing cannot be answered. It is returned only for UnreadOnly requests;
// a plain listing still succeeds and simply leaves each Unread nil.
var ErrNoReadState = errors.New("archive stores no read state")

// ChatListOptions carries the read-side flags for listing chats. Limit zero
// means no cap. UnreadOnly returns only chats with at least one unread
// received message, and requires the archive to store read state.
type ChatListOptions struct {
	Limit      int
	UnreadOnly bool
}

func (s *Store) Chats(ctx context.Context, opts ChatListOptions) ([]ChatSummary, error) {
	if s.schemaOutdated {
		return nil, ErrSchemaOutdated
	}
	db := s.store.DB()
	readState, err := s.readStateAvailable(ctx)
	if err != nil {
		return nil, err
	}
	if opts.UnreadOnly && !readState {
		return nil, ErrNoReadState
	}
	unreadSelect := "null"
	having := ""
	if readState {
		unreadSelect = unreadReceivedExpr
		if opts.UnreadOnly {
			having = "having " + unreadReceivedExpr + " > 0"
		}
	}
	limitClause := ""
	args := []any{}
	if opts.Limit > 0 {
		limitClause = "limit ?"
		args = append(args, opts.Limit)
	}
	rows, err := db.QueryContext(ctx, chatSummaryQuery("", unreadSelect, having)+limitClause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out, err := scanChatSummaries(rows)
	if err != nil {
		return nil, err
	}
	if err := populateParticipantHandles(ctx, db, out); err != nil {
		return nil, err
	}
	return out, nil
}

// readStateAvailable reports whether the archive has ingested read state. A
// pre-migration archive lacks the messages.is_read column; a synced one has
// it, fully populated by the sync rewrite. The check is a structural column
// probe, stable across retries against the same archive.
func (s *Store) readStateAvailable(ctx context.Context) (bool, error) {
	return tableHasColumn(ctx, s.store.DB(), "messages", "is_read")
}

func (s *Store) Chat(ctx context.Context, chatID string) (ChatSummary, error) {
	if s.schemaOutdated {
		return ChatSummary{}, ErrSchemaOutdated
	}
	id, err := parseID(chatID, "chat")
	if err != nil {
		return ChatSummary{}, err
	}
	db := s.store.DB()
	// The single-chat read backs the messages verb header, which never shows an
	// unread count, so the unread select is null and Chat.Unread stays nil here.
	rows, err := db.QueryContext(ctx, chatSummaryQuery("where c.source_rowid = ?", "null", ""), id)
	if err != nil {
		return ChatSummary{}, err
	}
	defer func() { _ = rows.Close() }()
	out, err := scanChatSummaries(rows)
	if err != nil {
		return ChatSummary{}, err
	}
	if len(out) == 0 {
		return ChatSummary{}, fmt.Errorf("%w: %s", ErrChatNotFound, chatID)
	}
	if err := populateParticipantHandles(ctx, db, out); err != nil {
		return ChatSummary{}, err
	}
	return out[0], nil
}

func scanChatSummaries(rows *sql.Rows) ([]ChatSummary, error) {
	out := []ChatSummary{}
	for rows.Next() {
		var c ChatSummary
		var chatID int64
		var unread sql.NullInt64
		if err := rows.Scan(&chatID, &c.GUID, &c.Title, &c.Kind, &c.ChatIdentifier, &c.RoomName, &c.Service, &c.ParticipantCount, &c.MessageCount, &c.LatestMessageDate, &unread); err != nil {
			return nil, err
		}
		c.ChatID = strconv.FormatInt(chatID, 10)
		if unread.Valid {
			count := unread.Int64
			c.Unread = &count
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func populateParticipantHandles(ctx context.Context, db *sql.DB, chats []ChatSummary) error {
	for i := range chats {
		handles, err := participantHandles(ctx, db, chats[i].ChatID)
		if err != nil {
			return err
		}
		chats[i].ParticipantHandles = handles
	}
	return nil
}

func participantHandles(ctx context.Context, db *sql.DB, chatID string) ([]string, error) {
	id, err := parseID(chatID, "chat")
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, participantHandlesSQL, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var handle string
		if err := rows.Scan(&handle); err != nil {
			return nil, err
		}
		out = append(out, handle)
	}
	return out, rows.Err()
}
