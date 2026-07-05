package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
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
	case control.Manifest:
		return printMetadataText(r.stdout, typed)
	case statusEnvelope:
		return printStatusText(r.stdout, typed)
	case searchOutput:
		return printSearchText(r.stdout, typed)
	case archive.WhoResult:
		return printWhoText(r.stdout, typed)
	case openOutput:
		return printOpenText(r.stdout, typed)
	case doctorOutput:
		return printDoctorText(r.stdout, typed)
	case control.ContactExport:
		return printContactsText(r.stdout, typed)
	default:
		// Every envelope needs a designed human renderer; falling back to
		// JSON in human mode is a rendering defect (docs/rendering.md).
		return fmt.Errorf("no human renderer for %T", value)
	}
}

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc
}

func printMetadataText(w io.Writer, value control.Manifest) error {
	return render.WriteCard(w, render.Card{
		Title: value.DisplayName + " crawler",
		Fields: []render.CardField{
			{Label: "ID", Value: value.ID},
			{Label: "Version", Value: value.Version},
			{Label: "Contract", Value: fmt.Sprintf("v%d", value.ContractVersion)},
			{Label: "Database", Value: value.Paths.DefaultDatabase},
			{Label: "Logs", Value: value.Paths.DefaultLogs},
			{Label: "Capabilities", Value: strings.Join(value.Capabilities, ", ")},
		},
		Body:  value.Description,
		Hints: []string{"JSON: gogcrawl metadata --json"},
	})
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
			lastSync = render.ShortLocalTime(parseRFC3339(value.Freshness.LastSync))
		}
		out.Sections = append(out.Sections, render.Section{
			Title: "Local archive",
			Fields: []render.Field{
				{Label: "Database", Value: value.Archive.ArchivePath},
				{Label: "Last sync", Value: lastSync},
				{Label: "Messages", Value: fmt.Sprint(value.Archive.Messages)},
				{Label: "Senders", Value: fmt.Sprint(value.Archive.Senders)},
				{Label: "Oldest message", Value: fmt.Sprint(value.Archive.Since)},
			},
		})
	}
	out.Sections = append(out.Sections, render.Section{
		Title:  "Auth",
		Fields: []render.Field{{Label: "Authorised", Value: yesNo(value.Auth.Authorized)}},
	})
	return out
}

func printSearchText(w io.Writer, value searchOutput) error {
	if value.WhoResolved != nil {
		query := value.WhoQuery
		if query == "" {
			query = strings.Join(value.WhoResolved.Identifiers, ", ")
		}
		if _, err := fmt.Fprintf(w, "%s → %s\n\n", query, value.WhoResolved.Who); err != nil {
			return err
		}
	}
	hints := []string{"Open: gogcrawl open REF"}
	if value.Truncated {
		hints = append(hints, searchMoreHint(value))
	}
	return render.WriteList(w, render.List{
		Heading:   searchHeading(w, value.SearchResult),
		Hints:     hints,
		Items:     searchListItems(value.Results),
		ClampText: 2,
		Empty:     searchEmptyText(w, value.Query),
	})
}

func searchHeading(w io.Writer, value archive.SearchResult) string {
	if strings.TrimSpace(value.Query) == "" {
		return fmt.Sprintf("Search filters: showing %d of %d matches.", len(value.Results), value.TotalMatches)
	}
	prefix := "Search \""
	suffix := fmt.Sprintf("\": showing %d of %d matches.", len(value.Results), value.TotalMatches)
	return prefix + truncateToLine(w, value.Query, prefix, suffix) + suffix
}

func searchEmptyText(w io.Writer, query string) string {
	if strings.TrimSpace(query) == "" {
		return "No matching messages."
	}
	prefix := "No matches for \""
	suffix := "\"."
	return prefix + truncateToLine(w, query, prefix, suffix) + suffix
}

