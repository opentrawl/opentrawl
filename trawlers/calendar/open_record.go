package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	canonicalOpenedRecordReference, err := c.resolveOpenRef(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	if calendarIdentifier, isCalendarReference := archive.CalendarIdentifierFromCanonicalRecordReference(trawlkit.CanonicalArchiveRecordReferenceText(canonicalOpenedRecordReference)); isCalendarReference {
		return c.openCalendarRecord(ctx, req, calendarIdentifier)
	}
	openedCalendarEvent, err := c.loadOpenEvent(ctx, req, canonicalOpenedRecordReference)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(openedCalendarEvent); err != nil {
		return nil, err
	}
	calendarEventRecord := projectCalendarEventRecord(
		calendarEventRecordValuesFromDetail(openedCalendarEvent),
	)
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: calendarEventRecord.GetCanonicalRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_CalendarEventRecord{
			CalendarEventRecord: calendarEventRecord,
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (c *Crawler) openCalendarRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	calendarIdentifier archive.CalendarIdentifier,
) (*open.OpenRecord, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archivedCalendar, err := archiveStore.CalendarWithActiveOrFutureEventCount(ctx, calendarIdentifier, time.Now())
	if err != nil {
		return nil, err
	}
	calendarRecord := calendarRecordForProduct(archivedCalendar)
	openedCalendarRecord := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: calendarRecord.GetCanonicalRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_CalendarRecord{
			CalendarRecord: calendarRecord,
		},
	}
	if err := openrecord.Validate(openedCalendarRecord); err != nil {
		return nil, err
	}
	return openedCalendarRecord, nil
}

func validateOpenTimestamps(openedCalendarEvent archive.EventDetail) error {
	return presentation.ValidateTimestamps(
		openedCalendarEvent.Start,
		openedCalendarEvent.End,
	)
}
