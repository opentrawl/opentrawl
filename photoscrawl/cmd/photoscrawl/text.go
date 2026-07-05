package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/output"
	ckrender "github.com/openclaw/crawlkit/render"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/cardformat"
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
	if _, err := fmt.Fprintf(w, "Imported: %d resources, %d album memberships, %d locations\n", result.ResourcesSeen, result.AlbumMembershipsSeen, result.LocationsSeen); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Classification queue: %d queued, %d need download\n", result.QueuedForClassify, result.QueuedNeedsDownload); err != nil {
		return err
	}
	return nil
}

type statusOutput struct {
	archive.StatusResult
	Log *logTailOutput `json:"log,omitempty"`
}

type doctorOutput struct {
	archive.DoctorResult
	Log *logTailOutput `json:"log,omitempty"`
}

func writeStatus(w io.Writer, format output.Format, status archive.StatusResult, tail *logTailOutput) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "status", statusOutput{StatusResult: status, Log: tail})
	}
	return ckrender.WriteStatus(w, renderStatus(status, tail))
}

func writeSearch(w io.Writer, format output.Format, result archive.SearchResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "search", result)
	}
	return printSearchText(w, result)
}

func printSearchText(w io.Writer, result archive.SearchResult) error {
	hints := []string{"Open: photoscrawl open REF"}
	if result.Truncated {
		hints = append(hints, searchMoreHint(result))
	}
	return ckrender.WriteList(w, ckrender.List{
		Heading:   searchHeading(w, result),
		Hints:     hints,
		Items:     searchListItems(result.Results),
		ClampText: 2,
		Empty:     searchEmptyText(w, result.Query),
	})
}

func searchHeading(w io.Writer, result archive.SearchResult) string {
	prefix := "Search \""
	suffix := fmt.Sprintf("\": showing %d of %d matches.", len(result.Results), result.TotalMatches)
	return prefix + fitToLine(w, result.Query, prefix, suffix) + suffix
}

func searchEmptyText(w io.Writer, query string) string {
	prefix := "No matches for \""
	suffix := "\"."
	return prefix + fitToLine(w, query, prefix, suffix) + suffix
}

// fitToLine truncates a user-supplied query so a heading never wraps.
func fitToLine(w io.Writer, value, prefix, suffix string) string {
	width := ckrender.OutputWidth(w) - ckrender.DisplayWidth(prefix) - ckrender.DisplayWidth(suffix)
	if width < 1 {
		width = 1
	}
	return ckrender.Truncate(strings.TrimSpace(value), width)
}

// searchMoreHint is only reached when the result truncated, so result.Limit is
// a positive cap; doubling it is a valid wider rerun.
func searchMoreHint(result archive.SearchResult) string {
	return fmt.Sprintf("More: photoscrawl search %s --limit %d", strconv.Quote(result.Query), result.Limit*2)
}

// searchListItems renders capture dates in each asset's own zone (DateOnly, no
// zone conversion): a photo taken abroad keeps its true calendar date instead
// of being shifted into this machine's timezone. The precise wall-clock time
// and zone live in the open card.
func searchListItems(hits []archive.SearchHit) []ckrender.ListItem {
	items := make([]ckrender.ListItem, 0, len(hits))
	for _, hit := range hits {
		items = append(items, ckrender.ListItem{
			Time:     parseCaptureTime(hit.Time),
			DateOnly: true,
			Who:      hit.Who,
			Where:    hit.Where,
			Ref:      searchDisplayRef(hit),
			Text:     hit.Snippet,
		})
	}
	return items
}

func parseCaptureTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func writeOpen(w io.Writer, format output.Format, result archive.OpenResult) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "open", result)
	}
	return printOpenText(w, result)
}

// printOpenText renders one asset as a card: the model summary titles it, the
// mechanical facts are labelled fields, the description is the body. The full
// 32-hex canonical ref is deliberately absent — it is machine slop in human
// output (rules §2.3) and stays in the JSON record.
func printOpenText(w io.Writer, result archive.OpenResult) error {
	title := strings.TrimSpace(result.Model.Summary)
	if title == "" {
		title = openFallbackTitle(result)
	}
	fields := openMechanicalFields(result.Mechanical)
	if len(result.Model.Uncertainties) > 0 {
		fields = append(fields, ckrender.CardField{Label: "Uncertainty", Value: strings.Join(result.Model.Uncertainties, "; ") + "."})
	}
	// The short alias, never the full 32-hex ref (rules §2.3): the machine ref
	// stays in JSON. A blank alias (index not yet built) just drops the field.
	if ref := strings.TrimSpace(result.ShortRef); ref != "" {
		fields = append(fields, ckrender.CardField{Label: "Ref", Value: ref})
	}
	return ckrender.WriteCard(w, ckrender.Card{
		Title:  title,
		Fields: fields,
		Body:   strings.TrimSpace(result.Model.Description),
		Hints:  []string{"JSON: add --json for the full record."},
	})
}

