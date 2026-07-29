package calendar

import (
	"context"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	ref string,
) (*openv1.OpenRecord, error) {
	openedCalendarEvent, err := c.loadOpenEvent(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if err := validateOpenTimestamps(openedCalendarEvent); err != nil {
		return nil, err
	}
	calendarEventRecord := projectCalendarEventRecord(
		calendarEventRecordValuesFromDetail(openedCalendarEvent),
	)
	record := &openv1.OpenRecord{
		RegisteredTrawlerManifestIdentity: c.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
		CanonicalOpenedRecordReference:    calendarEventRecord.GetCanonicalCalendarEventRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		TypedOpenedRecord: &openv1.OpenRecord_CalendarEventRecord{
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
