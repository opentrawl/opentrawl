package calcrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type calendarsOutput struct {
	Calendars []calendarRow  `json:"calendars"`
	Hints     []calendarHint `json:"hints,omitempty"`
}

type calendarRow struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Type             int64  `json:"type"`
	TypeLabel        string `json:"type_label"`
	AccountName      string `json:"account_name"`
	AccountType      int64  `json:"account_type"`
	AccountTypeLabel string `json:"account_type_label"`
	ExternalID       string `json:"external_id"`
	Disabled         bool   `json:"disabled"`
	EventCount       int64  `json:"event_count"`
	Meaning          string `json:"meaning"`
	MeaningStatedAt  string `json:"meaning_stated_at"`
}

type calendarHint struct {
	CalendarID string `json:"calendar_id"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	Command    string `json:"command"`
}

type calendarAnnotationOutput struct {
	Calendar calendarRow `json:"calendar"`
}

func (c *Crawler) calendars(ctx context.Context, req *crawlkit.Request) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	calendars, err := st.Calendars(ctx)
	if err != nil {
		return err
	}
	result := newCalendarsOutput(calendars)
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "calendars", result)
	}
	return writeCalendarsText(req.Out, result)
}

func (c *Crawler) annotateCalendar(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 2 {
		return output.UsageError{Err: errors.New("calendars annotate needs CALENDAR_ID and one quoted meaning")}
	}
	meaning := req.Args[1]
	if meaning == "" {
		return output.UsageError{Err: errors.New("calendar meaning cannot be empty")}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	calendar, err := st.SetCalendarMeaning(ctx, req.Args[0], meaning, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return err
	}
	result := calendarAnnotationOutput{Calendar: newCalendarRow(calendar)}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "calendar", result)
	}
	return writeCalendarAnnotationText(req.Out, result)
}

func newCalendarsOutput(calendars []archive.Calendar) calendarsOutput {
	rows := make([]calendarRow, 0, len(calendars))
	hints := []calendarHint{}
	for _, calendar := range calendars {
		row := newCalendarRow(calendar)
		rows = append(rows, row)
		if strings.TrimSpace(row.Meaning) == "" {
			hints = append(hints, hintForCalendar(row))
		}
	}
	return calendarsOutput{Calendars: rows, Hints: hints}
}

func newCalendarRow(calendar archive.Calendar) calendarRow {
	return calendarRow{
		ID:               calendar.ID,
		Title:            calendar.Title,
		Type:             calendar.Type,
		TypeLabel:        calendarTypeLabel(calendar.Type),
		AccountName:      calendar.AccountName,
		AccountType:      calendar.AccountType,
		AccountTypeLabel: accountTypeLabel(calendar.AccountType),
		ExternalID:       calendar.ExternalID,
		Disabled:         calendar.AccountDisabled,
		EventCount:       calendar.EventCount,
		Meaning:          calendar.Meaning,
		MeaningStatedAt:  calendar.MeaningStatedAt,
	}
}

func hintForCalendar(row calendarRow) calendarHint {
	title := strings.TrimSpace(row.Title)
	if title == "" {
		title = row.ID
	}
	return calendarHint{
		CalendarID: row.ID,
		Title:      row.Title,
		Prompt:     fmt.Sprintf("Ask the user what %q means to them, set CALENDAR_MEANING to their exact words.", title),
		Command:    "trawl calendar calendars annotate " + row.ID + ` "$CALENDAR_MEANING"`,
	}
}

func writeCalendarsText(w io.Writer, value calendarsOutput) error {
	if len(value.Calendars) == 0 {
		_, err := fmt.Fprintln(w, "No calendars archived. Run trawl calendar sync.")
		return err
	}
	if _, err := fmt.Fprintf(w, "Calendars: showing %s of %s.\n\n", render.FormatInteger(int64(len(value.Calendars))), render.FormatInteger(int64(len(value.Calendars)))); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Calendars))
	for _, calendar := range value.Calendars {
		rows = append(rows, []string{
			calendar.ID,
			calendar.Title,
			calendarTypeValue(calendar),
			calendar.AccountName,
			accountTypeValue(calendar),
			calendar.ExternalID,
			strconv.FormatBool(calendar.Disabled),
			render.FormatInteger(calendar.EventCount),
			calendar.Meaning,
			calendar.MeaningStatedAt,
		})
	}
	if err := render.WriteTable(w, []render.TableColumn{
		{Header: "id"},
		{Header: "title", Wrap: true},
		{Header: "type"},
		{Header: "account", Wrap: true},
		{Header: "account type"},
		{Header: "external id", Wrap: true},
		{Header: "disabled"},
		{Header: "events", AlignRight: true},
		{Header: "meaning", Wrap: true},
		{Header: "stated"},
	}, rows); err != nil {
		return err
	}
	return writeCalendarHints(w, value.Hints)
}

func writeCalendarHints(w io.Writer, hints []calendarHint) error {
	if len(hints) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	for _, hint := range hints {
		if _, err := fmt.Fprintf(w, "%s Run: %s\n", hint.Prompt, hint.Command); err != nil {
			return err
		}
	}
	return nil
}

func writeCalendarAnnotationText(w io.Writer, value calendarAnnotationOutput) error {
	return render.WriteCard(w, render.Card{
		Title: "Calendar annotation recorded",
		Fields: []render.CardField{
			{Label: "Calendar", Value: value.Calendar.Title},
			{Label: "Meaning", Value: value.Calendar.Meaning},
			{Label: "Stated", Value: value.Calendar.MeaningStatedAt},
		},
	})
}

func calendarTypeValue(row calendarRow) string {
	return row.TypeLabel + " (" + strconv.FormatInt(row.Type, 10) + ")"
}

func accountTypeValue(row calendarRow) string {
	return row.AccountTypeLabel + " (" + strconv.FormatInt(row.AccountType, 10) + ")"
}

func calendarTypeLabel(value int64) string {
	switch value {
	case 0:
		return "EKCalendarTypeLocal"
	case 1:
		return "EKCalendarTypeCalDAV"
	case 2:
		return "EKCalendarTypeExchange"
	case 3:
		return "EKCalendarTypeSubscription"
	case 4:
		return "EKCalendarTypeBirthday"
	default:
		return "unknown"
	}
}

func accountTypeLabel(value int64) string {
	switch value {
	case 0:
		return "EKSourceTypeLocal"
	case 1:
		return "EKSourceTypeExchange"
	case 2:
		return "EKSourceTypeCalDAV"
	case 3:
		return "EKSourceTypeMobileMe"
	case 4:
		return "EKSourceTypeSubscribed"
	case 5:
		return "EKSourceTypeBirthdays"
	default:
		return "unknown"
	}
}
