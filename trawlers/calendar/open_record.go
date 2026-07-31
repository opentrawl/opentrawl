package calendar

import (
	"context"

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
	openedCalendarEvent, err := c.loadOpenEvent(ctx, req, localShortReference)
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

func validateOpenTimestamps(openedCalendarEvent archive.EventDetail) error {
	return presentation.ValidateTimestamps(
		openedCalendarEvent.Start,
		openedCalendarEvent.End,
	)
}
