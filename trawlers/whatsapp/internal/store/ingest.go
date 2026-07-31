package store

import (
	"context"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store/storedb"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/state"
)

func (s *Store) ReplaceAll(ctx context.Context, stats ImportStats, contacts []Contact, chats []Chat, groups []Group, participants []GroupParticipant, messages []Message) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	q := s.q.WithTx(tx)
	shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction := make(
		[]trawlkit.ShortReferenceAssignmentCandidate,
		0,
		len(messages)+len(chats),
	)
	for _, deleteQuery := range []func(context.Context) error{
		q.DeleteMessagesFTS,
		q.DeleteMessages,
		q.DeleteGroupParticipants,
		q.DeleteGroups,
		q.DeleteChats,
		q.DeleteContacts,
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
			Text:     nullString(messageTextForFullTextSearchIndex(m)),
			Chat:     nullString(m.ChatName),
			Sender:   nullString(m.SenderName),
			Media:    nullString(strings.TrimSpace(m.MediaTitle + " " + m.MediaType)),
		})
		if err != nil {
			return err
		}
	}
	now := stats.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	syncState := state.New(tx)
	if err := syncState.Set(ctx, syncSource, syncEntityType, stateLastImportAt, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := syncState.Set(ctx, syncSource, syncEntityType, stateSourcePath, stats.SourcePath); err != nil {
		return err
	}
	for _, message := range messages {
		if strings.TrimSpace(message.MessageID) == "" {
			continue
		}
		shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction = append(
			shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction,
			trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
					MessageRefPrefix + message.MessageID,
				),
			},
		)
	}
	for _, chat := range chats {
		canonicalConversationRecordReference := ChatRef(chat.JID)
		if canonicalConversationRecordReference == "" {
			continue
		}
		shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction = append(
			shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction,
			trawlkit.ShortReferenceAssignmentCandidate{
				StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(
					canonicalConversationRecordReference,
				),
			},
		)
	}
	if err := trawlkit.ReplaceShortReferencesForCompleteArchiveRecordSnapshotUsingCallerOwnedSQLTransaction(
		ctx,
		tx,
		shortReferenceAssignmentCandidatesForRecordsPublishedByWhatsAppTransaction,
	); err != nil {
		return err
	}
	return tx.Commit()
}
