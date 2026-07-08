package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/render"
)

type StatusResult struct {
	Source Source         `json:"-"`
	Status StatusEnvelope `json:"status"`
}

type DoctorResult struct {
	Source string        `json:"source"`
	Checks []DoctorCheck `json:"checks"`

	sourceInfo Source
}

func renderStatusTable(w io.Writer, results []StatusResult, now time.Time) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No crawlers found.")
		return err
	}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, []string{
			result.Source.ID,
			result.Source.DisplayName,
			result.Status.State,
			freshnessText(result.Status, now),
			statusHeadline(result.Status),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "source"},
		{Header: "surface"},
		{Header: "state"},
		{Header: "fresh"},
		{Header: "headline"},
	}, rows)
}

func renderStatusDetail(w io.Writer, result StatusResult, now time.Time) error {
	status := result.Status
	if _, err := fmt.Fprintf(w, "source: %s\n", result.Source.ID); err != nil {
		return err
	}
	if surface := strings.TrimSpace(result.Source.DisplayName); surface != "" {
		if _, err := fmt.Fprintf(w, "surface: %s\n", surface); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "state: %s\n", status.State); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "fresh: %s\n", freshnessText(status, now)); err != nil {
		return err
	}
	if status.Summary != "" {
		if _, err := fmt.Fprintf(w, "summary: %s\n", status.Summary); err != nil {
			return err
		}
	}
	if err := renderDatabases(w, status); err != nil {
		return err
	}
	if err := renderCounts(w, status.Counts); err != nil {
		return err
	}
	if err := renderLastSync(w, status); err != nil {
		return err
	}
	return nil
}

// renderDoctor is a glance first: one row per source, and each failing
// check expands into its own block below the table so remedies never
// ride a data line.
func renderDoctor(w io.Writer, results []DoctorResult) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No crawlers found.")
		return err
	}
	type failure struct {
		source string
		check  DoctorCheck
	}
	rows := make([][]string, 0, len(results))
	var failures []failure
	for _, result := range results {
		var failedNames []string
		names := make([]string, 0, len(result.Checks))
		for _, check := range result.Checks {
			names = append(names, humanLabel(check.ID))
			if checkFailed(check) {
				failedNames = append(failedNames, humanLabel(check.ID))
				failures = append(failures, failure{source: result.Source, check: check})
			}
		}
		if len(failedNames) == 0 {
			plural := "checks"
			if len(result.Checks) == 1 {
				plural = "check"
			}
			rows = append(rows, []string{result.Source, "ok", fmt.Sprintf("%d %s: %s", len(result.Checks), plural, strings.Join(names, ", "))})
			continue
		}
		summary := fmt.Sprintf("%s failed · %d of %d ok",
			strings.Join(failedNames, ", "), len(result.Checks)-len(failedNames), len(result.Checks))
		rows = append(rows, []string{result.Source, "FAIL", summary})
	}
	if err := render.WriteTable(w, []render.TableColumn{
		{Header: "source"},
		{Header: "state"},
		{Header: "checks"},
	}, rows); err != nil {
		return err
	}
	for _, failed := range failures {
		if _, err := fmt.Fprintf(w, "\n%s %s failed: %s\n", failed.source, humanLabel(failed.check.ID), firstNonEmpty(failed.check.Message, "check failed")); err != nil {
			return err
		}
		if remedy := strings.TrimSpace(failed.check.Remedy); remedy != "" {
			if _, err := fmt.Fprintf(w, "  Remedy: %s\n", remedy); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderDatabases(w io.Writer, status StatusEnvelope) error {
	if len(status.Databases) == 0 && status.DatabasePath == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "databases:"); err != nil {
		return err
	}
	if status.DatabasePath != "" {
		if _, err := fmt.Fprintf(w, "  archive: %s\n", tildePath(status.DatabasePath)); err != nil {
			return err
		}
	}
	for _, database := range status.Databases {
		name := firstNonEmpty(database.Label, database.ID, database.Role, "database")
		parts := nonEmpty(database.Kind, database.Role)
		if database.IsPrimary {
			parts = append(parts, "primary")
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", humanLabel(name), strings.Join(normalisedStringList(parts), ", ")); err != nil {
			return err
		}
		location := firstNonEmpty(database.Path, database.Endpoint, database.Archive)
		if location != "" {
			if _, err := fmt.Fprintf(w, "    location: %s\n", tildePath(location)); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderCounts(w io.Writer, counts []Count) error {
	if len(counts) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "counts:"); err != nil {
		return err
	}
	for _, count := range counts {
		label := humanLabel(firstNonEmpty(count.Label, count.ID, "count"))
		if _, err := fmt.Fprintf(w, "  %s: %s\n", label, count.Value.text(count.ID, label)); err != nil {
			return err
		}
	}
	return nil
}

func renderLastSync(w io.Writer, status StatusEnvelope) error {
	lastSync := ""
	if status.Freshness != nil {
		lastSync = status.Freshness.LastSync
	}
	lastSync = firstNonEmpty(lastSync, status.LastSyncAt)
	if lastSync == "" && status.LastImportAt == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "last sync:"); err != nil {
		return err
	}
	if lastSync != "" {
		if _, err := fmt.Fprintf(w, "  at: %s\n", humanTime(lastSync)); err != nil {
			return err
		}
	}
	if status.LastImportAt != "" {
		if _, err := fmt.Fprintf(w, "  last import: %s\n", humanTime(status.LastImportAt)); err != nil {
			return err
		}
	}
	return nil
}

// humanLabel turns a crawler-supplied snake_case key into the words a
// person would say: "full_disk_access" reads "full disk access".
func humanLabel(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "_", " ")), " ")
}

