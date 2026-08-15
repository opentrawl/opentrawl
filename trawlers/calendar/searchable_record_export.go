package calendar

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (c *Crawler) ExportSearchableRecordPage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	exportRequest *searchablerecord.SearchableRecordExportRequest,
) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
	recordsAfterCanonicalReference := trawlkit.CanonicalArchiveRecordReferenceText(exportRequest.GetRecordsAfterCanonicalRecordReference())
	recordsAfterEventIdentifier := ""
	if recordsAfterCanonicalReference != "" {
		var valid bool
		recordsAfterEventIdentifier, valid = archive.UIDFromRef(recordsAfterCanonicalReference)
		if !valid {
			return nil, fmt.Errorf("calendar searchable record cursor is invalid")
		}
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx, `
		select event.event_uid, event.start_time, event.end_time, event.all_day,
		       event.summary, event.calendar_title, event.account_name,
		       event.availability, event.location_title, event.location_address,
		       event.organizer_name, event.organizer_email, event.organizer_phone,
		       event.attendees_json, event.url, event.status, event.has_recurrences,
		       event.description
		from events event
		where event.event_uid > ?
		  and trim(coalesce(event.summary, '') || coalesce(event.description, '') ||
		           coalesce(event.location_title, '') || coalesce(event.location_address, '') ||
		           coalesce(event.attendees_json, '')) <> ''
		order by event.event_uid
		limit ?`, recordsAfterEventIdentifier, int64(exportRequest.GetMaximumRecordCount())+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	searchableArchiveRecords := make([]*searchablerecord.SearchableArchiveRecord, 0, exportRequest.GetMaximumRecordCount()+1)
	for rows.Next() {
		var eventIdentifier string
		var allDay, hasRecurrences int64
		var availability sql.NullInt64
		var locationTitle, locationAddress, attendeesJSON string
		var values calendarEventRecordValuesFromArchive
		if err := rows.Scan(
			&eventIdentifier, &values.startTime, &values.endTime, &allDay,
			&values.eventDisplayName, &values.calendarDisplayName, &values.calendarAccountDisplayName,
			&availability, &locationTitle, &locationAddress,
			&values.organizer.DisplayName, &values.organizer.Email, &values.organizer.PhoneNumber,
			&attendeesJSON, &values.httpsURL, &values.status, &hasRecurrences,
			&values.description,
		); err != nil {
			return nil, err
		}
		values.canonicalCalendarEventRecordReference = archive.RefForUID(eventIdentifier)
		values.allDay = allDay != 0
		values.recurring = hasRecurrences != 0
		if availability.Valid {
			values.availability = &availability.Int64
		}
		if strings.TrimSpace(locationTitle) != "" || strings.TrimSpace(locationAddress) != "" {
			values.location = &archive.Location{Title: locationTitle, Address: locationAddress}
		}
		if err := json.Unmarshal([]byte(attendeesJSON), &values.attendees); err != nil {
			return nil, fmt.Errorf("decode event attendees: %w", err)
		}
		typedRecord := projectCalendarEventRecord(values)
		searchableArchiveRecords = append(searchableArchiveRecords, &searchablerecord.SearchableArchiveRecord{
			CanonicalRecordReference:            typedRecord.GetCanonicalRecordReference(),
			CanonicalSearchResultGroupReference: typedRecord.GetCanonicalRecordReference(),
			TypedSourceRecord: &searchablerecord.SearchableArchiveRecord_CalendarEventRecord{
				CalendarEventRecord: typedRecord,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return trawlkit.CompleteSearchableRecordExportPage(searchableArchiveRecords, exportRequest.GetMaximumRecordCount()), nil
}
