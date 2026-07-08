package crawlkit

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/whomatch"
)

type searchOutput struct {
	Query    string `json:"query"`
	SourceID string `json:"-"`
	SearchResult
}

type whoOutput struct {
	Query      string               `json:"query"`
	Candidates []whoCandidateOutput `json:"candidates"`
}

type whoCandidateOutput struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int64    `json:"messages"`
	lastSeen    time.Time
}

func writeResult(w io.Writer, format output.Format, label string, value any) error {
	if format != output.Text {
		value = normalizeJSONResult(value)
		return output.Write(w, format, label, value)
	}
	switch v := value.(type) {
	case control.Manifest:
		return writeManifestText(w, v)
	case *control.Status:
		return render.WriteStatus(w, renderStatus(v))
	case *Doctor:
		return render.WriteDoctor(w, renderDoctorChecks(v), render.LogTail{})
	case *SyncReport:
		return writeSyncReportText(w, v)
	case searchOutput:
		return writeSearchText(w, v)
	case whoOutput:
		return writeWhoText(w, v)
	case *control.ContactExport:
		return writeContactsText(w, v)
	default:
		return output.Write(w, format, label, value)
	}
}

func normalizeJSONResult(value any) any {
	switch v := value.(type) {
	case searchOutput:
		if v.Results == nil {
			v.Results = []Hit{}
		}
		return v
	case *Doctor:
		if v == nil {
			return &Doctor{Checks: []Check{}}
		}
		out := *v
		if out.Checks == nil {
			out.Checks = []Check{}
		}
		return &out
	case *control.ContactExport:
		if v == nil {
			return &control.ContactExport{Contacts: []control.Contact{}}
		}
		out := *v
		if out.Contacts == nil {
			out.Contacts = []control.Contact{}
		}
		return &out
	case whoOutput:
		if v.Candidates == nil {
			v.Candidates = []whoCandidateOutput{}
		}
		return v
	default:
		return value
	}
}

func writeManifestText(w io.Writer, manifest control.Manifest) error {
	fields := []render.CardField{
		{Label: "ID", Value: manifest.ID},
		{Label: "Name", Value: manifest.DisplayName},
		{Label: "Version", Value: manifest.Version},
		{Label: "Commands", Value: render.FormatInteger(int64(len(manifest.Commands)))},
		{Label: "Database", Value: manifest.Paths.DefaultDatabase},
		{Label: "Logs", Value: manifest.Paths.DefaultLogs},
	}
	return render.WriteCard(w, render.Card{Title: "Metadata", Fields: fields, Body: manifest.Description})
}

func renderStatus(status *control.Status) render.Status {
	if status == nil {
		return render.Status{State: render.StatusUnknown, Summary: "No status returned."}
	}
	out := render.Status{
		State:    render.StatusState(status.State),
		Summary:  statusSummary(status),
		Warnings: append([]string(nil), status.Warnings...),
		Errors:   append([]string(nil), status.Errors...),
	}
	if len(status.Counts) > 0 {
		archiveFields := archiveStatusFields(status)
		if len(archiveFields) > 0 {
			out.Sections = append(out.Sections, render.Section{Title: "Local archive", Fields: archiveFields})
		}
	} else {
		var archiveFields []render.Field
		if status.ConfigPath != "" {
			archiveFields = append(archiveFields, render.Field{Label: "Config", Value: status.ConfigPath})
		}
		if status.DatabasePath != "" {
			archiveFields = append(archiveFields, render.Field{Label: "Database", Value: status.DatabasePath})
		}
		if status.DatabaseBytes > 0 {
			archiveFields = append(archiveFields, render.Field{Label: "Database size", Value: render.FormatInteger(status.DatabaseBytes) + " bytes"})
		}
		if status.WALBytes > 0 {
			archiveFields = append(archiveFields, render.Field{Label: "WAL size", Value: render.FormatInteger(status.WALBytes) + " bytes"})
		}
		if len(archiveFields) > 0 {
			out.Sections = append(out.Sections, render.Section{Title: "Archive", Fields: archiveFields})
		}
	}
	if status.LastSyncAt == "" && status.LastImportAt != "" {
		out.Freshness = &render.Freshness{Label: "Last import", LastSync: status.LastImportAt}
	}
	if status.Freshness != nil && status.Freshness.Status != "" {
		if out.Freshness == nil {
			out.Freshness = &render.Freshness{}
		}
		out.Freshness.State = status.Freshness.Status
	}
	return out
}

