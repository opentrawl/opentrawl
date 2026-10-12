package calendar

import (
	"context"
	"errors"
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
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultEventListLimit = 20

func (c *Crawler) bindEventsFlags(flagSet *flag.FlagSet) {
	c.eventsLimit = defaultEventListLimit
	flagSet.IntVar(&c.eventsLimit, "limit", defaultEventListLimit, "Maximum number of events")
}

func (c *Crawler) runEvents(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if c.eventsLimit < 1 {
		return nil, output.UsageError{Err: render.LimitMustBeAtLeastOne()}
	}
	calendarIdentifierFilter := archive.CalendarIdentifier("")
	if len(req.TrawlerCommandPositionalArguments) == 1 {
		var err error
		calendarIdentifierFilter, err = calendarIdentifierFromGloballyRoutableTrawlLink(
			ctx,
			req,
			req.TrawlerCommandPositionalArguments[0],
		)
		if err != nil {
			return nil, err
		}
	}
	store, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	archiveEvents, err := store.ListActiveOrFutureEvents(
		ctx,
		time.Now(),
		c.eventsLimit+1,
		calendarIdentifierFilter,
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

func calendarIdentifierFromGloballyRoutableTrawlLink(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	globallyRoutableCalendarTrawlLink string,
) (archive.CalendarIdentifier, error) {
	localCalendarShortReference, argumentWasGloballyRoutableTrawlLink, err :=
		trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(
			globallyRoutableCalendarTrawlLink,
			archive.AppID,
		)
	if err != nil {
		return archive.CalendarIdentifier(""), err
	}
	if !argumentWasGloballyRoutableTrawlLink {
		return archive.CalendarIdentifier(""), output.UsageError{
			Err: render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounCalendar),
		}
	}
	canonicalCalendarRecordReferences, err := req.ResolveShortReference(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(localCalendarShortReference),
	)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return archive.CalendarIdentifier(""), commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounCalendar))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return archive.CalendarIdentifier(""), commandErr(1, "ambiguous", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounCalendar))
	}
	if err != nil {
		return archive.CalendarIdentifier(""), err
	}
	calendarIdentifier, calendarRecordReferenceIsValid := archive.CalendarIdentifierFromCanonicalRecordReference(
		trawlkit.CanonicalArchiveRecordReferenceText(canonicalCalendarRecordReferences[0]),
	)
	if !calendarRecordReferenceIsValid {
		return archive.CalendarIdentifier(""), commandErr(1, "not_found", render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounCalendar))
	}
	return calendarIdentifier, nil
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
	return exactCalendarEventTimeWithRecordedUTCOffset(parsed)
}

// exactCalendarEventTimeWithRecordedUTCOffset keeps the offset the archive
// stored with the event's own time zone, so the event shows in local time.
func exactCalendarEventTimeWithRecordedUTCOffset(eventTime time.Time) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	_, eventUTCOffsetSeconds := eventTime.Zone()
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{
		ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
			ExactTime: timestamppb.New(eventTime),
		},
		EventUtcOffsetSecondsRecordedBySourceArchive: proto.Int32(int32(eventUTCOffsetSeconds)),
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
		return exactCalendarEventTimeWithRecordedUTCOffset(endTime)
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
