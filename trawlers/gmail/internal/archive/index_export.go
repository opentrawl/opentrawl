package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

const gmailIndexBatchSize = 2000

// ExportArchiveIndexChanges streams Gmail messages (with observed Gmail labels,
// thread identifiers, and mechanical join anchors) into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var maxRowID, activeCount, totalCount int64
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select coalesce(max(rowid), 0), sum(case when deleted_from_source_at = '' then 1 else 0 end), count(*) from messages`,
	).Scan(&maxRowID, &activeCount, &totalCount); err != nil {
		return fmt.Errorf("read gmail index cursor state: %w", err)
	}
	nextCursor := fmt.Sprintf("gmail:%d:%d:%d", maxRowID, activeCount, totalCount)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	prevMaxRow, prevActive, prevTotal, ok := parseGmailCursor(sinceOpaqueCursor)
	incremental := ok && totalCount >= prevTotal && (activeCount-prevActive) == (totalCount-prevTotal) && maxRowID >= prevMaxRow

	labelNames, err := s.loadGmailLabelNames(ctx)
	if err != nil {
		return err
	}

	startRowID := int64(0)
	if incremental {
		startRowID = prevMaxRow
	}
	rows, err := s.store.DB().QueryContext(ctx, `
		select id, thread_id, time_unix, internal_date_ms,
		       from_name, from_address, to_address, cc_address,
		       subject, body, labels_json, rfc5322_message_identifier, deleted_from_source_at
		from messages
		where rowid > ?
		order by rowid
	`, startRowID)
	if err != nil {
		return fmt.Errorf("read gmail messages for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, gmailIndexBatchSize)
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
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, gmailIndexBatchSize)
		}
		shortRefOnly = nil
		return nil
	}

	for rows.Next() {
		var id, threadID, fromName, fromAddr, toAddr, ccAddr, subject, body, labelsJSON, rfcID, deletedAt string
		var timeUnix, internalDateMS int64
		if err := rows.Scan(
			&id, &threadID, &timeUnix, &internalDateMS,
			&fromName, &fromAddr, &toAddr, &ccAddr,
			&subject, &body, &labelsJSON, &rfcID, &deletedAt,
		); err != nil {
			return fmt.Errorf("scan gmail message for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(RefPrefix + id)
		if strings.TrimSpace(deletedAt) != "" {
			shortRefOnly = append(shortRefOnly, &indexcontract.ShortReferenceOnlyRegistrationEntry{
				StableRecordReference:   canonical,
				ResolvedRecordReference: canonical,
			})
			continue
		}
		occUnix := timeUnix
		if occUnix == 0 && internalDateMS > 0 {
			occUnix = internalDateMS / 1000
		}
		convKey := "gmail:thread/" + strings.TrimSpace(threadID)
		if strings.TrimSpace(threadID) == "" {
			convKey = RefPrefix + id
		}
		observedLabels := resolveGmailObservedLabels(labelsJSON, labelNames)
		upserts = append(upserts, &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_EMAIL,
			ObservedSourceRole:             gmailObservedRole(observedLabels),
			ConversationOrThreadIdentifier: convKey,
			DisplayContextTitle:            strings.TrimSpace(fromAddr),
			OccurredUnixSeconds:            occUnix,
			HasOccurredTime:                occUnix > 0,
			HasTimeOfDay:                   true,
			Title:                          strings.TrimSpace(subject),
			BodyText:                       strings.TrimSpace(body),
			Participants:                   buildGmailParticipants(fromName, fromAddr, toAddr, ccAddr),
			ObservedSourceLabels:           observedLabels,
			MechanicalJoinAnchors:          buildGmailAnchors(rfcID, fromAddr, subject, body),
		})
		if len(upserts) >= gmailIndexBatchSize {
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

func parseGmailCursor(cursor string) (int64, int64, int64, bool) {
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 || parts[0] != "gmail" {
		return 0, 0, 0, false
	}
	maxRow, err1 := strconv.ParseInt(parts[1], 10, 64)
	active, err2 := strconv.ParseInt(parts[2], 10, 64)
	total, err3 := strconv.ParseInt(parts[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return maxRow, active, total, true
}

func (s *Store) loadGmailLabelNames(ctx context.Context) (map[string]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select id, name from gmail_labels`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string)
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) != "" {
			out[id] = strings.TrimSpace(name)
		}
	}
	return out, rows.Err()
}

func resolveGmailObservedLabels(labelsJSON string, labelNames map[string]string) []string {
	var ids []string
	if err := json.Unmarshal([]byte(labelsJSON), &ids); err != nil || len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if name, ok := labelNames[trimmed]; ok && name != "" && name != trimmed {
			out = append(out, trimmed, name)
		} else {
			out = append(out, trimmed)
		}
	}
	return out
}

func gmailObservedRole(labels []string) indexcontract.ObservedArchiveRecordRole {
	for _, lbl := range labels {
		if lbl == "SENT" {
			return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_AUTHORED
		}
	}
	return indexcontract.ObservedArchiveRecordRole_OBSERVED_ARCHIVE_RECORD_ROLE_RECEIVED
}

func buildGmailParticipants(fromName, fromAddr, toAddr, ccAddr string) []*indexcontract.ArchiveRecordParticipant {
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

func buildGmailAnchors(rfcID, fromAddr, subject, body string) []*indexcontract.MechanicalJoinAnchor {
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
