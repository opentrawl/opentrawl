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
	calendareventv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/calendar_event/v1"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultEventListLimit = 20

func (c *Crawler) bindEventsFlags(flagSet *flag.FlagSet) {
	c.eventsLimit = defaultEventListLimit
	flagSet.IntVar(&c.eventsLimit, "limit", defaultEventListLimit, "Maximum number of events")
}

func (c *Crawler) runEvents(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, output.UsageError{Err: fmt.Errorf("events takes flags only")}
	}
	if c.eventsLimit < 1 {
		return nil, output.UsageError{Err: fmt.Errorf("--limit must be at least 1.")}
	}
	store, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archiveEvents, err := store.ListUpcomingEvents(ctx, time.Now(), c.eventsLimit+1)
	if err != nil {
		return nil, err
	}
	moreEventsExist := len(archiveEvents) > c.eventsLimit
	totalObservedEventCount := len(archiveEvents)
	if moreEventsExist {
		archiveEvents = archiveEvents[:c.eventsLimit]
	}
	eventRecords := make([]*calendareventv1.CalendarEventRecord, 0, len(archiveEvents))
	for _, archiveEvent := range archiveEvents {
		eventRecords = append(
			eventRecords,
			projectCalendarEventRecord(
				calendarEventRecordValuesFromListItem(archiveEvent),
			),
		)
	}
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_CalendarEventListResponse{
			CalendarEventListResponse: &calendareventv1.CalendarEventListResponse{
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
) *presentationv1.ArchiveRecordAssociatedTimeForDisplay {
	parsed, err := parseEventTime(storedStartTime)
	if err != nil || parsed.IsZero() {
		return nil
	}
	if allDay {
		return &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
				CalendarDate: &presentationv1.CalendarDate{
					CalendarYear:        int32(parsed.Year()),
					CalendarMonthNumber: int32(parsed.Month()),
					CalendarDayOfMonth:  int32(parsed.Day()),
				},
			},
		}
	}
	return &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
			ExactTime: timestamppb.New(parsed),
		},
	}
}

func calendarEventEndTimeForDisplay(
	storedStartTime string,
	storedEndTime string,
	allDay bool,
) *presentationv1.ArchiveRecordAssociatedTimeForDisplay {
	if !allDay {
		return calendarEventStartTimeForDisplay(storedEndTime, false)
	}
	startTime, startTimeError := parseEventTime(storedStartTime)
	endTime, endTimeError := parseEventTime(storedEndTime)
	if startTimeError != nil || endTimeError != nil || endTime.IsZero() {
		return nil
	}
	if endTime.After(startTime) {
		endTime = endTime.AddDate(0, 0, -1)
	}
	return &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
			CalendarDate: &presentationv1.CalendarDate{
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
