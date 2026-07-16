package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type StatusResult struct {
	Source Source
	Status *federationv1.SourceStatus
}

func renderStatusTable(w io.Writer, results []StatusResult, now time.Time) error {
	if len(results) == 0 {
		_, err := fmt.Fprintln(w, "No crawlers found.")
		return err
	}
	rows := make([][]string, 0, len(results))
	for _, result := range results {
		rows = append(rows, []string{
			sourceHumanName(result.Source),
			statusState(result.Status),
			freshnessText(result.Status, now),
			statusHeadline(result.Status),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "source"},
		{Header: "state"},
		{Header: "recently synced"},
		{Header: "headline"},
	}, rows)
}

func renderStatusDetail(w io.Writer, result StatusResult, now time.Time) error {
	status := result.Status
	if _, err := fmt.Fprintf(w, "source: %s\n", sourceHumanName(result.Source)); err != nil {
		return err
	}
	if id := strings.TrimSpace(result.Source.ID); id != "" && id != sourceHumanName(result.Source) {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "state: %s\n", statusState(status)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "recently synced: %s\n", freshnessText(status, now)); err != nil {
		return err
	}
	if summary := statusSummary(status); summary != "" {
		if _, err := fmt.Fprintf(w, "summary: %s\n", summary); err != nil {
			return err
		}
	}
	if err := renderDatabases(w, status); err != nil {
		return err
	}
	if err := renderCounts(w, statusCounts(status.GetCounts())); err != nil {
		return err
	}
	if err := renderLastSync(w, status); err != nil {
		return err
	}
	if err := renderStatusMessages(w, "warnings", status.GetWarnings()); err != nil {
		return err
	}
	if err := renderStatusMessages(w, "errors", status.GetErrors()); err != nil {
		return err
	}
	if err := renderSetupRequirements(w, status.GetSetupRequirements()); err != nil {
		return err
	}
	return nil
}

func renderDatabases(w io.Writer, status *federationv1.SourceStatus) error {
	if len(status.GetDatabases()) == 0 && status.GetDatabasePath() == "" {
		return nil
	}
	if _, err := fmt.Fprintln(w, "databases:"); err != nil {
		return err
	}
	if status.GetDatabasePath() != "" {
		if _, err := fmt.Fprintf(w, "  archive: %s\n", tildePath(status.GetDatabasePath())); err != nil {
			return err
		}
	}
	for _, database := range status.GetDatabases() {
		name := firstNonEmpty(database.GetLabel(), database.GetId(), database.GetRole(), "database")
		parts := nonEmpty(database.GetKind(), database.GetRole())
		if database.GetIsPrimary() {
			parts = append(parts, "primary")
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", humanLabel(name), strings.Join(normalisedStringList(parts), ", ")); err != nil {
			return err
		}
		location := firstNonEmpty(database.GetPath(), database.GetEndpoint(), database.GetArchive())
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

func renderLastSync(w io.Writer, status *federationv1.SourceStatus) error {
	lastSync := status.GetLastSyncRfc3339()
	if lastSync == "" && status.GetLastImportRfc3339() == "" {
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
	if status.GetLastImportRfc3339() != "" {
		if _, err := fmt.Fprintf(w, "  last import: %s\n", humanTime(status.GetLastImportRfc3339())); err != nil {
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

func statusHeadline(status *federationv1.SourceStatus) string {
	if statusFailed(status) {
		return statusSummary(status)
	}
	if len(status.GetCounts()) == 0 {
		return statusSummary(status)
	}
	counts := headlineCounts(statusCounts(status.GetCounts()))
	if len(counts) == 0 {
		return statusSummary(status)
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

func statusCounts(values []*federationv1.Count) []Count {
	counts := make([]Count, 0, len(values))
	for _, count := range values {
		counts = append(counts, Count{ID: count.GetId(), Label: count.GetLabel(), Value: countValue(count.GetValue())})
	}
	return counts
}

func renderStatusMessages(w io.Writer, label string, messages []string) error {
	if len(messages) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "%s:\n", label); err != nil {
		return err
	}
	for _, message := range messages {
		if _, err := fmt.Fprintf(w, "  - %s\n", strings.TrimSpace(message)); err != nil {
			return err
		}
	}
	return nil
}

func renderSetupRequirements(w io.Writer, requirements []*federationv1.SetupRequirement) error {
	visible := make([]*federationv1.SetupRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.GetState() == federationv1.SetupState_SETUP_STATE_READY {
			continue
		}
		visible = append(visible, requirement)
	}
	if len(visible) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "setup:"); err != nil {
		return err
	}
	for _, requirement := range visible {
		if explanation := strings.TrimSpace(requirement.GetExplanation()); explanation != "" {
			if _, err := fmt.Fprintf(w, "  - %s\n", explanation); err != nil {
				return err
			}
		}
		if next := setupNextStep(requirement); next != "" {
			if _, err := fmt.Fprintf(w, "    next: %s\n", next); err != nil {
				return err
			}
		}
	}
	return nil
}

func setupNextStep(requirement *federationv1.SetupRequirement) string {
	switch requirement.GetAction() {
	case federationv1.SetupActionKind_SETUP_ACTION_KIND_OPEN_FULL_DISK_ACCESS:
		return "OpenTrawl will open Full Disk Access."
	case federationv1.SetupActionKind_SETUP_ACTION_KIND_REQUEST_PHOTOS:
		return "OpenTrawl will request Photos access."
	case federationv1.SetupActionKind_SETUP_ACTION_KIND_CHOOSE_ARCHIVE:
		return "OpenTrawl will ask you to choose the archive."
	case federationv1.SetupActionKind_SETUP_ACTION_KIND_RUN_COMMAND:
		if len(requirement.GetCommand()) > 0 {
			return strings.Join(requirement.GetCommand(), " ")
		}
	}
	return ""
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
