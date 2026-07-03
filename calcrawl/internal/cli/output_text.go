package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/openclaw/crawlkit/control"
	ckrender "github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

func printManifestText(w io.Writer, value manifestOutput) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n", value.DisplayName, value.ID); err != nil {
		return err
	}
	if value.Description != "" {
		if _, err := fmt.Fprintln(w, value.Description); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nVersion: %s\nContract: v%d\nArchive schema: %d\n", value.Version, value.ContractVersion, value.ArchiveSchemaVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Capabilities: %s\n", strings.Join(value.Capabilities, ", ")); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\nAgent-facing commands:\n"); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, name := range []string{"metadata", "status", "sync", "search", "who", "open", "doctor", "contacts_export"} {
		command, ok := value.Commands[name]
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(tw, "  %s\t%s\n", name, strings.Join(command.Argv, " ")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nMachine output: add --json.\n")
	return err
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
				{Label: "Last sync", Value: value.LastSyncAt},
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
	label := "Search"
	if value.Query != "" {
		label = fmt.Sprintf("Search %q", value.Query)
	}
	if value.WhoResolved != nil && value.WhoQuery != "" {
		if _, err := fmt.Fprintf(w, "%s → %s\n", value.WhoQuery, value.WhoResolved.Who); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%s: showing %d of %d.\n", label, len(value.Results), value.TotalMatches); err != nil {
		return err
	}
	if value.Truncated {
		if _, err := io.WriteString(w, "More: narrow with --after, --before or --limit.\n"); err != nil {
			return err
		}
	}
	if len(value.Results) == 0 {
		_, err := io.WriteString(w, "No matching events.\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "\ntime\twho\twhere\tsnippet\tref"); err != nil {
		return err
	}
	for _, result := range value.Results {
		ref := result.ShortRef
		if ref == "" {
			ref = result.Ref
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", result.Time, result.Who, result.Where, result.Snippet, ref); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func printOpenText(w io.Writer, event archive.EventDetail) error {
	if _, err := fmt.Fprintf(w, "%s\n%s to %s\n", event.Title, event.Start, event.End); err != nil {
		return err
	}
	if event.Location != nil {
		location := strings.Join(nonEmpty(event.Location.Title, event.Location.Address), ", ")
		if _, err := fmt.Fprintf(w, "Location: %s\n", location); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Calendar: %s\nAccount: %s\nStatus: %s\nRef: %s\n", event.Calendar, event.Account, emptyDash(event.Status), event.Ref); err != nil {
		return err
	}
	if len(event.Attendees) > 0 {
		if _, err := io.WriteString(w, "\nAttendees:\n"); err != nil {
			return err
		}
		for _, attendee := range event.Attendees {
			label := attendee.DisplayName
			if label == "" {
				label = attendee.Email
			}
			if label == "" {
				label = attendee.PhoneNumber
			}
			if attendee.RSVPStatus != "" {
				label += " (" + attendee.RSVPStatus + ")"
			}
			if _, err := fmt.Fprintf(w, "  - %s\n", label); err != nil {
				return err
			}
		}
	}
	if event.Description != "" {
		if _, err := fmt.Fprintf(w, "\nDescription:\n%s\n", event.Description); err != nil {
			return err
		}
	}
	return nil
}

func printContactsText(w io.Writer, value control.ContactExport) error {
	if _, err := fmt.Fprintf(w, "Contacts export: %d contacts.\n", len(value.Contacts)); err != nil {
		return err
	}
	for _, contact := range value.Contacts {
		if _, err := fmt.Fprintf(w, "  - %s: %s\n", contact.DisplayName, strings.Join(contact.PhoneNumbers, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
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
