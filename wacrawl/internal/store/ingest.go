package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/wacrawl/internal/store/storedb"
)

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
