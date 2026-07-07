package calcrawl

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := resolveOpenRef(ctx, st, ref)
	if err != nil {
		return err
	}
	event, err := st.OpenEvent(ctx, resolved)
	if err != nil {
		return err
	}
	_ = req.Log.Info("open_complete", "result=event")
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", event)
	}
	return printOpenText(req.Out, event)
}

func resolveOpenRef(ctx context.Context, st *archive.Store, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, nil
	}
	if !shortref.ValidAlias(ref) {
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	}
	matches, err := st.ResolveShortRef(ctx, ref)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	case 1:
		return matches[0], nil
	default:
		return "", commandErr(1, "ambiguous_short_ref", fmt.Errorf("short ref %q matches %d events", ref, len(matches)), "rerun search or use the full ref")
	}
}

func printOpenText(w io.Writer, event archive.EventDetail) error {
	fields := []render.CardField{
		{Label: "When", Value: formatEventWhen(event.Start, event.End, event.AllDay)},
		{Label: "Location", Value: locationString(event.Location)},
		{Label: "Calendar", Value: event.Calendar},
		{Label: "Account", Value: event.Account},
		{Label: "Status", Value: event.Status},
		{Label: "Organizer", Value: personName(event.Organizer)},
		{Label: "Attendees", Value: attendeesLine(event.Attendees)},
		{Label: "Ref", Value: event.Ref},
	}
	return render.WriteCard(w, render.Card{
		Title:  event.Title,
		Fields: fields,
		Body:   event.Description,
		Hints:  []string{"JSON: add --json for the full record."},
	})
}

func formatEventWhen(startValue, endValue string, allDay bool) string {
	start, err := parseEventTime(startValue)
	if err != nil || start.IsZero() {
		return ""
	}
	end, _ := parseEventTime(endValue)
	if allDay {
		startDate := start.Format("2006-01-02")
		if !end.IsZero() {
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
