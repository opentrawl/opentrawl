package calendar

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	calendarevent "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultEventListLimit = 20

func (c *Crawler) bindEventsFlags(flagSet *flag.FlagSet) {
	c.eventsLimit = defaultEventListLimit
	flagSet.IntVar(&c.eventsLimit, "limit", defaultEventListLimit, "Maximum number of events")
}

func (c *Crawler) runEvents(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) > 2 {
		return nil, output.UsageError{Err: fmt.Errorf("events takes at most one calendar and one account")}
	}
	if c.eventsLimit < 1 {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage("--limit must be at least 1.")}
	}
	calendarDisplayNameFilter := ""
	calendarAccountDisplayNameFilter := ""
	if len(req.TrawlerCommandPositionalArguments) > 0 {
		calendarDisplayNameFilter = req.TrawlerCommandPositionalArguments[0]
	}
	if len(req.TrawlerCommandPositionalArguments) > 1 {
		calendarAccountDisplayNameFilter = req.TrawlerCommandPositionalArguments[1]
	}
	store, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archiveEvents, err := store.ListUpcomingEvents(
		ctx,
		time.Now(),
		c.eventsLimit+1,
		calendarDisplayNameFilter,
		calendarAccountDisplayNameFilter,
	)
	if err != nil {
		return nil, err
	}
	moreEventsExist := len(archiveEvents) > c.eventsLimit
	totalObservedEventCount := len(archiveEvents)
	if moreEventsExist {
		archiveEvents = archiveEvents[:c.eventsLimit]
	}
	eventRecords := make([]*calendarevent.CalendarEventRecord, 0, len(archiveEvents))
	for _, archiveEvent := range archiveEvents {
		eventRecords = append(
			eventRecords,
			projectCalendarEventRecord(
				calendarEventRecordValuesFromListItem(archiveEvent),
			),
		)
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_CalendarEventListResponse{
			CalendarEventListResponse: &calendarevent.CalendarEventListResponse{
				CalendarEventRecordsInDisplayOrder:          eventRecords,
				TotalMatchingCalendarEventCount:             uint64(totalObservedEventCount),
				TotalMatchingCalendarEventCountIsLowerBound: moreEventsExist,
				MoreMatchingCalendarEventsExist:             moreEventsExist,
			},
		},
	}, nil
}

func calendarEventStartTimeForDisplay(
	storedStartTime string,
	allDay bool,
) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	parsed, err := parseEventTime(storedStartTime)
	if err != nil || parsed.IsZero() {
		return nil
	}
	if allDay {
		return &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
				CalendarDate: &presentation.CalendarDate{
					CalendarYear:        int32(parsed.Year()),
					CalendarMonthNumber: int32(parsed.Month()),
					CalendarDayOfMonth:  int32(parsed.Day()),
				},
			},
		}
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
			ExactTime: timestamppb.New(parsed),
		},
	}
}

func calendarEventEndTimeForDisplay(
	storedStartTime string,
	storedEndTime string,
	allDay bool,
) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	startTime, startTimeError := parseEventTime(storedStartTime)
	endTime, endTimeError := parseEventTime(storedEndTime)
	if startTimeError != nil || endTimeError != nil || startTime.IsZero() || endTime.IsZero() {
		return nil
	}
	if !allDay {
		if endTime.Before(startTime) {
			return nil
		}
		return &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
				ExactTime: timestamppb.New(endTime),
			},
		}
	}
	if endTime.After(startTime) {
		endTime = endTime.AddDate(0, 0, -1)
	}
	if endTime.Before(startTime) {
		return nil
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
			CalendarDate: &presentation.CalendarDate{
				CalendarYear:        int32(endTime.Year()),
				CalendarMonthNumber: int32(endTime.Month()),
				CalendarDayOfMonth:  int32(endTime.Day()),
			},
		},
	}
}

func calendarEventDisplayName(storedEventTitle string) string {
	if storedEventTitle = strings.Join(strings.Fields(storedEventTitle), " "); storedEventTitle != "" {
		return storedEventTitle
	}
	return "Calendar event"
}
