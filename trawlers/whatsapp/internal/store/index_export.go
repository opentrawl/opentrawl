package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const whatsappIndexBatchSize = 5000

// IndexedMessageProjection turns one archived message, with the names a
// person sees already applied, into its index.db record. It returns nil for a
// message without searchable text.
type IndexedMessageProjection func(Message) *indexcontract.IndexedArchiveRecordUpsert

// ExportArchiveIndexChanges streams WhatsApp messages and chat short references
// incrementally into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	projectIndexedMessage IndexedMessageProjection,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxMsgRow, msgCount, chatCount int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(rowid), 0), count(*) from messages`).Scan(&maxMsgRow, &msgCount); err != nil {
		return fmt.Errorf("read whatsapp index cursor state: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from chats`).Scan(&chatCount); err != nil {
		return fmt.Errorf("read whatsapp chat cursor state: %w", err)
	}
	displayNameFingerprint, err := ckstore.FingerprintQueryRows(ctx, s.db,
		`select jid, phone, full_name, first_name, last_name, business_name, username, lid from contacts order by jid`,
		`select jid, kind, name from chats order by jid`,
		`select group_jid, user_jid, contact_name, is_active from group_participants order by group_jid, user_jid`,
	)
	if err != nil {
		return fmt.Errorf("read whatsapp display names for index cursor: %w", err)
	}
	nextCursor := fmt.Sprintf("whatsapp:%d:%d:%d:%s", maxMsgRow, msgCount, chatCount, displayNameFingerprint)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevRow, prevMsgCount, prevChatCount, prevDisplayNameFingerprint, ok := parseWhatsAppCursor(sinceOpaqueCursor)
	incremental := ok && prevDisplayNameFingerprint == displayNameFingerprint &&
		msgCount >= prevMsgCount && chatCount >= prevChatCount && maxMsgRow >= prevRow

	startRow := int64(0)
	if incremental {
		startRow = prevRow
	}

	chatShortRefs, err := s.loadWhatsAppChatShortRefs(ctx)
	if err != nil {
		return err
	}

	humanDisplayNames, err := s.canonicalWhatsAppMessageDisplayNames(ctx)
	if err != nil {
		return fmt.Errorf("read whatsapp display names for unified index: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `select `+messageSelectColumns+`
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
		archivedMessage, err := scanMessageRow(rows)
		if err != nil {
			return fmt.Errorf("scan whatsapp message for unified index: %w", err)
		}
		namedMessages := []Message{archivedMessage}
		humanDisplayNames.applyToMessages(namedMessages)
		archivedMessage = namedMessages[0]
		if upsert := projectIndexedMessage(archivedMessage); upsert != nil {
			upserts = append(upserts, upsert)
		} else {
			canonical := trawlkit.NewCanonicalArchiveRecordReference(MessageRefPrefix + archivedMessage.MessageID)
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
		}
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

func parseWhatsAppCursor(cursor string) (int64, int64, int64, string, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 5 || parts[0] != "whatsapp" || parts[4] == "" {
		return 0, 0, 0, "", false
	}
	a, e1 := strconv.ParseInt(parts[1], 10, 64)
	b, e2 := strconv.ParseInt(parts[2], 10, 64)
	c, e3 := strconv.ParseInt(parts[3], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, "", false
	}
	return a, b, c, parts[4], true
}
