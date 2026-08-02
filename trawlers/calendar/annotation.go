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

func (c *Crawler) annotateCalendarOwnerOrPurpose(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 2 {
		return nil, output.UsageError{
			Err: errors.New("annotate needs LINK and one quoted owner or purpose"),
		}
	}
	ownerOrPurposeDescription := strings.TrimSpace(req.TrawlerCommandPositionalArguments[1])
	if ownerOrPurposeDescription == "" {
		return nil, output.UsageError{
			Err: errors.New("calendar owner or purpose cannot be empty"),
		}
	}
	calendarIdentifier, err := calendarIdentifierFromGloballyRoutableTrawlLink(
		ctx,
		req,
		req.TrawlerCommandPositionalArguments[0],
	)
	if err != nil {
		return nil, err
	}
	archiveStore, err := archive.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	today := time.Now()
	annotatedCalendar, err := archiveStore.SetCalendarOwnerOrPurposeAnnotation(
		ctx,
		calendarIdentifier,
		archive.CalendarOwnerOrPurposeAnnotation{
			CalendarOwnerOrPurposeDescription: ownerOrPurposeDescription,
			CalendarOwnerOrPurposeDescriptionStatedDate: archive.CalendarOwnerOrPurposeDescriptionStatedDate{
				CalendarYear:        int32(today.Year()),
				CalendarMonthNumber: int32(today.Month()),
				CalendarDayOfMonth:  int32(today.Day()),
			},
		},
	)
	if err != nil {
		return nil, err
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_CalendarListResponse{
			CalendarListResponse: &calendarrecord.CalendarListResponse{
				CalendarRecordsInDisplayOrder: []*calendarrecord.CalendarRecord{
					calendarRecordForProduct(annotatedCalendar),
				},
			},
		},
	}, nil
}
