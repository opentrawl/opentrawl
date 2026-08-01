package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

// ChatListOptions carries the read-side flags for listing chats. Limit zero
// means no cap. UnreadOnly returns only chats with at least one unread
// received message.
type ChatListOptions struct {
	Limit      int
	UnreadOnly bool
}

func (s *Store) Chats(ctx context.Context, opts ChatListOptions) ([]ChatSummary, error) {
	db := s.store.DB()
	unreadSelect := unreadReceivedExpr
	having := ""
	if opts.UnreadOnly {
		having = "having " + unreadReceivedExpr + " > 0"
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
	if err := populateConversationParticipantIdentities(ctx, db, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) Chat(ctx context.Context, chatID string) (ChatSummary, error) {
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
	if err := populateConversationParticipantIdentities(ctx, db, out); err != nil {
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

func populateConversationParticipantIdentities(ctx context.Context, db *sql.DB, chats []ChatSummary) error {
	for i := range chats {
		conversationParticipantIdentities, err := readConversationParticipantIdentities(
			ctx,
			db,
			chats[i].ChatID,
		)
		if err != nil {
			return err
		}
		chats[i].ConversationParticipantIdentities = conversationParticipantIdentities
	}
	return nil
}

func readConversationParticipantIdentities(
	ctx context.Context,
	db *sql.DB,
	chatID string,
) ([]ConversationParticipantIdentity, error) {
	id, err := parseID(chatID, "chat")
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, conversationParticipantIdentitiesSQL, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var conversationParticipantIdentities []ConversationParticipantIdentity
	for rows.Next() {
		var conversationParticipantIdentity ConversationParticipantIdentity
		var exactPersonFilterIdentifier string
		if err := rows.Scan(
			&exactPersonFilterIdentifier,
			&conversationParticipantIdentity.PersonDisplayName,
		); err != nil {
			return nil, err
		}
		conversationParticipantIdentity.ExactPersonFilterIdentifier = &person.ExactPersonFilterIdentifier{
			ExactPersonFilterIdentifier: exactPersonFilterIdentifier,
		}
		conversationParticipantIdentities = append(
			conversationParticipantIdentities,
			conversationParticipantIdentity,
		)
	}
	return conversationParticipantIdentities, rows.Err()
}
