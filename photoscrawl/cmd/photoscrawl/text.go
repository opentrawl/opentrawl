package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/output"
	ckrender "github.com/openclaw/crawlkit/render"
	"github.com/openclaw/photoscrawl/internal/archive"
)

func writeMetadata(w io.Writer, format output.Format, manifest archive.Manifest) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "metadata", manifest)
	}
	return printMetadataText(w, manifest)
}

func printMetadataText(w io.Writer, manifest archive.Manifest) error {
	if _, err := fmt.Fprintf(w, "%s (%s)\n", manifest.DisplayName, manifest.ID); err != nil {
		return err
	}
	if manifest.Description != "" {
		if _, err := fmt.Fprintf(w, "%s\n", manifest.Description); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nVersion: %s\nContract version: %d\n", manifest.Version, manifest.ContractVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Capabilities: %s\n", strings.Join(manifest.Capabilities, ", ")); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\nCommands:\n"); err != nil {
		return err
	}
	names := make([]string, 0, len(manifest.Commands))
	for name := range manifest.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		command := manifest.Commands[name]
		if _, err := fmt.Fprintf(w, "  %s: %s\n", name, strings.Join(command.Argv, " ")); err != nil {
			return err
		}
	}
	return nil
}

func writeSync(w io.Writer, format output.Format, result archive.SyncResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "sync", result)
	}
	return printSyncText(w, result)
}

func printSyncText(w io.Writer, result archive.SyncResult) error {
	if _, err := io.WriteString(w, "Sync complete\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Provider: %s\n", emptyDash(result.Provider)); err != nil {
		return err
	}
	if result.Database != "" {
		if _, err := fmt.Fprintf(w, "Archive: %s\n", result.Database); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nAssets: %d seen, %d new, %d changed, %d unchanged, %d missing\n", result.AssetsSeen, result.AssetsNew, result.AssetsChanged, result.AssetsUnchanged, result.PreviouslySeenMissing); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Evidence: %d resources, %d album memberships, %d locations\n", result.ResourcesSeen, result.AlbumMembershipsSeen, result.LocationsSeen); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Classification queue: %d queued, %d need download\n", result.QueuedForClassify, result.QueuedNeedsDownload); err != nil {
		return err
	}
	return nil
}

func writeStatus(w io.Writer, format output.Format, status archive.StatusResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "status", status)
	}
	return ckrender.WriteStatus(w, renderStatus(status))
}

func writeSearch(w io.Writer, format output.Format, result archive.SearchResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "search", result)
	}
	return printSearchText(w, result)
}

func printSearchText(w io.Writer, result archive.SearchResult) error {
	if _, err := fmt.Fprintf(w, "Search: %q\n", result.Query); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Showing %d of %d matches", len(result.Results), result.TotalMatches); err != nil {
		return err
	}
	if result.Truncated {
		if _, err := io.WriteString(w, " (truncated; narrow the query or time range)"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	for _, hit := range result.Results {
		line := strings.TrimSpace(strings.Join(nonEmptyText(hit.Time, hit.Who, hit.Where, hit.Ref), " | "))
		if line == "" {
			line = hit.Ref
		}
		if _, err := fmt.Fprintf(w, "\n%s\n", line); err != nil {
			return err
		}
		if hit.Snippet != "" {
			if _, err := fmt.Fprintf(w, "  %s\n", hit.Snippet); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeOpen(w io.Writer, format output.Format, result archive.OpenResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "open", result)
	}
	return printOpenText(w, result)
}

func printOpenText(w io.Writer, result archive.OpenResult) error {
	if _, err := fmt.Fprintf(w, "%s\n", emptyDash(result.Ref)); err != nil {
		return err
	}
	header := strings.Join(nonEmptyText(openTextTime(result.Time), result.MediaType, result.Where), " · ")
	if header != "" {
		if _, err := fmt.Fprintf(w, "%s\n", header); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	bodyParts := nonEmptyText(result.Summary, result.Description)
	for i, part := range bodyParts {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "%s\n", part); err != nil {
			return err
		}
	}
	if len(result.Uncertainties) > 0 {
		if len(bodyParts) > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Uncertain: %s.\n", strings.Join(result.Uncertainties, "; ")); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if result.Original != nil {
		if _, err := fmt.Fprintf(w, "Original: %s (%s, %s)\n", result.Original.Filename, humanBytes(result.Original.Bytes), result.Original.Availability); err != nil {
			return err
		}
	} else if _, err := io.WriteString(w, "Original: unavailable\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Evidence: %d records — photoscrawl evidence %s\n", result.Evidence.Count, result.Evidence.Ref); err != nil {
		return err
	}
	return nil
}

func writeNeighbors(w io.Writer, format output.Format, result archive.NeighborResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "neighbors", result)
	}
	return printNeighborsText(w, result)
}

