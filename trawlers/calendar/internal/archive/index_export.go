package archive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

const calendarIndexBatchSize = 2000

// ExportArchiveIndexChanges streams all calendars (for short references) and
// events as a complete snapshot replacement into core index.db.
func (s *Store) ExportArchiveIndexChanges(
	ctx context.Context,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	var eventCount int64
	var maxStart int64
	if err := s.store.DB().QueryRowContext(
		ctx,
		`select count(*), coalesce(max(start_unix), 0) from events`,
	).Scan(&eventCount, &maxStart); err != nil {
		return fmt.Errorf("read calendar index cursor state: %w", err)
	}
	var calCount int64
	if err := s.store.DB().QueryRowContext(ctx, `select count(*) from calendars`).Scan(&calCount); err != nil {
		return fmt.Errorf("count calendars for index cursor: %w", err)
	}
	nextCursor := fmt.Sprintf("cal:%d:%d:%d", calCount, eventCount, maxStart)
	if sinceOpaqueCursor != "" && sinceOpaqueCursor == nextCursor {
		return nil
	}

	calOnlyRefs, err := s.loadCalendarShortReferenceOnlyEntries(ctx)
	if err != nil {
		return err
	}

	rows, err := s.store.DB().QueryContext(ctx, `
		select event_uid, calendar_id, calendar_title, start_time, start_unix, end_unix, all_day,
		       summary, description, organizer_name, organizer_email,
		       location_title, location_address, participants_text, url, status
		from events
		order by event_uid
	`)
	if err != nil {
		return fmt.Errorf("read calendar events for unified index: %w", err)
	}
	defer func() { _ = rows.Close() }()

	upserts := make([]*indexcontract.IndexedArchiveRecordUpsert, 0, min(int(eventCount), calendarIndexBatchSize))
	firstBatch := true
	flush := func(isFinal bool) error {
		batch := &indexcontract.ArchiveIndexChangeBatch{
			Upserts:                     upserts,
			CompleteSnapshotReplacement: firstBatch,
		}
		if firstBatch {
			batch.ShortReferenceOnlyEntries = calOnlyRefs
			firstBatch = false
		}
		if isFinal {
			batch.NextOpaqueCursor = nextCursor
		}
		if err := emit(batch); err != nil {
			return err
		}
		if isFinal {
			upserts = nil
		} else {
			upserts = make([]*indexcontract.IndexedArchiveRecordUpsert, 0, calendarIndexBatchSize)
		}
		return nil
	}

	for rows.Next() {
		var uid, calID, calTitle, startTime, summary, desc, orgName, orgEmail, locTitle, locAddr, participants, rawURL, status string
		var startUnix, endUnix int64
		var allDay int
		if err := rows.Scan(
			&uid, &calID, &calTitle, &startTime, &startUnix, &endUnix, &allDay,
			&summary, &desc, &orgName, &orgEmail,
			&locTitle, &locAddr, &participants, &rawURL, &status,
		); err != nil {
			return fmt.Errorf("scan calendar event for unified index: %w", err)
		}
		canonical := trawlkit.NewCanonicalArchiveRecordReference(RefForUID(uid))
		upsert := &indexcontract.IndexedArchiveRecordUpsert{
			StableRecordReference:          canonical,
			ResolvedRecordReference:        canonical,
			RecordKind:                     indexcontract.ArchiveRecordKind_ARCHIVE_RECORD_KIND_CALENDAR_EVENT,
			ConversationOrThreadIdentifier: "calendar:event/" + uid,
			DisplayContextTitle:            strings.TrimSpace(calTitle),
			OccurredUnixSeconds:            startUnix,
			UtcOffsetSeconds:               eventStartUTCOffsetSeconds(startTime),
			HasOccurredTime:                startUnix != 0,
			HasTimeOfDay:                   allDay == 0,
			EndUnixSeconds:                 endUnix,
			HasEndTime:                     endUnix != 0,
			Title:                          strings.TrimSpace(summary),
			BodyText:                       strings.TrimSpace(desc),
			MetadataText:                   strings.TrimSpace(strings.Join([]string{locTitle, locAddr}, " ")),
			Participants:                   buildCalendarParticipants(orgName, orgEmail, participants),
			ObservedSourceLabels:           nonEmptySlice(strings.TrimSpace(calTitle)),
			MechanicalJoinAnchors:          buildCalendarAnchors(rawURL, desc),
		}
		if strings.TrimSpace(status) != "" && !strings.EqualFold(strings.TrimSpace(status), "confirmed") {
			upsert.StatusLabels = []string{strings.TrimSpace(status)}
		}
		upserts = append(upserts, upsert)
		if len(upserts) >= calendarIndexBatchSize {
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

// eventStartUTCOffsetSeconds keeps the offset of the event's own time zone.
// An all-day event starts at local midnight, so its date depends on it.
func eventStartUTCOffsetSeconds(storedStartTime string) int32 {
	startTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(storedStartTime))
	if err != nil {
		return 0
	}
	_, utcOffsetSeconds := startTime.Zone()
	return int32(utcOffsetSeconds)
}

func (s *Store) loadCalendarShortReferenceOnlyEntries(ctx context.Context) ([]*indexcontract.ShortReferenceOnlyRegistrationEntry, error) {
	rows, err := s.store.DB().QueryContext(ctx, `select calendar_id from calendars order by calendar_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var entries []*indexcontract.ShortReferenceOnlyRegistrationEntry
	for rows.Next() {
		var calendarID string
		if err := rows.Scan(&calendarID); err != nil {
			return nil, err
		}
		ref := trawlkit.NewCanonicalArchiveRecordReference(CalendarCanonicalRecordReferenceForIdentifier(CalendarIdentifier(calendarID)))
		entries = append(entries, &indexcontract.ShortReferenceOnlyRegistrationEntry{
			StableRecordReference:   ref,
			ResolvedRecordReference: ref,
		})
	}
	return entries, rows.Err()
}

func buildCalendarParticipants(orgName, orgEmail, participantsText string) []*indexcontract.ArchiveRecordParticipant {
	var out []*indexcontract.ArchiveRecordParticipant
	if strings.TrimSpace(orgName) != "" || strings.TrimSpace(orgEmail) != "" {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			DisplayName:     strings.TrimSpace(orgName),
			AddressOrHandle: strings.TrimSpace(orgEmail),
			ParticipantRole: "organizer",
		})
	}
	if trimmed := strings.TrimSpace(participantsText); trimmed != "" {
		out = append(out, &indexcontract.ArchiveRecordParticipant{
			DisplayName:     trimmed,
			ParticipantRole: "attendee",
		})
	}
	return out
}

func buildCalendarAnchors(rawURL, desc string) []*indexcontract.MechanicalJoinAnchor {
	var out []*indexcontract.MechanicalJoinAnchor
	for _, dom := range unifiedindex.ExtractRegistrableDomains(rawURL + " " + desc) {
		out = append(out, &indexcontract.MechanicalJoinAnchor{
			AnchorKind:  indexcontract.MechanicalJoinAnchorKind_MECHANICAL_JOIN_ANCHOR_KIND_REGISTRABLE_DOMAIN,
			AnchorValue: dom,
		})
	}
	return out
}

func nonEmptySlice(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
