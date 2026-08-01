package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	calendarrecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
)

func (c *Crawler) calendars(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, output.UsageError{Err: errors.New("calendars takes no arguments")}
	}
	archiveStore, err := archive.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archivedCalendars, err := archiveStore.ListCalendarsWithActiveOrFutureEventCounts(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	calendarRecords := make([]*calendarrecord.CalendarRecord, 0, len(archivedCalendars))
	for _, archivedCalendar := range archivedCalendars {
		calendarRecords = append(calendarRecords, &calendarrecord.CalendarRecord{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
				archive.CalendarRefForID(archivedCalendar.ID),
			),
			CalendarDisplayName:                           strings.Join(strings.Fields(archivedCalendar.Title), " "),
			CalendarAccountDisplayName:                    strings.Join(strings.Fields(archivedCalendar.AccountName), " "),
			HumanEnteredCalendarOwnerOrPurposeDescription: strings.TrimSpace(archivedCalendar.Meaning),
			ActiveOrFutureCalendarEventCount:              uint64(max(archivedCalendar.ActiveOrFutureEventCount, 0)),
		})
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_CalendarListResponse{
			CalendarListResponse: &calendarrecord.CalendarListResponse{
				CalendarRecordsInDisplayOrder: calendarRecords,
			},
		},
	}, nil
}
