package archive

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

const (
	ownerSenderDisplayName     = "me"
	imessageIndexBatchSize     = 5000
	appleCoreDataEpochUnixSecs = int64(978307200)
)

// IndexedMessageHumanDisplayNames carries the names a person sees for
// archived conversations and senders, so index.db shows the same names as
// the iMessage conversation and message commands.
type IndexedMessageHumanDisplayNames struct {
	ConversationDisplayNameByChatRowID        map[int64]string
	SenderDisplayNameFromContactNameAndHandle func(contactDisplayName, handle string) string
}

// ExportArchiveIndexChanges streams iMessage messages (with explicit opaque
// chat_identifier conversation keys separate from display names) and chat
// short references incrementally into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	humanDisplayNames IndexedMessageHumanDisplayNames,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxMsgRow, msgCount, maxChatRow, chatCount int64
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select coalesce(max(source_rowid), 0), count(*) from messages`,
	).Scan(&maxMsgRow, &msgCount); err != nil {
		return fmt.Errorf("read imessage index cursor state: %w", err)
	}
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select coalesce(max(source_rowid), 0), count(*) from chats`,
	).Scan(&maxChatRow, &chatCount); err != nil {
		return fmt.Errorf("read imessage chat cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("imessage:%d:%d:%d:%d", maxMsgRow, msgCount, maxChatRow, chatCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevMsgRow, prevMsgCount, prevChatRow, prevChatCount, ok := parseIMessageCursor(sinceOpaqueCursor)
	incremental := ok && msgCount >= prevMsgCount && chatCount >= prevChatCount && maxMsgRow >= prevMsgRow && maxChatRow >= prevChatRow

	startMsgRow, startChatRow := int64(0), int64(0)
	if incremental {
		startMsgRow = prevMsgRow
		startChatRow = prevChatRow
	}

	chatShortRefs, err := s.loadIMessageChatShortRefs(ctx, startChatRow)
	if err != nil {
		return err
	}

	rows, err := s.store.DB().QueryContext(ctx, `
		select m.source_rowid, m.date, coalesce(m.text, ''), m.is_from_me,
		       coalesce(nullif(h.display_name, ''), ''), coalesce(nullif(h.handle, ''), ''),
		       coalesce(ch.source_rowid, 0),
		       coalesce(nullif(ch.chat_identifier, ''), '')
		from messages m
		left join handles h on h.source_rowid = m.handle_rowid
		left join chat_messages cm on cm.message_rowid = m.source_rowid
		left join chats ch on ch.source_rowid = cm.chat_rowid
		where m.source_rowid > ?
		order by m.source_rowid
	`, startMsgRow)
	if err != nil {
		return fmt.Errorf("read imessage messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, imessageIndexBatchSize)
	shortRefOnly := chatShortRefs
	seenRowIDs := make(map[int64]struct{}, 1024)
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
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, imessageIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var rowID, rawDate, chatRowID int64
		var isFromMe int
		var text, senderContactName, senderHandle, chatIdent string
		if err := rows.Scan(
			&rowID, &rawDate, &text, &isFromMe,
			&senderContactName, &senderHandle,
			&chatRowID, &chatIdent,
		); err != nil {
			return fmt.Errorf("scan imessage row for unified index: %w", err)
		}
		if _, dup := seenRowIDs[rowID]; dup {
			continue
		}
		seenRowIDs[rowID] = struct{}{}

		canonical := trawlkit.NewCanonicalArchiveRecordReference(MessageRef(strconv.FormatInt(rowID, 10)))
		if strings.TrimSpace(text) == "" {
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		occUnix, hasTime := decodeAppleIMessageDate(rawDate)
		convKey := opaqueIMessageConversationID(chatIdent, chatRowID, senderHandle, rowID)
		senderDisplayName := ""
		if senderContactName != "" || senderHandle != "" {
			senderDisplayName = humanDisplayNames.SenderDisplayNameFromContactNameAndHandle(senderContactName, senderHandle)
		}
		conversationDisplayName := humanDisplayNames.ConversationDisplayNameByChatRowID[chatRowID]
		if chatRowID == 0 && isFromMe == 0 {
			conversationDisplayName = senderDisplayName
		}
		role := indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED
		if isFromMe != 0 {
			role = indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
		}
		var participants []*indexcontract.ArchiveRecordParticipant
		switch {
		case isFromMe != 0:
			participants = []*indexcontract.ArchiveRecordParticipant{{
				DisplayName:     ownerSenderDisplayName,
				ParticipantRole: "sender",
			}}
		case senderDisplayName != "" || senderHandle != "":
			participants = []*indexcontract.ArchiveRecordParticipant{{
				DisplayName:     senderDisplayName,
				AddressOrHandle: senderHandle,
				ParticipantRole: "sender",
			}}
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
			ObservedSourceRole:             role,
			ConversationOrThreadIdentifier: convKey,
			DisplayContextTitle:            conversationDisplayName,
			OccurredUnixSeconds:            occUnix,
			HasOccurredTime:                hasTime,
			HasTimeOfDay:                   true,
			BodyText:                       strings.TrimSpace(text),
			Participants:                   participants,
		})
		if len(upserts)+len(shortRefOnly) >= imessageIndexBatchSize {
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

func (s *Store) loadIMessageChatShortRefs(ctx context.Context, afterChatRowID int64) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select source_rowid from chats where source_rowid > ? order by source_rowid`, afterChatRowID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var rowID int64
		if err := rows.Scan(&rowID); err != nil {
			return nil, err
		}
		ref := trawlkit.NewCanonicalArchiveRecordReference(ChatRef(strconv.FormatInt(rowID, 10)))
		out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
			StableRecordReference:   ref,
			ResolvedRecordReference: ref,
		})
	}
	return out, rows.Err()
}

func parseIMessageCursor(cursor string) (int64, int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 5 || parts[0] != "imessage" {
		return 0, 0, 0, 0, false
	}
	a, e1 := strconv.ParseInt(parts[1], 10, 64)
	b, e2 := strconv.ParseInt(parts[2], 10, 64)
	c, e3 := strconv.ParseInt(parts[3], 10, 64)
	d, e4 := strconv.ParseInt(parts[4], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return 0, 0, 0, 0, false
	}
	return a, b, c, d, true
}

func decodeAppleIMessageDate(rawDate int64) (int64, bool) {
	switch {
	case rawDate > 1_000_000_000_000:
		return rawDate/1_000_000_000 + appleCoreDataEpochUnixSecs, true
	case rawDate > 0:
		return rawDate + appleCoreDataEpochUnixSecs, true
	default:
		return 0, false
	}
}

func opaqueIMessageConversationID(chatIdent string, chatRowID int64, senderHandle string, msgRowID int64) string {
	if chatIdent != "" {
		return "imessage:chat/" + chatIdent
	}
	if chatRowID > 0 {
		return "imessage:chat_row/" + strconv.FormatInt(chatRowID, 10)
	}
	if senderHandle != "" {
		return "imessage:handle/" + senderHandle
	}
	return "imessage:msg/" + strconv.FormatInt(msgRowID, 10)
}
