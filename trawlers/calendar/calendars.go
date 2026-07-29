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
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func (c *Crawler) calendars(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*commandv1.TrawlerCommandResponse, error) {
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
	archivedCalendars, err := archiveStore.Calendars(ctx)
	if err != nil {
		return nil, err
	}
	rows := make(
		[]*presentationv1.TrawlerSpecificCommandListPresentationRow,
		0,
		len(archivedCalendars),
	)
	for _, archivedCalendar := range archivedCalendars {
		rows = append(rows, &presentationv1.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentationv1.TrawlerSpecificCommandPresentationValue{
				calendarPresentationTextValue(strings.Join(strings.Fields(archivedCalendar.Title), " ")),
				calendarPresentationTextValue(strings.TrimSpace(archivedCalendar.AccountName)),
				calendarPresentationUnsignedCountValue(archivedCalendar.EventCount),
			},
		})
	}
	return calendarTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentationv1.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: []string{"Calendar", "Account", "Events"},
				RowsInDisplayOrder:        rows,
				TotalRowCount: &presentationv1.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: uint64(len(rows)),
				},
			},
		},
	}), nil
}

func (c *Crawler) annotateCalendar(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*commandv1.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 2 {
		return nil, output.UsageError{
			Err: errors.New("calendars annotate needs CALENDAR_ID and one quoted meaning"),
		}
	}
	meaning := req.TrawlerCommandPositionalArguments[1]
	if meaning == "" {
		return nil, output.UsageError{Err: errors.New("calendar meaning cannot be empty")}
	}
	archiveStore, err := archive.UseExisting(
		ctx,
		req.OpenedTrawlerArchiveStore,
		req.TrawlerArchivePaths.TrawlerArchivePath,
	)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	annotatedCalendar, err := archiveStore.SetCalendarMeaning(
		ctx,
		req.TrawlerCommandPositionalArguments[0],
		meaning,
		time.Now().UTC().Format("2006-01-02"),
	)
	if err != nil {
		return nil, err
	}
	return calendarTrawlerSpecificCommandResponse(&commandv1.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &commandv1.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentationv1.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName: "Calendar annotation recorded",
				FieldsInDisplayOrder: []*presentationv1.TrawlerSpecificCommandDetailPresentationField{
					calendarDetailTextField("Calendar", calendarDisplayName(annotatedCalendar.Title)),
					calendarDetailTextField("Meaning", annotatedCalendar.Meaning),
					calendarDetailTextField("Stated", annotatedCalendar.MeaningStatedAt),
				},
			},
		},
	}), nil
}

func calendarDisplayName(value string) string {
	if value = strings.Join(strings.Fields(value), " "); value != "" {
		return value
	}
	return "Untitled calendar"
}

func calendarTrawlerSpecificCommandResponse(
	trawlerSpecificCommandResponse *commandv1.TrawlerSpecificCommandResponse,
) *commandv1.TrawlerCommandResponse {
	return &commandv1.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func calendarPresentationTextValue(
	textValue string,
) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_Text{
			Text: textValue,
		},
	}
}

func calendarPresentationUnsignedCountValue(
	count int64,
) *presentationv1.TrawlerSpecificCommandPresentationValue {
	return &presentationv1.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentationv1.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func calendarDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentationv1.TrawlerSpecificCommandDetailPresentationField {
	return &presentationv1.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       calendarPresentationTextValue(textValue),
	}
}