func printNeighborsText(w io.Writer, result archive.NeighborResult) error {
	if _, err := fmt.Fprintf(w, "Neighbors of %s\n", result.Ref); err != nil {
		return err
	}
	if len(result.Neighbors) == 0 {
		_, err := io.WriteString(w, "No neighbors found\n")
		return err
	}
	if _, err := fmt.Fprintf(w, "Showing %d (limit %d)\n", len(result.Neighbors), result.Limit); err != nil {
		return err
	}
	for _, hit := range result.Neighbors {
		reasons := make([]string, 0, len(hit.Reasons))
		for _, reason := range hit.Reasons {
			reasons = append(reasons, reason.Type)
		}
		line := strings.TrimSpace(strings.Join(nonEmptyText(hit.Time, hit.MediaType, strings.Join(reasons, ", "), hit.Ref), " | "))
		if _, err := fmt.Fprintf(w, "\n%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

func writeEvidence(w io.Writer, format output.Format, result archive.EvidenceResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "evidence", result)
	}
	return printEvidenceText(w, result)
}

func printEvidenceText(w io.Writer, result archive.EvidenceResult) error {
	if _, err := fmt.Fprintf(w, "Evidence: %s\n", emptyDash(result.Ref)); err != nil {
		return err
	}
	if len(result.Evidence) == 0 {
		_, err := io.WriteString(w, "No evidence refs found\n")
		return err
	}
	for _, ref := range result.Evidence {
		if _, err := fmt.Fprintf(w, "  %s", ref.Ref); err != nil {
			return err
		}
		if ref.Kind != "" {
			if _, err := fmt.Fprintf(w, " | %s", ref.Kind); err != nil {
				return err
			}
		}
		if ref.Source != "" {
			if _, err := fmt.Fprintf(w, " | %s", ref.Source); err != nil {
				return err
			}
		}
		if ref.Summary != "" {
			if _, err := fmt.Fprintf(w, " | %s", ref.Summary); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func writeDoctor(w io.Writer, format output.Format, result archive.DoctorResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "doctor", result)
	}
	return ckrender.WriteDoctor(w, renderDoctorChecks(result.Checks), ckrender.LogTail{})
}

func renderStatus(status archive.StatusResult) ckrender.Status {
	return ckrender.Status{
		State:     ckrender.StatusState(status.State),
		Summary:   status.Summary,
		Sections:  renderStatusSections(status),
		Freshness: renderFreshness(status.Freshness),
	}
}

func renderStatusSections(status archive.StatusResult) []ckrender.Section {
	sections := []ckrender.Section{
		{Title: "Counts", Fields: renderCountFields(status)},
	}
	archiveFields := []ckrender.Field{
		{Label: "Database", Value: status.DatabasePath},
	}
	if status.DatabaseBytes > 0 {
		archiveFields = append(archiveFields, ckrender.Field{Label: "Size", Value: fmt.Sprintf("%d bytes", status.DatabaseBytes)})
	}
	if status.LastImportAt != "" {
		archiveFields = append(archiveFields, ckrender.Field{Label: "Last import", Value: status.LastImportAt})
	}
	sections = append(sections, ckrender.Section{Title: "Archive", Fields: archiveFields})
	return sections
}

func renderCountFields(status archive.StatusResult) []ckrender.Field {
	if len(status.Counts) == 0 {
		return []ckrender.Field{{Label: "Archived photos", Value: "none"}}
	}
	fields := make([]ckrender.Field, 0, len(status.Counts))
	for _, count := range status.Counts {
		fields = append(fields, ckrender.Field{Label: count.Label, Value: fmt.Sprint(count.Value)})
	}
	return fields
}

func renderFreshness(freshness *archive.StatusFreshness) *ckrender.Freshness {
	if freshness == nil || freshness.LastSync == "" {
		return nil
	}
	return &ckrender.Freshness{LastSync: freshness.LastSync}
}

func renderDoctorChecks(checks []archive.DoctorCheck) []ckrender.Check {
	rendered := make([]ckrender.Check, 0, len(checks))
	for _, check := range checks {
		rendered = append(rendered, ckrender.Check{
			Name:    check.ID,
			State:   ckrender.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return rendered
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func nonEmptyText(values ...string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mapString(row map[string]any, key string) string {
	if row == nil {
		return ""
	}
	switch value := row[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func displayTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return parsed.Local().Format(time.RFC3339)
}

func openTextTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return parsed.Local().Format("2006-01-02 15:04")
}

func humanBytes(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	case bytes > 0:
		return fmt.Sprintf("%d B", bytes)
	default:
		return "unknown size"
	}
}

func displayObservationKind(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "_", " ")
	if value == "" {
		return "observation"
	}
	return value
}
