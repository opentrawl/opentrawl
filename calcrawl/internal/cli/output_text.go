package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	ckrender "github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

func printManifestText(w io.Writer, value manifestOutput) error {
	fields := []ckrender.CardField{
		{Label: "ID", Value: value.ID},
		{Label: "Version", Value: value.Version},
		{Label: "Contract", Value: fmt.Sprintf("v%d", value.ContractVersion)},
		{Label: "Archive schema", Value: strconv.Itoa(value.ArchiveSchemaVersion)},
		{Label: "Database", Value: value.Paths.DefaultDatabase},
		{Label: "Logs", Value: value.Paths.DefaultLogs},
	}
	return ckrender.WriteCard(w, ckrender.Card{
		Title:  value.DisplayName + " crawler",
		Fields: fields,
		Body:   value.Description,
		Hints:  []string{"JSON: calcrawl metadata --json"},
	})
}

func printStatusText(w io.Writer, value statusText) error {
	return ckrender.WriteStatus(w, renderStatus(value))
}

func renderStatus(value statusText) ckrender.Status {
	out := ckrender.Status{
		State:   ckrender.StatusState(value.State),
		Summary: value.Summary,
		Log:     value.Log,
		Errors:  value.Errors,
	}
	if value.Archive != nil {
		out.Sections = append(out.Sections, ckrender.Section{
			Title: "Local archive",
			Fields: []ckrender.Field{
				{Label: "Database", Value: value.Archive.ArchivePath},
				{Label: "Last sync", Value: shortLocalTime(parseEventTime(value.LastSyncAt))},
				{Label: "Calendars", Value: fmt.Sprintf("%d", value.Archive.Calendars)},
				{Label: "Events", Value: fmt.Sprintf("%d", value.Archive.Events)},
			},
		})
	}
	return out
}

func printDoctorText(w io.Writer, value doctorOutput) error {
	return ckrender.WriteDoctor(w, renderDoctorChecks(value.Checks), value.Log)
}

func renderDoctorChecks(checks []doctorCheck) []ckrender.Check {
	out := make([]ckrender.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, ckrender.Check{
			Name:    check.ID,
			State:   ckrender.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}

func printSearchText(w io.Writer, value searchOutput) error {
	hints := []string{"Open: calcrawl open REF"}
	if value.Truncated {
		hints = append(hints, searchMoreHint(value), searchAllHint(value))
	}
	return ckrender.WriteList(w, ckrender.List{
		Heading:   searchHeading(w, value),
		Hints:     hints,
		Items:     searchListItems(value.Results),
		ClampText: 2,
		Empty:     searchEmptyText(w, value),
	})
}

func searchHeading(w io.Writer, value searchOutput) string {
	who := ""
	if value.WhoResolved != nil && strings.TrimSpace(value.WhoResolved.Who) != "" {
		who = " with " + strings.TrimSpace(value.WhoResolved.Who)
	}
	returned := len(value.Results)
	total := value.TotalMatches
	query := strings.TrimSpace(value.Query)
	if query == "" {
		return fmt.Sprintf("Search filters%s: showing %d of %d.", who, returned, total)
	}
	prefix := "Search \""
	suffix := fmt.Sprintf("\"%s: showing %d of %d.", who, returned, total)
	queryWidth := ckrender.OutputWidth(w) - ckrender.DisplayWidth(prefix) - ckrender.DisplayWidth(suffix)
	if queryWidth < 1 {
		queryWidth = 1
	}
	return prefix + ckrender.Truncate(query, queryWidth) + suffix
}

func searchHintBase(value searchOutput) []string {
	parts := []string{"calcrawl", "search"}
	if query := strings.TrimSpace(value.Query); query != "" {
		parts = append(parts, shellQuote(query))
	}
	if value.WhoResolved != nil && strings.TrimSpace(value.WhoQuery) != "" {
		parts = append(parts, "--who", shellQuote(value.WhoQuery))
	}
	if value.After != "" {
		parts = append(parts, "--after", shellQuote(value.After))
	}
	if value.Before != "" {
		parts = append(parts, "--before", shellQuote(value.Before))
	}
	return parts
}

func searchMoreHint(value searchOutput) string {
	parts := append(searchHintBase(value), "--limit", strconv.Itoa(nextSearchLimit(value.Limit)))
	return "More: " + strings.Join(parts, " ")
}

func searchAllHint(value searchOutput) string {
	parts := append(searchHintBase(value), "--all")
	return "All: " + strings.Join(parts, " ")
}

func nextSearchLimit(limit int) int {
	if limit <= 0 {
		limit = archive.DefaultSearchLimit
	}
	return limit * 2
}

func searchEmptyText(w io.Writer, value searchOutput) string {
	query := strings.TrimSpace(value.Query)
	if query == "" {
		return "No matching events."
	}
	prefix := "No matches for \""
	suffix := "\"."
	queryWidth := ckrender.OutputWidth(w) - ckrender.DisplayWidth(prefix) - ckrender.DisplayWidth(suffix)
	if queryWidth < 1 {
		queryWidth = 1
	}
	return prefix + ckrender.Truncate(query, queryWidth) + suffix
}

func searchListItems(results []archive.SearchResult) []ckrender.ListItem {
	items := make([]ckrender.ListItem, 0, len(results))
	for _, result := range results {
		ref := result.ShortRef
		if ref == "" {
			ref = result.Ref
		}
		items = append(items, ckrender.ListItem{
			Time:     parseEventTime(result.Time),
			DateOnly: result.AllDay,
			Who:      result.Who,
			Where:    result.Where,
			Ref:      ref,
			Text:     result.Snippet,
		})
	}
	return items
}

func printOpenText(w io.Writer, event archive.EventDetail) error {
	fields := []ckrender.CardField{
		{Label: "When", Value: formatEventWhen(event.Start, event.End, event.AllDay)},
		{Label: "Location", Value: locationString(event.Location)},
		{Label: "Calendar", Value: event.Calendar},
		{Label: "Account", Value: event.Account},
		{Label: "Status", Value: event.Status},
		{Label: "Organizer", Value: personName(event.Organizer)},
		{Label: "Attendees", Value: attendeesLine(event.Attendees)},
		{Label: "Ref", Value: event.Ref},
	}
	return ckrender.WriteCard(w, ckrender.Card{
		Title:  event.Title,
		Fields: fields,
		Body:   event.Description,
		Hints:  []string{"JSON: add --json for the full record."},
	})
}

func printContactsText(w io.Writer, value control.ContactExport) error {
	if len(value.Contacts) == 0 {
		_, err := io.WriteString(w, "No contacts with phone numbers.\n")
		return err
	}
	if _, err := fmt.Fprintf(w, "Contacts: showing %d with phone numbers.\n\n", len(value.Contacts)); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Contacts))
	for _, contact := range value.Contacts {
		rows = append(rows, []string{contact.DisplayName, strings.Join(contact.PhoneNumbers, ", ")})
	}
	return ckrender.WriteTable(w, []ckrender.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "phone"},
	}, rows)
}

func parseEventTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t
	}
	return time.Time{}
}

func shortLocalTime(t time.Time) string {
	return ckrender.ShortLocalTime(t)
}

// formatEventWhen renders an event's span for a human. All-day events show the
// date in their own zone (a date is the same wall-clock day everywhere, so no
// timezone conversion), while timed events convert to the viewer's local zone
// the way Calendar.app does.
func formatEventWhen(startValue, endValue string, allDay bool) string {
	start := parseEventTime(startValue)
	if start.IsZero() {
		return ""
	}
	end := parseEventTime(endValue)
	if allDay {
		startDate := start.Format("2006-01-02")
		if !end.IsZero() {
			// The stored end is the exclusive next-day midnight; the last day
			// the event covers is the day before it.
			if last := end.AddDate(0, 0, -1).Format("2006-01-02"); last != startDate {
				return fmt.Sprintf("%s to %s (all day)", startDate, last)
			}
		}
		return startDate + " (all day)"
	}
	startLocal := start.Local()
	out := startLocal.Format("2006-01-02 15:04")
	if end.IsZero() {
		return out
	}
	endLocal := end.Local()
	if endLocal.Equal(startLocal) {
		return out
	}
	if endLocal.Year() == startLocal.Year() && endLocal.YearDay() == startLocal.YearDay() {
		return out + " to " + endLocal.Format("15:04")
	}
	return out + " to " + endLocal.Format("2006-01-02 15:04")
}

func locationString(location *archive.Location) string {
	if location == nil {
		return ""
	}
	return strings.Join(nonEmpty(location.Title, location.Address), ", ")
}

func personName(person archive.Person) string {
	return firstNonEmptyValue(person.DisplayName, person.Email, person.PhoneNumber)
}

func attendeesLine(attendees []archive.Attendee) string {
	parts := make([]string, 0, len(attendees))
	for _, attendee := range attendees {
		label := firstNonEmptyValue(attendee.DisplayName, attendee.Email, attendee.PhoneNumber, attendee.Address)
		if label == "" {
			continue
		}
		if strings.TrimSpace(attendee.RSVPStatus) != "" {
			label += " (" + strings.TrimSpace(attendee.RSVPStatus) + ")"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	return out
}
