package photoscrawl

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/cardformat"
)

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	resolved, err := resolveInputRef(ctx, archivePaths(req), ref)
	if err != nil {
		return err
	}
	result, err := archive.Open(ctx, archivePaths(req), resolved)
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

func resolveInputRef(ctx context.Context, paths archive.Paths, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !archive.ValidShortRef(ref) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photoscrawl asset ref",
			Remedy:  "use a ref in the form photoscrawl:asset/ID or a short ref from search",
		}
	}
	resolved, err := archive.ResolveShortRef(ctx, paths, ref)
	if err != nil {
		return "", err
	}
	switch len(resolved.FullRefs) {
	case 0:
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	case 1:
		return resolved.FullRefs[0], nil
	default:
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset", Remedy: "rerun search or use the full ref"}
	}
}

func printOpenText(w io.Writer, result archive.OpenResult) error {
	title := strings.TrimSpace(result.Model.Summary)
	if title == "" {
		title = openFallbackTitle(result)
	}
	fields := openMechanicalFields(result.Mechanical)
	if len(result.Model.Uncertainties) > 0 {
		fields = append(fields, render.CardField{Label: "Uncertainty", Value: strings.Join(result.Model.Uncertainties, "; ") + "."})
	}
	if ref := strings.TrimSpace(result.ShortRef); ref != "" {
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
			parts = append(parts, fmt.Sprintf("%d x %d", media.Width, media.Height))
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
				titles = append(titles, fmt.Sprintf("and %d more", len(mechanical.Albums)-3))
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
		return fmt.Sprintf("%d B", bytes)
	default:
		return "unknown size"
	}
}
