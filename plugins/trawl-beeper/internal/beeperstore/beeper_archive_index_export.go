package beeperstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
)

const beeperUnifiedIndexBatchSize = 5000

// ExportArchiveIndexChanges streams committed Beeper messages and conversation
// short references into OpenTrawl's core index.db so Beeper joins unified
// search, temporal `trawl around`, and hybrid semantic search.
func (s *BeeperArchiveStore) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxMsgRow, msgCount, editedCount, chatCount int64
	if err := s.db.QueryRowContext(ctx, `
select
	coalesce(max(rowid), 0),
	count(*),
	coalesce(sum(case when edited_timestamp_unix > 0 then 1 else 0 end), 0)
from beeper_messages
`).Scan(&maxMsgRow, &msgCount, &editedCount); err != nil {
		if isMissingTableError(err) {
			return nil
		}
		return fmt.Errorf("read beeper message cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from beeper_chats`).Scan(&chatCount); err != nil {
		return fmt.Errorf("read beeper chat cursor state: %w", err)
	}

	nextCursor := fmt.Sprintf("beeper:%d:%d:%d:%d", maxMsgRow, msgCount, editedCount, chatCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevRow, prevMsgCount, prevEditedCount, prevChatCount, ok := parseBeeperIndexCursor(sinceOpaqueCursor)
	incremental := ok &&
		msgCount >= prevMsgCount &&
		editedCount == prevEditedCount &&
		chatCount >= prevChatCount &&
		maxMsgRow >= prevRow

	startRow := int64(0)
	if incremental {
		startRow = prevRow
	}

	chatShortRefs, err := s.loadBeeperChatShortRefs(ctx)
	if err != nil {
		return err
	}
	participantsByChat, err := s.loadChatParticipantsMap(ctx)
	if err != nil {
		return err
	}

	rows, err := s.db.QueryContext(ctx, `
select
	m.message_id,
	m.chat_id,
	coalesce(nullif(m.network, ''), c.network, ''),
	coalesce(c.title, ''),
	coalesce(nullif(c.chat_type, ''), 'single'),
	m.sender_id,
	m.sender_name,
	m.is_sender,
	m.timestamp_unix,
	m.text,
	m.media_kind,
	m.media_title,
	m.media_transcript,
	m.links_summary
from beeper_messages m
left join beeper_chats c on c.chat_id = m.chat_id
where m.rowid > ? and trim(m.message_id) <> ''
order by m.rowid
`, startRow)
	if err != nil {
		return fmt.Errorf("read beeper messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, beeperUnifiedIndexBatchSize)
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
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, beeperUnifiedIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var messageID, chatID, network, chatTitle, chatType, senderID, senderName string
		var text, mediaKind, mediaTitle, mediaTranscript, linksSummary string
		var isSender int
		var tsUnix int64
		if err := rows.Scan(
			&messageID,
			&chatID,
			&network,
			&chatTitle,
			&chatType,
			&senderID,
			&senderName,
			&isSender,
			&tsUnix,
			&text,
			&mediaKind,
			&mediaTitle,
			&mediaTranscript,
			&linksSummary,
		); err != nil {
			return fmt.Errorf("scan beeper message for unified index: %w", err)
		}

		canonical := trawlkit.NewCanonicalArchiveRecordReference(BeeperMessageRef(messageID))
		bodyText := buildIndexBodyText(text, mediaTranscript, mediaTitle, mediaKind, linksSummary)
		if bodyText == "" {
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}

		role := indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED
		if isSender != 0 {
			role = indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
		}

		displayContext := FormatNetworkChatDisplayTitle(network, chatTitle)
		participants := buildIndexedParticipants(isSender != 0, senderName, senderID, chatType, participantsByChat[chatID])

		var networkLabels []string
		if cleanNet := strings.TrimSpace(network); cleanNet != "" {
			networkLabels = []string{cleanNet}
		}

		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordAnchor:                   trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CHAT_MESSAGE,
			ObservedSourceRole:             role,
			ConversationOrThreadIdentifier: BeeperChatRef(chatID),
			DisplayContextTitle:            displayContext,
			OccurredUnixSeconds:            tsUnix,
			HasOccurredTime:                tsUnix > 0,
			HasTimeOfDay:                   true,
			BodyText:                       bodyText,
			MetadataText:                   strings.TrimSpace(network),
			Participants:                   participants,
			ObservedSourceLabels:           networkLabels,
			StatusLabels:                   networkLabels,
		})
		if len(upserts)+len(shortRefOnly) >= beeperUnifiedIndexBatchSize {
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

func (s *BeeperArchiveStore) loadBeeperChatShortRefs(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.db.QueryContext(ctx, `select chat_id from beeper_chats order by chat_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var chatID string
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		if ref := BeeperChatRef(chatID); ref != "" {
			canonical := trawlkit.NewCanonicalArchiveRecordReference(ref)
			out = append(out, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
		}
	}
	return out, rows.Err()
}

func (s *BeeperArchiveStore) loadChatParticipantsMap(ctx context.Context) (map[string][]BeeperArchivedParticipant, error) {
	rows, err := s.db.QueryContext(ctx, `
select chat_id, user_id, full_name, username, phone_number, email, is_self
from beeper_chat_participants
where is_self = 0
`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byChat := make(map[string][]BeeperArchivedParticipant)
	for rows.Next() {
		var participant BeeperArchivedParticipant
		var isSelf int
		if err := rows.Scan(
			&participant.ChatID,
			&participant.UserID,
			&participant.FullName,
			&participant.Username,
			&participant.PhoneNumber,
			&participant.Email,
			&isSelf,
		); err != nil {
			return nil, err
		}
		participant.IsSelf = isSelf != 0
		byChat[participant.ChatID] = append(byChat[participant.ChatID], participant)
	}
	return byChat, rows.Err()
}

func buildIndexBodyText(text string, transcript string, mediaTitle string, mediaKind string, linksSummary string) string {
	var parts []string
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(transcript); trimmed != "" && !strings.Contains(text, trimmed) {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(linksSummary); trimmed != "" && !strings.Contains(text, trimmed) {
		parts = append(parts, trimmed)
	}
	if len(parts) == 0 {
		if trimmed := strings.TrimSpace(mediaTitle); trimmed != "" {
			parts = append(parts, trimmed)
		} else if trimmed := strings.TrimSpace(mediaKind); trimmed != "" {
			parts = append(parts, "["+trimmed+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func buildIndexedParticipants(
	isSender bool,
	senderName string,
	senderID string,
	chatType string,
	chatParticipants []BeeperArchivedParticipant,
) []*indexcontract.ArchiveRecordParticipant {
	var out []*indexcontract.ArchiveRecordParticipant
	seen := make(map[string]struct{})
	add := func(displayName string, handle string, role string) {
		displayName = strings.TrimSpace(displayName)
		handle = strings.TrimSpace(handle)
		if displayName == "" && handle == "" {
			return
		}
		key := strings.ToLower(displayName + "\x00" + handle)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			DisplayName:     displayName,
			AddressOrHandle: handle,
			ParticipantRole: role,
		})
	}

	if isSender {
		add("me", "", "sender")
		if strings.EqualFold(chatType, "single") {
			for _, participant := range chatParticipants {
				name := firstNonEmptyTrimmed(participant.FullName, participant.Username, participant.PhoneNumber, participant.Email)
				handle := firstNonEmptyTrimmed(participant.Username, participant.PhoneNumber, participant.Email)
				if strings.EqualFold(name, handle) {
					handle = ""
				}
				add(name, handle, "recipient")
			}
		}
		return out
	}

	var senderHandle string
	for _, participant := range chatParticipants {
		if strings.EqualFold(strings.TrimSpace(participant.UserID), strings.TrimSpace(senderID)) {
			senderHandle = firstNonEmptyTrimmed(participant.Username, participant.PhoneNumber, participant.Email)
			if strings.TrimSpace(senderName) == "" {
				senderName = firstNonEmptyTrimmed(participant.FullName, senderHandle)
			}
			break
		}
	}
	if strings.EqualFold(strings.TrimSpace(senderName), senderHandle) {
		senderHandle = ""
	}
	add(senderName, senderHandle, "sender")
	return out
}

func parseBeeperIndexCursor(cursor string) (int64, int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 5 || parts[0] != "beeper" {
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
