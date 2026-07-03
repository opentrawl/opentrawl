package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func (r *runtime) print(value any) error {
	if r.json {
		enc := newJSONEncoder(r.stdout)
		return enc.Encode(value)
	}
	switch typed := value.(type) {
	case metadataEnvelope:
		return printMetadataText(r.stdout, typed)
	case statusEnvelope:
		return printStatusText(r.stdout, typed)
	case archive.SearchResult:
		return printSearchText(r.stdout, typed)
	case archive.WhoResult:
		return printWhoText(r.stdout, typed)
	case archive.OpenResult:
		return printOpenText(r.stdout, typed)
	case doctorOutput:
		return printDoctorText(r.stdout, typed)
	case control.ContactExport:
		return printContactsText(r.stdout, typed)
	default:
		return newJSONEncoder(r.stdout).Encode(value)
	}
}

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc
}

func printMetadataText(w io.Writer, value metadataEnvelope) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n%s\n", value.DisplayName, value.ID, value.Description); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\nCapabilities: %s\n", strings.Join(value.Capabilities, ", ")); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nMachine output: add --json for the control manifest.\n")
	return err
}

func printStatusText(w io.Writer, value statusEnvelope) error {
	return render.WriteStatus(w, renderStatus(value))
}

func renderStatus(value statusEnvelope) render.Status {
	out := render.Status{
		State:   render.StatusState(value.State),
		Summary: value.Summary,
		Log:     renderLogTail(value.LastRun, value.RecentError),
	}
	if value.Archive != nil {
		lastSync := ""
		if value.Freshness != nil {
			lastSync = value.Freshness.LastSync
		}
		out.Sections = append(out.Sections, render.Section{
			Title: "Local archive",
			Fields: []render.Field{
				{Label: "Database", Value: value.Archive.ArchivePath},
				{Label: "Last sync", Value: lastSync},
				{Label: "Messages", Value: fmt.Sprint(value.Archive.Messages)},
				{Label: "Senders", Value: fmt.Sprint(value.Archive.Senders)},
				{Label: "Since", Value: fmt.Sprint(value.Archive.Since)},
			},
		})
	}
	out.Sections = append(out.Sections, render.Section{
		Title:  "Auth",
		Fields: []render.Field{{Label: "Authorised", Value: fmt.Sprint(value.Auth.Authorized)}},
	})
	return out
}

func printSearchText(w io.Writer, value archive.SearchResult) error {
	if _, err := fmt.Fprintf(w, "Search %q: showing %d of %d.\n", value.Query, len(value.Results), value.TotalMatches); err != nil {
		return err
	}
	if value.WhoResolved != nil {
		query := value.WhoQuery
		if query == "" {
			query = strings.Join(value.WhoResolved.Identifiers, ", ")
		}
		if _, err := fmt.Fprintf(w, "%s → %s\n", query, value.WhoResolved.Who); err != nil {
			return err
		}
	}
	if value.Truncated {
		if _, err := io.WriteString(w, "More results exist; narrow with --after, --before or a more specific query.\n"); err != nil {
			return err
		}
	}
	for _, hit := range value.Results {
		ref := hit.Ref
		if hit.ShortRef != "" {
			ref = hit.ShortRef
		}
		if _, err := fmt.Fprintf(w, "%s  %s  %s  %s\n", hit.Time, hit.Who, ref, hit.Snippet); err != nil {
			return err
		}
	}
	return nil
}

func printWhoText(w io.Writer, value archive.WhoResult) error {
	return renderWhoTable(w, value.Candidates)
}

func printOpenText(w io.Writer, value archive.OpenResult) error {
	if _, err := fmt.Fprintf(w, "Message %s\nTime: %s\nFrom: %s\nTo: %s\n",
		value.Ref, value.Time, senderText(value.Headers), emptyDash(value.Headers.ToAddress)); err != nil {
		return err
	}
	if value.Headers.CcAddress != "" {
		if _, err := fmt.Fprintf(w, "Cc: %s\n", value.Headers.CcAddress); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Subject: %s\n", emptyDash(value.Headers.Subject)); err != nil {
		return err
	}
	if len(value.Attachments) > 0 {
		if _, err := io.WriteString(w, "Attachments:\n"); err != nil {
			return err
		}
		for _, attachment := range value.Attachments {
			if _, err := fmt.Fprintf(w, "  %s (%s, %d bytes)\n", emptyDash(attachment.Filename), emptyDash(attachment.MIMEType), attachment.Size); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", value.Body); err != nil {
		return err
	}
	return nil
}

func printDoctorText(w io.Writer, value doctorOutput) error {
	return render.WriteDoctor(w, renderDoctorChecks(value.Checks), renderLogTail(value.LastRun, value.RecentError))
}

func renderDoctorChecks(checks []doctorCheck) []render.Check {
	out := make([]render.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, render.Check{
			Name:    check.ID,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}

func renderLogTail(lastRun *logRunEnvelope, recentError *logErrorEnvelope) render.LogTail {
	return render.LogTail{
		LastRun:         renderRunSummary(lastRun),
		MostRecentError: renderLogError(recentError),
	}
}

func renderRunSummary(value *logRunEnvelope) *cklog.RunSummary {
	if value == nil {
		return nil
	}
	return &cklog.RunSummary{
		RunID:      value.RunID,
		Command:    value.Command,
		StartedAt:  parseLogRFC3339(value.StartedAt),
		FinishedAt: parseLogRFC3339(value.FinishedAt),
		Outcome:    value.Outcome,
		LastEvent:  value.LastEvent,
	}
}

func renderLogError(value *logErrorEnvelope) *cklog.Line {
	if value == nil {
		return nil
	}
	return &cklog.Line{
		RunID:     value.RunID,
		Command:   value.Command,
		Event:     value.Event,
		Timestamp: parseLogRFC3339(value.Time),
		Message:   value.Message,
	}
}

func parseLogRFC3339(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func printContactsText(w io.Writer, value control.ContactExport) error {
	for _, contact := range value.Contacts {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", contact.DisplayName, strings.Join(contact.PhoneNumbers, ",")); err != nil {
			return err
		}
	}
	return nil
}

func senderText(headers archive.MailHeaders) string {
	if headers.FromName != "" && headers.FromAddress != "" {
		return headers.FromName + " <" + headers.FromAddress + ">"
	}
	if headers.FromName != "" {
		return headers.FromName
	}
	return emptyDash(headers.FromAddress)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