func openFallbackTitle(result archive.OpenResult) string {
	if original := result.Mechanical.Original; original != nil && strings.TrimSpace(original.Filename) != "" {
		return original.Filename
	}
	return "Photo"
}

func openMechanicalFields(mechanical archive.OpenMechanical) []ckrender.CardField {
	fields := []ckrender.CardField{}
	if captured := mechanical.Captured; captured != nil {
		value := openTextTime(captured.Local)
		if captured.Timezone != "" {
			value += " local (" + captured.Timezone + ")"
		}
		fields = append(fields, ckrender.CardField{Label: "Captured", Value: value})
	}
	if media := mechanical.Media; media != nil {
		parts := nonEmptyText(media.Kind)
		if media.Width > 0 && media.Height > 0 {
			parts = append(parts, fmt.Sprintf("%d x %d", media.Width, media.Height))
		}
		if media.DurationSeconds > 0 {
			parts = append(parts, fmt.Sprintf("%.1fs", media.DurationSeconds))
		}
		if len(parts) > 0 {
			fields = append(fields, ckrender.CardField{Label: "Media", Value: strings.Join(parts, ", ")})
		}
	}
	if gps := mechanical.GPS; gps != nil {
		value := cardformat.FormatCoordinate(gps.Latitude) + ", " + cardformat.FormatCoordinate(gps.Longitude)
		if gps.HorizontalAccuracyMeters > 0 {
			value += ", +/-" + cardformat.FormatMeters(gps.HorizontalAccuracyMeters) + "m"
		}
		fields = append(fields, ckrender.CardField{Label: "GPS", Value: value})
	}
	if mechanical.Address != "" {
		fields = append(fields, ckrender.CardField{Label: "Address", Value: mechanical.Address})
	}
	if knownPlace := mechanical.KnownPlace; knownPlace != nil {
		if line := archive.KnownPlaceCardLine(knownPlace.Kind, knownPlace.Name, knownPlace.After); line != "" {
			fields = append(fields, ckrender.CardField{Label: "Place", Value: line})
		}
	} else if venue := mechanical.Venue; venue != nil {
		value := venue.Name
		if venue.Tier == "venue_candidate" {
			value += ", candidate"
		}
		if venue.DistanceMeters > 0 {
			value += ", " + cardformat.FormatMeters(venue.DistanceMeters) + "m from GPS"
		}
		fields = append(fields, ckrender.CardField{Label: "Venue", Value: value})
	}
	if camera := mechanical.Camera; camera != nil && camera.Display != "" {
		fields = append(fields, ckrender.CardField{Label: "Camera", Value: camera.Display})
	}
	if len(mechanical.Albums) > 0 {
		titles := []string{}
		for i, album := range mechanical.Albums {
			if i == 3 {
				titles = append(titles, fmt.Sprintf("and %d more", len(mechanical.Albums)-3))
				break
			}
			titles = append(titles, album.Title)
		}
		fields = append(fields, ckrender.CardField{Label: "Albums", Value: strings.Join(titles, ", ")})
	}
	if original := mechanical.Original; original != nil {
		fields = append(fields, ckrender.CardField{Label: "Original", Value: fmt.Sprintf("%s, %s, %s", original.Filename, original.Availability, humanBytes(original.Bytes))})
	}
	if len(mechanical.Flags) > 0 {
		fields = append(fields, ckrender.CardField{Label: "Flags", Value: strings.Join(mechanical.Flags, ", ")})
	}
	return fields
}

func writeDoctor(w io.Writer, format output.Format, result archive.DoctorResult, tail *logTailOutput) error {
	if format != output.Text && format != "" {
		return output.Write(w, format, "doctor", doctorOutput{DoctorResult: result, Log: tail})
	}
	return ckrender.WriteDoctor(w, renderDoctorChecks(result.Checks), renderLogTail(tail))
}

func renderStatus(status archive.StatusResult, tail *logTailOutput) ckrender.Status {
	return ckrender.Status{
		State:     ckrender.StatusState(status.State),
		Summary:   status.Summary,
		Sections:  renderStatusSections(status),
		Freshness: renderFreshness(status.Freshness),
		Log:       renderLogTail(tail),
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

func searchDisplayRef(hit archive.SearchHit) string {
	if strings.TrimSpace(hit.ShortRef) != "" {
		return hit.ShortRef
	}
	return hit.Ref
}

// openTextTime formats a capture time that already carries the asset's own
// zone offset; converting to the machine's zone would fabricate a fact.
func openTextTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return value
	}
	return parsed.Format("2006-01-02 15:04")
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