// truncateToLine fits a user-supplied query between prefix and suffix on one
// terminal line so headings never wrap.
func truncateToLine(w io.Writer, value, prefix, suffix string) string {
	width := render.OutputWidth(w) - render.DisplayWidth(prefix) - render.DisplayWidth(suffix)
	if width < 1 {
		width = 1
	}
	return render.Truncate(strings.TrimSpace(value), width)
}

func searchMoreHint(value searchOutput) string {
	parts := []string{"gogcrawl", "search"}
	if query := strings.TrimSpace(value.Query); query != "" {
		parts = append(parts, strconv.Quote(query))
	}
	if value.who != "" {
		parts = append(parts, "--who", strconv.Quote(value.who))
	}
	if value.after != "" {
		parts = append(parts, "--after", value.after)
	}
	if value.before != "" {
		parts = append(parts, "--before", value.before)
	}
	limit := value.limit
	if limit < 1 {
		limit = defaultSearchLimit
	}
	parts = append(parts, "--limit", strconv.Itoa(limit*2))
	return "More: " + strings.Join(parts, " ")
}

func searchListItems(hits []archive.SearchHit) []render.ListItem {
	items := make([]render.ListItem, 0, len(hits))
	for _, hit := range hits {
		ref := hit.ShortRef
		if ref == "" {
			ref = hit.Ref
		}
		items = append(items, render.ListItem{
			Time: parseRFC3339(hit.Time),
			Who:  hit.Who,
			Ref:  ref,
			Text: hit.Snippet,
		})
	}
	return items
}

func printWhoText(w io.Writer, value archive.WhoResult) error {
	if len(value.Candidates) == 0 {
		_, err := fmt.Fprintf(w, "No people matched %q.\n", value.Query)
		return err
	}
	return writeWhoTable(w, value.Candidates)
}

func writeWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			render.ShortLocalTime(parseRFC3339(candidate.LastSeen)),
			strconv.FormatInt(candidate.Messages, 10),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who"},
		{Header: "last seen"},
		{Header: "messages", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

func printOpenText(w io.Writer, value openOutput) error {
	title := strings.TrimSpace(value.Headers.Subject)
	if title == "" {
		title = "(no subject)"
	}
	hints := make([]string, 0, 2)
	if value.BodyTruncated {
		hints = append(hints, fmt.Sprintf("… %s more characters. Open the full message in Gmail.", commaInt(value.BodyElidedChars)))
	}
	hints = append(hints, "JSON: gogcrawl open REF --json for the full record.")
	return render.WriteCard(w, render.Card{
		Title: title,
		Fields: []render.CardField{
			{Label: "Date", Value: render.ShortLocalTime(parseRFC3339(value.Time))},
			{Label: "From", Value: senderText(value.Headers)},
			{Label: "To", Value: value.Headers.ToAddress},
			{Label: "Cc", Value: value.Headers.CcAddress},
			{Label: "Attachments", Value: attachmentsLine(value.Attachments)},
			// The short alias, never the full machine ref: full refs
			// stay in JSON (docs/rendering.md).
			{Label: "Ref", Value: value.shortRef},
		},
		Body:  value.Body,
		Hints: hints,
	})
}

func attachmentsLine(attachments []archive.Attachment) string {
	parts := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		name := strings.TrimSpace(attachment.Filename)
		if name == "" {
			name = "(unnamed)"
		}
		parts = append(parts, fmt.Sprintf("%s (%s bytes)", name, commaInt(int(attachment.Size))))
	}
	return strings.Join(parts, ", ")
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
		StartedAt:  parseRFC3339(value.StartedAt),
		FinishedAt: parseRFC3339(value.FinishedAt),
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
		Timestamp: parseRFC3339(value.Time),
		Message:   value.Message,
	}
}

func parseRFC3339(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
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
	return render.WriteTable(w, []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "phone"},
	}, rows)
}

func senderText(headers archive.MailHeaders) string {
	if headers.FromName != "" && headers.FromAddress != "" {
		return headers.FromName + " <" + headers.FromAddress + ">"
	}
	if headers.FromName != "" {
		return headers.FromName
	}
	return headers.FromAddress
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
