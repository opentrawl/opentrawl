package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

const telegramIndexBatchSize = 5000

// ExportArchiveIndexChanges streams Telegram messages and chat short references
// incrementally into core index.db using a monotonic rowid cursor.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxMsgRow, msgCount, chatCount int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(rowid), 0), count(*) from messages`).Scan(&maxMsgRow, &msgCount); err != nil {
		return fmt.Errorf("read telegram index cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from chats`).Scan(&chatCount); err != nil {
		return fmt.Errorf("read telegram chat cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("telegram:%d:%d:%d", maxMsgRow, msgCount, chatCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevRow, prevMsgCount, prevChatCount, ok := parseTelegramCursor(sinceOpaqueCursor)
	incremental := ok && msgCount >= prevMsgCount && chatCount >= prevChatCount && maxMsgRow >= prevRow

	startRow := int64(0)
	if incremental {
		startRow = prevRow
	}

	chatShortRefs, err := s.loadTelegramChatShortRefs(ctx)
	if err != nil {
		return err
	}

	contactsByJID, err := s.allContactsForDisplay(ctx)
	if err != nil {
		return fmt.Errorf("read telegram contacts for unified index: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		select source_pk, ts, coalesce(text, ''), from_me,
		       coalesce(chat_jid, ''), coalesce(chat_name, ''),
		       coalesce(sender_jid, ''), coalesce(sender_name, '')
		from messages
		where rowid > ?
		order by rowid
	`, startRow)
	if err != nil {
		return fmt.Errorf("read telegram messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, telegramIndexBatchSize)
	shortRefOnly := chatShortRefs
	firstBatch := true
	flush := func(isFinal bool) error {
		batch := &indexcontract.ArchiveIndexChangeBatch{
			Upserts:                     upserts,
			ShortReferenceOnlyEntries:   shortRefOnly,
			CompleteSnapshotReplacement: !incremental && firstBatch,
		}
		firstBatch = false
		if isFinal {
			batch.NextOpaqueCursor = nextCursor
		}
		if err := emit(batch); err != nil {
			return err
		}
		if isFinal {
			upserts = nil
		} else {
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, telegramIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var sourcePK, tsUnix int64
		var fromMe int
		var text, chatJID, chatName, senderJID, senderName string
		if err := rows.Scan(&sourcePK, &tsUnix, &text, &fromMe, &chatJID, &chatName, &senderJID, &senderName); err != nil {
			return fmt.Errorf("scan telegram message for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(MessageRef(sourcePK))
		if strings.TrimSpace(text) == "" {
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		role := indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED
		if fromMe != 0 {
			role = indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
		}
		namedMessages := []Message{{
			ChatJID:    chatJID,
			ChatName:   chatName,
			SenderJID:  senderJID,
			SenderName: senderName,
			FromMe:     fromMe != 0,
		}}
		humanizeMessagesWithContacts(namedMessages, func(jid string) Contact {
			return contactsByJID[strings.TrimSpace(jid)]
		})
		displayCtx := strings.TrimSpace(namedMessages[0].ChatName)
		convKey := "telegram:chat/" + strings.TrimSpace(chatJID)
		if strings.TrimSpace(chatJID) == "" {
			convKey = "telegram:chat/" + displayCtx
		}
		var participants []*indexcontract.ArchiveRecordParticipant
		if senderDisplayName := strings.TrimSpace(namedMessages[0].SenderName); senderDisplayName != "" {
			participants = []*indexcontract.ArchiveRecordParticipant{{
				DisplayName:     senderDisplayName,
				AddressOrHandle: strings.TrimSpace(senderJID),
				ParticipantRole: "sender",
			}}
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
			ObservedSourceRole:             role,
			ConversationOrThreadIdentifier: convKey,
			DisplayContextTitle:            displayCtx,
			OccurredUnixSeconds:            tsUnix,
			HasOccurredTime:                tsUnix > 0,
			HasTimeOfDay:                   true,
			BodyText:                       strings.TrimSpace(text),
			Participants:                   participants,
		})
		if len(upserts)+len(shortRefOnly) >= telegramIndexBatchSize {
			if err := flush(false); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return flush(true)
}

func (s *Store) loadTelegramChatShortRefs(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.db.QueryContext(ctx, `select account_scoped_conversation_identifier_for_conversation_across_telegram_migrations from chats order by id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var convID string
		if err := rows.Scan(&convID); err != nil {
			return nil, err
		}
		if ref := ChatRef(convID); ref != "" {
			c := trawlkit.NewCanonicalArchiveRecordReference(ref)
			out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   c,
				ResolvedRecordReference: c,
			})
		}
	}
	return out, rows.Err()
}

func parseTelegramCursor(cursor string) (int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 || parts[0] != "telegram" {
		return 0, 0, 0, false
	}
	a, e1 := strconv.ParseInt(parts[1], 10, 64)
	b, e2 := strconv.ParseInt(parts[2], 10, 64)
	c, e3 := strconv.ParseInt(parts[3], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, false
	}
	return a, b, c, true
}
