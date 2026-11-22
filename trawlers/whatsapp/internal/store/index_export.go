package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

const whatsappIndexBatchSize = 5000

// ExportArchiveIndexChanges streams WhatsApp messages and chat short references
// incrementally into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxMsgRow, msgCount, chatCount int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(rowid), 0), count(*) from messages`).Scan(&maxMsgRow, &msgCount); err != nil {
		return fmt.Errorf("read whatsapp index cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from chats`).Scan(&chatCount); err != nil {
		return fmt.Errorf("read whatsapp chat cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("whatsapp:%d:%d:%d", maxMsgRow, msgCount, chatCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevRow, prevMsgCount, prevChatCount, ok := parseWhatsAppCursor(sinceOpaqueCursor)
	incremental := ok && msgCount >= prevMsgCount && chatCount >= prevChatCount && maxMsgRow >= prevRow

	startRow := int64(0)
	if incremental {
		startRow = prevRow
	}

	chatShortRefs, err := s.loadWhatsAppChatShortRefs(ctx)
	if err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx, `
		select msg_id, ts, coalesce(text, ''), from_me,
		       coalesce(chat_jid, ''), coalesce(chat_name, ''),
		       coalesce(sender_jid, ''), coalesce(sender_name, '')
		from messages
		where rowid > ? and trim(msg_id) <> ''
		order by rowid
	`, startRow)
	if err != nil {
		return fmt.Errorf("read whatsapp messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, whatsappIndexBatchSize)
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
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, whatsappIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var msgID, text, chatJID, chatName, senderJID, senderName string
		var tsUnix int64
		var fromMe int
		if err := rows.Scan(&msgID, &tsUnix, &text, &fromMe, &chatJID, &chatName, &senderJID, &senderName); err != nil {
			return fmt.Errorf("scan whatsapp message for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(MessageRefPrefix + msgID)
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
		displayCtx := strings.TrimSpace(chatName)
		if displayCtx == "" {
			displayCtx = strings.TrimSpace(chatJID)
		}
		var participants []*indexcontract.ArchiveRecordParticipant
		if strings.TrimSpace(senderName) != "" || strings.TrimSpace(senderJID) != "" {
			participants = []*indexcontract.ArchiveRecordParticipant{{
				DisplayName:     strings.TrimSpace(senderName),
				AddressOrHandle: strings.TrimSpace(senderJID),
				ParticipantRole: "sender",
			}}
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
			ObservedSourceRole:             role,
			ConversationOrThreadIdentifier: ChatRef(chatJID),
			DisplayContextTitle:            displayCtx,
			OccurredUnixSeconds:            tsUnix,
			HasOccurredTime:                tsUnix > 0,
			HasTimeOfDay:                   true,
			BodyText:                       strings.TrimSpace(text),
			Participants:                   participants,
		})
		if len(upserts)+len(shortRefOnly) >= whatsappIndexBatchSize {
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

func (s *Store) loadWhatsAppChatShortRefs(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.db.QueryContext(ctx, `select jid from chats order by jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		if ref := ChatRef(jid); ref != "" {
			c := trawlkit.NewCanonicalArchiveRecordReference(ref)
			out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   c,
				ResolvedRecordReference: c,
			})
		}
	}
	return out, rows.Err()
}

func parseWhatsAppCursor(cursor string) (int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 || parts[0] != "whatsapp" {
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
