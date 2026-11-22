package archive

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

const mailIndexBatchSize = 2000

// ExportArchiveIndexChanges streams Apple Mail messages into core index.db,
// using an incremental id cursor when only new messages were appended and
// falling back to complete snapshot replacement if rows were removed.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxID, activeCount, totalCount int64
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select coalesce(max(id), 0), sum(case when deleted_from_source_at = '' then 1 else 0 end), count(*) from messages`,
	).Scan(&maxID, &activeCount, &totalCount); err != nil {
		return fmt.Errorf("read mail index cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("mail:%d:%d:%d", maxID, activeCount, totalCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevMaxID, prevActive, prevTotal, ok := parseMailCursor(sinceOpaqueCursor)
	incremental := ok && totalCount >= prevTotal && (activeCount-prevActive) == (totalCount-prevTotal) && maxID >= prevMaxID

	query := `
		select id, apple_mail_mailbox_name, time_unix, from_name, from_address, to_address, cc_address,
		       subject, body, rfc5322_message_identifier, in_reply_to_message_identifiers, deleted_from_source_at
		from messages
		where id > ?
		order by id
	`
	startID := int64(0)
	if incremental {
		startID = prevMaxID
	}

	rows, err := s.store.DB().QueryContext(ctx, query, startID)
	if err != nil {
		return fmt.Errorf("read mail messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, mailIndexBatchSize)
	var shortRefOnly []*indexcontract.ShortReferenceOnlyRegistrationEntry
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
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, mailIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var id, timeUnix int64
		var mailbox, fromName, fromAddr, toAddr, ccAddr, subject, body, rfcID, inReplyTo, deletedAt string
		if err := rows.Scan(
			&id, &mailbox, &timeUnix, &fromName, &fromAddr, &toAddr, &ccAddr,
			&subject, &body, &rfcID, &inReplyTo, &deletedAt,
		); err != nil {
			return fmt.Errorf("scan mail message for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(MailMessageRecordReference(ArchivedMailMessageIdentifier(id)))
		if strings.TrimSpace(deletedAt) != "" {
			// Deleted mail rows keep their shortref reserved without appearing in active search.
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		threadID := deriveMailConversationID(id, rfcID, inReplyTo)
		var labels []string
		if mb := strings.TrimSpace(mailbox); mb != "" {
			labels = []string{mb}
		}
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_EMAIL,
			ConversationOrThreadIdentifier: threadID,
			DisplayContextTitle:            strings.TrimSpace(fromAddr),
			OccurredUnixSeconds:            timeUnix,
			HasOccurredTime:                timeUnix > 0,
			HasTimeOfDay:                   true,
			Title:                          strings.TrimSpace(subject),
			BodyText:                       strings.TrimSpace(body),
			MetadataText:                   strings.TrimSpace(mailbox),
			Participants:                   buildMailParticipants(fromName, fromAddr, toAddr, ccAddr),
			ObservedSourceLabels:           labels,
			MechanicalJoinAnchors:          buildMailAnchors(rfcID, fromAddr, subject, body),
		})
		if len(upserts) >= mailIndexBatchSize {
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

func parseMailCursor(cursor string) (int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 || parts[0] != "mail" {
		return 0, 0, 0, false
	}
	maxID, err1 := strconv.ParseInt(parts[1], 10, 64)
	active, err2 := strconv.ParseInt(parts[2], 10, 64)
	total, err3 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return maxID, active, total, true
}

func deriveMailConversationID(id int64, rfcID, inReplyTo string) string {
	if fields := strings.Fields(inReplyTo); len(fields) > 0 {
		if parent := strings.Trim(fields[0], "<>"); parent != "" {
			return "mail:thread/" + parent
		}
	}
	if self := strings.Trim(strings.TrimSpace(rfcID), "<>"); self != "" {
		return "mail:thread/" + self
	}
	return MailMessageRecordReference(ArchivedMailMessageIdentifier(id))
}

func buildMailParticipants(fromName, fromAddr, toAddr, ccAddr string) []*indexcontract.ArchiveRecordParticipant {
	var out []*indexcontract.ArchiveRecordParticipant
	if strings.TrimSpace(fromName) != "" || strings.TrimSpace(fromAddr) != "" {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			DisplayName:     strings.TrimSpace(fromName),
			AddressOrHandle: strings.TrimSpace(fromAddr),
			ParticipantRole: "from",
		})
	}
	if strings.TrimSpace(toAddr) != "" {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			AddressOrHandle: strings.TrimSpace(toAddr),
			ParticipantRole: "to",
		})
	}
	if strings.TrimSpace(ccAddr) != "" {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			AddressOrHandle: strings.TrimSpace(ccAddr),
			ParticipantRole: "cc",
		})
	}
	return out
}

func buildMailAnchors(rfcID, fromAddr, subject, body string) []*indexcontract.MechanicalJoinAnchor {
	var out []*indexcontract.MechanicalJoinAnchor
	if trimmedRFC := strings.Trim(strings.TrimSpace(rfcID), "<>"); trimmedRFC != "" {
		out = append(out, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_RFC5322_MESSAGE_IDENTIFIER,
			AnchorValue: trimmedRFC,
		})
	}
	snippet := unifiedindex.TruncateRunes(body, 3000)
	for _, dom := range unifiedindex.ExtractRegistrableDomains(fromAddr + " " + subject + " " + unifiedindex.TruncateRunes(snippet, 1500)) {
		out = append(out, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_REGISTRABLE_DOMAIN,
			AnchorValue: dom,
		})
	}
	for _, minor := range unifiedindex.ExtractMonetaryAmountMinorUnits(subject + " " + snippet) {
		out = append(out, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_EXACT_MINOR_UNIT_AMOUNT,
			AnchorValue: strconv.FormatInt(minor, 10),
		})
	}
	return out
}
