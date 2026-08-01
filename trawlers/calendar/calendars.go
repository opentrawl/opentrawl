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
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
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
	archivedCalendars, err := archiveStore.ListCalendarsWithUpcomingEventCounts(ctx, time.Now())
	if err != nil {
		return nil, err
	}
	rows := make(
		[]*presentation.TrawlerSpecificCommandListPresentationRow,
		0,
		len(archivedCalendars),
	)
	for _, archivedCalendar := range archivedCalendars {
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				calendarPresentationTextValue(strings.TrimSpace(archivedCalendar.Title)),
				calendarPresentationTextValue(strings.TrimSpace(archivedCalendar.AccountName)),
				calendarPresentationUnsignedCountValue(archivedCalendar.UpcomingEventCount),
			},
		})
	}
	return calendarTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
			TrawlerSpecificCommandListPresentation: &presentation.TrawlerSpecificCommandListPresentation{
				ColumnDisplayNamesInOrder: []string{"Calendar", "Account", "Upcoming events"},
				RowsInDisplayOrder:        rows,
				TotalRowCount: &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
					ExactTotalRowCount: uint64(len(rows)),
				},
			},
		},
	}), nil
}

func calendarListTrawlCommandActions(
	response *command.TrawlerCommandResponse,
) render.TrawlerSpecificCommandActions {
	listPresentation := response.GetTrawlerSpecificCommandResponse().GetTrawlerSpecificCommandListPresentation()
	actions := make([]*render.TrawlCommandAction, 0, len(listPresentation.GetRowsInDisplayOrder()))
	for _, row := range listPresentation.GetRowsInDisplayOrder() {
		if row == nil || len(row.GetColumnValuesInDisplayOrder()) < 3 {
			actions = append(actions, nil)
			continue
		}
		calendarDisplayName := row.GetColumnValuesInDisplayOrder()[0].GetText()
		calendarAccountDisplayName := row.GetColumnValuesInDisplayOrder()[1].GetText()
		if row.GetColumnValuesInDisplayOrder()[2].GetUnsignedCount() == 0 {
			actions = append(actions, nil)
			continue
		}
		actions = append(actions, &render.TrawlCommandAction{
			TrawlCommandActionDisplayName: "List events",
			CommandArgumentsAfterTrawlInvocationInOrder: []render.TrawlCommandArgument{
				render.TrawlCommandTextArgument{Text: "calendar"},
				render.TrawlCommandTextArgument{Text: "events"},
				render.TrawlCommandTextArgument{Text: calendarDisplayName},
				render.TrawlCommandTextArgument{Text: calendarAccountDisplayName},
			},
		})
	}
	return render.TrawlerSpecificCommandActions{ListRowActionsInDisplayOrder: actions}
}

func (c *Crawler) annotateCalendar(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*command.TrawlerCommandResponse, error) {
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
	return calendarTrawlerSpecificCommandResponse(&command.TrawlerSpecificCommandResponse{
		TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
			TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
				DetailDisplayName: "Calendar annotation recorded",
				FieldsInDisplayOrder: []*presentation.TrawlerSpecificCommandDetailPresentationField{
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
	trawlerSpecificCommandResponse *command.TrawlerSpecificCommandResponse,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: trawlerSpecificCommandResponse,
		},
	}
}

func calendarPresentationTextValue(
	textValue string,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{
			Text: textValue,
		},
	}
}

func calendarPresentationUnsignedCountValue(
	count int64,
) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{
			UnsignedCount: uint64(max(count, 0)),
		},
	}
}

func calendarDetailTextField(
	fieldDisplayName string,
	textValue string,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       calendarPresentationTextValue(textValue),
	}
}