// humanTime renders a contract RFC3339 timestamp as short local time;
// anything unparseable stays visible as-is rather than vanishing.
func humanTime(value string) string {
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
		return render.ShortLocalTime(parsed)
	}
	return value
}

// A headline is a glance, not a report: the first few declared counts
// stand for the archive, the rest stay behind `trawl status <source>`.
const headlineCountLimit = 3

func statusHeadline(status StatusEnvelope) string {
	if len(status.Counts) == 0 {
		return status.Summary
	}
	counts := headlineCounts(status.Counts)
	if len(counts) == 0 {
		return status.Summary
	}
	truncated := false
	if len(counts) > headlineCountLimit {
		counts = counts[:headlineCountLimit]
		truncated = true
	}
	parts := make([]string, 0, len(counts)+1)
	for _, count := range counts {
		parts = append(parts, formatCount(count))
	}
	if truncated {
		parts = append(parts, "…")
	}
	return strings.Join(parts, " · ")
}

func headlineCounts(counts []Count) []Count {
	out := make([]Count, 0, len(counts))
	for _, count := range counts {
		if isZeroSinceOrYearCount(count) {
			continue
		}
		out = append(out, count)
	}
	return out
}

func isZeroSinceOrYearCount(count Count) bool {
	if !isSinceOrYearLabel(count.ID, count.Label) {
		return false
	}
	switch value := count.Value.value.(type) {
	case int:
		return value == 0
	case int64:
		return value == 0
	case float64:
		return value == 0
	default:
		return false
	}
}

func formatCount(count Count) string {
	label := firstNonEmpty(count.Label, count.ID)
	value := count.Value.text(count.ID, label)
	if isSinceOrYearLabel(count.ID, label) && strings.EqualFold(strings.TrimSpace(label), "since") {
		return strings.TrimSpace(label + " " + value)
	}
	if label == "" {
		return value
	}
	return strings.TrimSpace(value + " " + label)
}

func isSinceOrYearLabel(id, label string) bool {
	name := strings.ToLower(strings.TrimSpace(label))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(id))
	}
	return name == "since" || strings.Contains(name, "year")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, strings.TrimSpace(value))
		}
	}
	sort.Strings(out)
	return out
}
