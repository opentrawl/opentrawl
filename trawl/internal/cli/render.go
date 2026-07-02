package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

type StatusResult struct {
	Source Source         `json:"-"`
	Status StatusEnvelope `json:"status"`
}

type DoctorResult struct {
	Source string        `json:"source"`
	Checks []DoctorCheck `json:"checks"`
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
			firstNonEmpty(result.Source.DisplayName, "—"),
			result.Status.State,
			freshnessText(result.Status, now),
			statusHeadline(result.Status),
		})
	}
	return writeTable(w, []string{"SOURCE", "SURFACE", "STATE", "FRESH", "HEADLINE"}, rows, nil)
}

// writeTable sizes every column but the last to its widest cell; the
// last column absorbs what remains of the output width and truncates
// with an ellipsis rather than wrap. remedies, when present, holds one
// indented follow-up line per row (empty for none).
func writeTable(w io.Writer, header []string, rows [][]string, remedies []string) error {
	widths := columnWidths(header, rows)
	free := lastColumnBudget(widths)
	if err := writeTableRow(w, header, widths, free); err != nil {
		return err
	}
	for i, row := range rows {
		if err := writeTableRow(w, row, widths, free); err != nil {
			return err
		}
		if remedies != nil && remedies[i] != "" {
			if _, err := fmt.Fprintf(w, "  remedy: %s\n", remedies[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// lastColumnBudget is the room left for the free-running last column
// after the fixed columns and their separators.
func lastColumnBudget(widths []int) int {
	used := 0
	for _, width := range widths {
		used += width + 2
	}
	free := outputWidth() - used
	if free < 24 {
		free = 24
	}
	return free
}

func writeTableRow(w io.Writer, row []string, widths []int, free int) error {
	var line strings.Builder
	for column, width := range widths {
		line.WriteString(padCell(row[column], width))
		line.WriteString("  ")
	}
	line.WriteString(truncateCell(row[len(row)-1], free))
	_, err := fmt.Fprintln(w, strings.TrimRight(line.String(), " "))
	return err
}

// padCell pads by display width, not bytes or runes, so emoji and wide
// characters keep the columns aligned.
func padCell(cell string, width int) string {
	gap := width - runewidth.StringWidth(cell)
	if gap <= 0 {
		return cell
	}
	return cell + strings.Repeat(" ", gap)
}

// truncateCell cuts a cell to a display width, marking the cut.
func truncateCell(cell string, width int) string {
	if runewidth.StringWidth(cell) <= width || width < 2 {
		return cell
	}
	return strings.TrimRight(runewidth.Truncate(cell, width-1, ""), " ") + "…"
}

// columnWidths sizes every column except the last, which runs free.
func columnWidths(header []string, rows [][]string) []int {
	widths := make([]int, len(header)-1)
	for column := range widths {
		widths[column] = runewidth.StringWidth(header[column])
		for _, row := range rows {
			if cells := runewidth.StringWidth(row[column]); cells > widths[column] {
				widths[column] = cells
			}
		}
	}
	return widths
}

func renderStatusDetail(w io.Writer, result StatusResult, now time.Time) error {
	status := result.Status
	if _, err := fmt.Fprintf(w, "source: %s\n", result.Source.ID); err != nil {
		return err
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
	return renderAuth(w, status.Auth)
}

// renderDoctor is a glance first: healthy sources collapse to one line,
// and only failing checks expand into message and remedy detail.
func renderDoctor(w io.Writer, results []DoctorResult) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No crawlers found.")
		return err
	}
	rows := make([][]string, 0, len(results))
	remedies := make([]string, 0, len(results))
	for _, result := range results {
		var failed []DoctorCheck
		names := make([]string, 0, len(result.Checks))
		for _, check := range result.Checks {
			names = append(names, check.ID)
			if checkFailed(check) {
				failed = append(failed, check)
			}
		}
		if len(failed) == 0 {
			plural := "checks"
			if len(result.Checks) == 1 {
				plural = "check"
			}
			rows = append(rows, []string{result.Source, "ok", fmt.Sprintf("%d %s: %s", len(result.Checks), plural, strings.Join(names, ", "))})
			remedies = append(remedies, "")
			continue
		}
		for _, check := range failed {
			rows = append(rows, []string{result.Source, "FAIL", check.ID + ": " + firstNonEmpty(check.Message, "check failed")})
			remedies = append(remedies, check.Remedy)
		}
	}
	return writeTable(w, []string{"SOURCE", "STATE", "CHECKS"}, rows, remedies)
}

func renderDatabases(w io.Writer, status StatusEnvelope) error {
	if len(status.Databases) == 0 && status.DatabasePath == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "databases:"); err != nil {
		return err
	}
	if status.DatabasePath != "" {
		if _, err := fmt.Fprintf(w, "  archive: %s\n", status.DatabasePath); err != nil {
			return err
		}
	}
	for _, database := range status.Databases {
		name := firstNonEmpty(database.Label, database.ID, database.Role, "database")
		parts := nonEmpty(database.Kind, database.Role)
		if database.IsPrimary {
			parts = append(parts, "primary")
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(parts, ", ")); err != nil {
			return err
		}
		location := firstNonEmpty(database.Path, database.Endpoint, database.Archive)
		if location != "" {
			if _, err := fmt.Fprintf(w, "    location: %s\n", location); err != nil {
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
		label := firstNonEmpty(count.Label, count.ID, "count")
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
	if lastSync == "" && status.LastSyncOutcome == nil && status.LastImportAt == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "last sync:"); err != nil {
		return err
	}
	if lastSync != "" {
		if _, err := fmt.Fprintf(w, "  at: %s\n", lastSync); err != nil {
			return err
		}
	}
	if status.LastImportAt != "" {
		if _, err := fmt.Fprintf(w, "  last import: %s\n", status.LastImportAt); err != nil {
			return err
		}
	}
	if status.LastSyncOutcome != nil {
		outcome := firstNonEmpty(status.LastSyncOutcome.State, status.LastSyncOutcome.Message)
		if outcome != "" {
			if _, err := fmt.Fprintf(w, "  outcome: %s\n", outcome); err != nil {
				return err
			}
		}
		if status.LastSyncOutcome.FinishedAt != "" {
			if _, err := fmt.Fprintf(w, "  finished: %s\n", status.LastSyncOutcome.FinishedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderAuth(w io.Writer, auth SafeAuth) error {
	if len(auth) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "auth:"); err != nil {
		return err
	}
	for _, key := range auth.boolKeys() {
		if _, err := fmt.Fprintf(w, "  %s: %t\n", key, auth[key]); err != nil {
			return err
		}
	}
	if value, ok := auth["expires"]; ok {
		expires := unknownFreshness
		if text, ok := value.(string); ok && text != "" {
			expires = text
		}
		if _, err := fmt.Fprintf(w, "  expires: %s\n", expires); err != nil {
			return err
		}
	}
	return nil
}

// A headline is a glance, not a report: the first few declared counts
// stand for the archive, the rest stay behind `trawl status <source>`.
const headlineCountLimit = 3

func statusHeadline(status StatusEnvelope) string {
	if len(status.Counts) == 0 {
		return status.Summary
	}
	counts := status.Counts
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

func formatCount(count Count) string {
	label := firstNonEmpty(count.Label, count.ID)
	value := count.Value.text(count.ID, label)
	if strings.EqualFold(label, "since") {
		return strings.TrimSpace(label + " " + value)
	}
	if label == "" {
		return value
	}
	return strings.TrimSpace(value + " " + label)
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
