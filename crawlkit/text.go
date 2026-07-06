package crawlkit

import (
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
	Query string `json:"query"`
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
		{Label: "Commands", Value: strconv.Itoa(len(manifest.Commands))},
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
		Summary:  status.Summary,
		Warnings: append([]string(nil), status.Warnings...),
		Errors:   append([]string(nil), status.Errors...),
	}
	if len(status.Counts) > 0 {
		fields := make([]render.Field, 0, len(status.Counts))
		for _, count := range status.Counts {
			label := firstText(count.Label, count.ID)
			fields = append(fields, render.Field{Label: label, Value: strconv.FormatInt(count.Value, 10)})
		}
		out.Sections = append(out.Sections, render.Section{Title: "Counts", Fields: fields})
	}
	var archiveFields []render.Field
	if status.ConfigPath != "" {
		archiveFields = append(archiveFields, render.Field{Label: "Config", Value: status.ConfigPath})
	}
	if status.DatabasePath != "" {
		archiveFields = append(archiveFields, render.Field{Label: "Database", Value: status.DatabasePath})
	}
	if status.DatabaseBytes > 0 {
		archiveFields = append(archiveFields, render.Field{Label: "Database size", Value: strconv.FormatInt(status.DatabaseBytes, 10) + " bytes"})
	}
	if status.WALBytes > 0 {
		archiveFields = append(archiveFields, render.Field{Label: "WAL size", Value: strconv.FormatInt(status.WALBytes, 10) + " bytes"})
	}
	if len(archiveFields) > 0 {
		out.Sections = append(out.Sections, render.Section{Title: "Archive", Fields: archiveFields})
	}
	if status.LastSyncAt != "" {
		out.Freshness = &render.Freshness{LastSync: status.LastSyncAt}
	} else if status.LastImportAt != "" {
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
		{Label: "Added", Value: strconv.FormatInt(report.Added, 10)},
		{Label: "Updated", Value: strconv.FormatInt(report.Updated, 10)},
		{Label: "Removed", Value: strconv.FormatInt(report.Removed, 10)},
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
			Ref:      firstText(hit.ShortRef, hit.Ref),
			Text:     hit.Snippet,
		})
	}
	if shouldWriteSearchSummary(value.SearchResult) {
		if err := render.WriteSearchSummary(w, value.Query, len(value.Results), int64(max(value.TotalMatches, len(value.Results)))); err != nil {
			return err
		}
		if value.Truncated {
			if _, err := fmt.Fprintln(w, "Narrow results with --who, --after, or --before."); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return render.WriteList(w, render.List{Heading: "Search results:", Empty: "No results.", Items: items})
}

func shouldWriteSearchSummary(result SearchResult) bool {
	return result.Truncated || result.TotalMatches > len(result.Results)
}

func writeWhoText(w io.Writer, value whoOutput) error {
	rows := make([][]string, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		rows = append(rows, []string{
			candidate.Who,
			strings.Join(candidate.Identifiers, ", "),
			strconv.FormatInt(candidate.Messages, 10),
			render.ShortLocalTime(candidate.lastSeen),
		})
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No matches.")
		return err
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "Name", Width: 20},
		{Header: "Identifiers", Width: 28, Wrap: true},
		{Header: "Messages", AlignRight: true},
		{Header: "Last seen", Width: 16},
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
	rows := make([][]string, 0, len(value.Contacts))
	for _, contact := range value.Contacts {
		rows = append(rows, []string{contact.DisplayName, strings.Join(contact.PhoneNumbers, ", ")})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "Name", Width: 24},
		{Header: "Phone numbers", Width: 32, Wrap: true},
	}, rows)
}