func archiveStatusFields(status *control.Status) []render.Field {
	var fields []render.Field
	if status.ConfigPath != "" {
		fields = append(fields, render.Field{Label: "Config", Value: status.ConfigPath})
	}
	if status.DatabasePath != "" {
		fields = append(fields, render.Field{Label: "Database", Value: status.DatabasePath})
	}
	if status.LastSyncAt != "" {
		fields = append(fields, render.Field{Label: "Last sync", Value: shortStatusTime(status.LastSyncAt)})
	}
	for _, count := range status.Counts {
		label := firstText(count.Label, count.ID)
		fields = append(fields, render.Field{Label: displayFieldLabel(label), Value: render.FormatCount(count.Value, count.ID, label)})
	}
	return fields
}

func statusSummary(status *control.Status) string {
	if status == nil {
		return "No status returned."
	}
	switch strings.TrimSpace(status.State) {
	case "ok":
		return "Recently synced."
	case "stale":
		if strings.TrimSpace(status.Summary) != "" {
			return status.Summary
		}
		return "Needs sync."
	default:
		return status.Summary
	}
}

func displayFieldLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func shortStatusTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return render.ShortLocalTime(t)
}

func renderDoctorChecks(doctor *Doctor) []render.Check {
	if doctor == nil {
		return []render.Check{{Name: "runner", State: render.CheckFail, Message: "no doctor result returned"}}
	}
	checks := make([]render.Check, 0, len(doctor.Checks))
	for _, check := range doctor.Checks {
		checks = append(checks, render.Check{
			Name:    check.ID,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return checks
}

func writeSyncReportText(w io.Writer, report *SyncReport) error {
	if report == nil {
		return render.WriteCard(w, render.Card{Title: "Sync complete"})
	}
	fields := []render.CardField{
		{Label: "Added", Value: render.FormatInteger(report.Added)},
		{Label: "Updated", Value: render.FormatInteger(report.Updated)},
		{Label: "Removed", Value: render.FormatInteger(report.Removed)},
	}
	return render.WriteCard(w, render.Card{Title: "Sync complete", Fields: fields, Hints: report.Warnings})
}

func writeSearchText(w io.Writer, value searchOutput) error {
	items := make([]render.ListItem, 0, len(value.Results))
	for _, hit := range value.Results {
		items = append(items, render.ListItem{
			Time:     hit.Time,
			DateOnly: hit.AllDay,
			Source:   hit.Source,
			Who:      hit.Who,
			Where:    hit.Where,
			Ref:      hit.Ref,
			Text:     hit.Snippet,
		})
	}
	hints := []string{}
	sourceID := strings.TrimSpace(value.SourceID)
	if sourceID != "" {
		hints = append(hints, "Open: trawl "+sourceID+" open REF")
	}
	if value.Truncated {
		if sourceID != "" && strings.TrimSpace(value.Query) != "" {
			hints = append(hints, fmt.Sprintf("More: trawl %s search %s --limit %d", sourceID, quoteSearchArg(value.Query), nextSearchLimit(len(value.Results))))
		}
		hints = append(hints, "Narrow results with --who, --after, or --before.")
	}
	return render.WriteList(w, render.List{
		Heading:   searchHeading(value.Query, resolvedWhoName(value.WhoResolved), len(value.Results), max(value.TotalMatches, len(value.Results))),
		Hints:     hints,
		Items:     items,
		ClampText: 2,
		Empty:     searchEmptyText(value.Query),
	})
}

func nextSearchLimit(shown int) int {
	if shown < 1 {
		return 20
	}
	return shown * 2
}

func quoteSearchArg(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func writeWhoText(w io.Writer, value whoOutput) error {
	rows := make([][]string, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		rows = append(rows, []string{
			candidate.Who,
			render.ShortLocalTime(candidate.lastSeen),
			render.FormatInteger(candidate.Messages),
			strings.Join(humanIdentifiers(candidate.Identifiers), ", "),
		})
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No people matched %q.\n", value.Query)
		return err
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who", Wrap: true},
		{Header: "last seen"},
		{Header: "items", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

func newWhoOutput(query string, candidates []whomatch.Candidate) whoOutput {
	return whoOutput{Query: strings.Join(strings.Fields(query), " "), Candidates: whoCandidateOutputs(candidates)}
}

func whoCandidateOutputs(candidates []whomatch.Candidate) []whoCandidateOutput {
	out := make([]whoCandidateOutput, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidateOutput{
			Who:         strings.Join(strings.Fields(candidate.Who), " "),
			Identifiers: append([]string{}, candidate.Identifiers...),
			LastSeen:    formatContractTime(candidate.LastSeen),
			Messages:    candidate.Messages,
			lastSeen:    candidate.LastSeen,
		})
	}
	return out
}

func newWhoResolved(candidate whomatch.Candidate) *WhoResolved {
	return &WhoResolved{
		Who:         strings.Join(strings.Fields(candidate.Who), " "),
		Identifiers: append([]string{}, candidate.Identifiers...),
	}
}

func formatContractTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func writeContactsText(w io.Writer, value *control.ContactExport) error {
	if value == nil || len(value.Contacts) == 0 {
		_, err := fmt.Fprintln(w, "No contacts.")
		return err
	}
	if _, err := fmt.Fprintf(w, "Contacts: showing %s of %s.\n\n", render.FormatInteger(int64(len(value.Contacts))), render.FormatInteger(int64(len(value.Contacts)))); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Contacts))
	for _, contact := range value.Contacts {
		rows = append(rows, []string{contact.DisplayName, strings.Join(humanPhones(contact.PhoneNumbers), ", ")})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "phone"},
	}, rows)
}

func humanPhones(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, render.FormatPhone(value))
	}
	return out
}

