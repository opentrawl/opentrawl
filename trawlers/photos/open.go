package photoscrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardformat"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	result, err := c.loadOpenAsset(ctx, req, ref)
	if err != nil {
		return err
	}
	if req.Log != nil {
		_ = req.Log.Info("open_written", "ref_kind=asset")
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "open", result)
	}
	return printOpenText(req.Out, result)
}

func (c *Crawler) loadOpenAsset(ctx context.Context, req *trawlkit.Request, ref string) (archive.OpenResult, error) {
	anchorID := ""
	if req != nil {
		anchorID = req.RequestedAnchorID
	}
	return c.loadOpenAssetForAnchor(ctx, req, ref, anchorID)
}

func (c *Crawler) loadOpenAssetForAnchor(ctx context.Context, req *trawlkit.Request, ref, anchorID string) (archive.OpenResult, error) {
	resolved, err := c.resolveInputRef(ctx, req, ref)
	if err != nil {
		return archive.OpenResult{}, err
	}
	result, err := archive.OpenWithStoreFocused(ctx, req.Store, resolved, anchorID)
	if err != nil {
		return archive.OpenResult{}, archiveReadCommandError(err)
	}
	return result, nil
}

func archiveReadCommandError(err error) error {
	var incompatible archive.ArchiveIncompatibleError
	if errors.As(err, &incompatible) {
		return commandError{
			Code:    "archive_incompatible",
			Message: "The Photos archive needs to be updated.",
			Remedy:  "run trawl photos sync, then retry",
		}
	}
	return err
}

func (c *Crawler) resolveInputRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photos asset ref",
			Remedy:  "use a ref in the form photos:asset/ID or a short ref from search",
		}
	}
	fullRefs, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset", Remedy: "rerun search or use the full ref"}
	}
	if err != nil {
		return "", err
	}
	if len(fullRefs) != 1 {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	return fullRefs[0], nil
}

func printOpenText(w io.Writer, result archive.OpenResult) error {
	if result.Stale != nil {
		if _, err := fmt.Fprintf(w, "%s\n\n", result.Stale.Banner); err != nil {
			return err
		}
	}
	title := strings.TrimSpace(result.Model.Summary)
	if title == "" {
		title = openFallbackTitle(result)
	}
	fields := openMechanicalFields(result.Mechanical)
	if location := result.Model.Location; location != nil {
		value := strings.TrimSpace(location.Name)
		if value == "" {
			value = "No useful location"
		}
		value += " · " + location.Kind + " · " + location.Confidence + "\n" + location.Reason
		fields = append(fields, render.CardField{Label: "Model location", Value: value})
	}
	if text := strings.TrimSpace(result.Model.VisibleText); text != "" {
		fields = append(fields, render.CardField{Label: "Visible text", Value: text})
	}
	if len(result.Model.Uncertainties) > 0 {
		fields = append(fields, render.CardField{Label: "Uncertainty", Value: strings.Join(result.Model.Uncertainties, "; ") + "."})
	}
	if ref := strings.TrimSpace(result.Ref); ref != "" {
		fields = append(fields, render.CardField{Label: "Ref", Value: ref})
	}
	return render.WriteCard(w, render.Card{
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

func openMechanicalFields(mechanical archive.OpenMechanical) []render.CardField {
	fields := []render.CardField{}
	if mechanical.Source.State == "deleted_upstream" {
		value := "Deleted upstream"
		if mechanical.Source.FirstMissingAt != "" {
			value += " since " + openTextTime(mechanical.Source.FirstMissingAt)
		}
		fields = append(fields, render.CardField{Label: "Source", Value: value})
	}
	if captured := mechanical.Captured; captured != nil {
		value := openTextTime(captured.Local)
		if captured.Timezone != "" {
			value += " local (" + captured.Timezone + ")"
		}
		fields = append(fields, render.CardField{Label: "Captured", Value: value})
	}
	if media := mechanical.Media; media != nil {
		parts := nonEmptyText(media.Kind)
		if media.Width > 0 && media.Height > 0 {
			parts = append(parts, fmt.Sprintf("%s x %s", render.FormatInteger(int64(media.Width)), render.FormatInteger(int64(media.Height))))
		}
		if media.DurationSeconds > 0 {
			parts = append(parts, fmt.Sprintf("%.1fs", media.DurationSeconds))
		}
		if len(parts) > 0 {
			fields = append(fields, render.CardField{Label: "Media", Value: strings.Join(parts, ", ")})
		}
	}
	placeNameRendered := false
	placeAddressRendered := false
	if place := mechanical.Place; place != nil {
		if line := archive.OpenPlaceCardLine(place); line != "" {
			fields = append(fields, render.CardField{Label: "Place", Value: line})
			placeNameRendered = strings.TrimSpace(place.Name) != ""
			placeAddressRendered = strings.TrimSpace(place.Name) != "" && strings.EqualFold(strings.TrimSpace(place.Name), strings.TrimSpace(mechanical.Address))
		}
	}
	if gps := mechanical.GPS; gps != nil {
		value := cardformat.FormatCoordinate(gps.Latitude) + ", " + cardformat.FormatCoordinate(gps.Longitude)
		if gps.HorizontalAccuracyMeters > 0 {
			value += ", +/-" + cardformat.FormatMeters(gps.HorizontalAccuracyMeters) + "m"
		}
		fields = append(fields, render.CardField{Label: "GPS", Value: value})
	}
	if mechanical.Address != "" && !placeAddressRendered {
		fields = append(fields, render.CardField{Label: "Address", Value: mechanical.Address})
	}
	if knownPlace := mechanical.KnownPlace; knownPlace != nil && !placeNameRendered {
		if line := archive.KnownPlaceCardLine(knownPlace.Kind, knownPlace.Name, knownPlace.After); line != "" {
			fields = append(fields, render.CardField{Label: "Place", Value: line})
		}
	} else if venue := mechanical.Venue; venue != nil && !placeNameRendered {
		value := venue.Name
		if venue.Tier == "venue_candidate" {
			value += ", candidate"
		}
		if venue.DistanceMeters > 0 {
			value += ", " + cardformat.FormatMeters(venue.DistanceMeters) + "m from GPS"
		}
		fields = append(fields, render.CardField{Label: "Venue", Value: value})
	}
	if camera := mechanical.Camera; camera != nil && camera.Display != "" {
		fields = append(fields, render.CardField{Label: "Camera", Value: camera.Display})
	}
	if len(mechanical.Albums) > 0 {
		titles := []string{}
		for i, album := range mechanical.Albums {
			if i == 3 {
				titles = append(titles, fmt.Sprintf("and %s more", render.FormatInteger(int64(len(mechanical.Albums)-3))))
				break
			}
			titles = append(titles, album.Title)
		}
		fields = append(fields, render.CardField{Label: "Albums", Value: strings.Join(titles, ", ")})
	}
	if original := mechanical.Original; original != nil {
		fields = append(fields, render.CardField{Label: "Original", Value: fmt.Sprintf("%s, %s, %s", original.Filename, original.Availability, humanBytes(original.Bytes))})
	}
	if len(mechanical.Flags) > 0 {
		fields = append(fields, render.CardField{Label: "Flags", Value: strings.Join(mechanical.Flags, ", ")})
	}
	return fields
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
		return render.FormatInteger(bytes) + " B"
	default:
		return "unknown size"
	}
}