func humanIdentifiers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, render.HumanIdentity(value))
	}
	return out
}

func searchHeading(query, who string, returned, total int) string {
	query = strings.TrimSpace(query)
	who = strings.TrimSpace(who)
	shown := render.FormatInteger(int64(returned))
	matches := render.FormatInteger(int64(total))
	switch {
	case query != "" && who != "":
		return fmt.Sprintf("Search %q with %s: showing %s of %s, newest first.", query, who, shown, matches)
	case query != "":
		return fmt.Sprintf("Search %q: showing %s of %s, newest first.", query, shown, matches)
	case who != "":
		return fmt.Sprintf("Search with %s: showing %s of %s, newest first.", who, shown, matches)
	default:
		return fmt.Sprintf("Search filters: showing %s of %s, newest first.", shown, matches)
	}
}

func searchEmptyText(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "No matching items."
	}
	return fmt.Sprintf("No matches for %q.", query)
}

func resolvedWhoName(who *WhoResolved) string {
	if who == nil {
		return ""
	}
	return strings.Join(strings.Fields(who.Who), " ")
}

func writeWhoResolutionErrorText(w io.Writer, err error, body output.ErrorBody) bool {
	var whoErr whoAmbiguityError
	if !errors.As(err, &whoErr) {
		return false
	}
	_, _ = fmt.Fprintf(w, "Error: %s\n", body.Message)
	candidates := whoCandidateOutputs(whoErr.candidates)
	switch {
	case whoErr.code == 5 && len(candidates) == 0:
		if retry := retrySearchWithoutWho(whoErr.query); retry != "" {
			_, _ = fmt.Fprintf(w, "\nRetry without --who: %s\n", retry)
		} else if strings.TrimSpace(body.Remedy) != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n", body.Remedy)
		}
	case whoErr.code == 5:
		_, _ = fmt.Fprintln(w, "\nDid you mean:")
		_ = writeWhoText(w, whoOutput{Query: whoErr.who, Candidates: candidates})
		_, _ = fmt.Fprintf(w, "\nRetry with a suggestion: %s\n", retrySearchWithWho(whoErr.query, candidates[0]))
	case len(candidates) == 0:
		if strings.TrimSpace(body.Remedy) != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n", body.Remedy)
		}
	default:
		_, _ = fmt.Fprintln(w)
		_ = writeWhoText(w, whoOutput{Query: whoErr.who, Candidates: candidates})
		_, _ = fmt.Fprintf(w, "\nRetry with one listed identifier: %s\n", retrySearchWithWho(whoErr.query, candidates[0]))
	}
	return true
}

func retrySearchWithWho(query string, candidate whoCandidateOutput) string {
	parts := []string{"search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteRetryArg(query))
	}
	parts = append(parts, "--who", quoteRetryArg(candidateWhoFilter(candidate)))
	return strings.Join(parts, " ")
}

func retrySearchWithoutWho(query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	return "search " + quoteRetryArg(query)
}

func candidateWhoFilter(candidate whoCandidateOutput) string {
	for _, identifier := range candidate.Identifiers {
		if strings.TrimSpace(identifier) != "" {
			return identifier
		}
	}
	return candidate.Who
}

func quoteRetryArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return strconv.Quote(value)
	}
	return value
}
