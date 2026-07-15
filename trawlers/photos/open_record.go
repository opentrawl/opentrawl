package photoscrawl

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	photosopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/photos/open/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(ctx context.Context, req *trawlkit.Request, ref string) (*openv1.OpenRecord, error) {
	value, err := c.loadOpenAsset(ctx, req, ref)
	if err != nil {
		return nil, err
	}
	if captured := value.Mechanical.Captured; captured != nil {
		if err := presentation.ValidateTimestamps(captured.Local); err != nil {
			return nil, err
		}
	}
	machine := projectOpenRecord(value)
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	resource, err := c.presentationResource(ctx, req, machine.GetRef())
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{SourceId: c.Info().ID, OpenRef: machine.GetRef(), Data: data, Presentation: projectOpenPresentationWithResource(value, resource)}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

const sourceRecordSchemaVersion = 6

func projectOpenRecord(value archive.OpenResult) *photosopenv1.PhotosRecord {
	return &photosopenv1.PhotosRecord{
		SchemaVersion: sourceRecordSchemaVersion,
		Ref:           value.Ref,
		Stale:         projectStale(value.Stale),
		Mechanical:    projectMechanical(value.Mechanical),
		Model:         projectModel(value.Model),
	}
}

func projectStale(value *archive.OpenStale) *photosopenv1.Stale {
	if value == nil {
		return nil
	}
	reason := strings.TrimSpace(value.Reason)
	if reason == "asset metadata changed in sync (fingerprint drift)" {
		reason = "source details changed after this card was created"
	}
	return &photosopenv1.Stale{
		Since:  value.Since,
		Reason: reason,
		Banner: "Card status: Stale · " + reason + " · since " + sourceRecordDate(value.Since),
	}
}

func sourceRecordDate(value string) string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		parsed, err := time.Parse(layout, strings.TrimSpace(value))
		if err == nil {
			return parsed.Format("2 January 2006")
		}
	}
	return strings.TrimSpace(value)
}

func projectMechanical(value archive.OpenMechanical) *photosopenv1.Mechanical {
	record := &photosopenv1.Mechanical{
		Source:          projectSource(value.Source),
		Captured:        projectCaptured(value.Captured),
		Media:           projectMedia(value.Media),
		Place:           projectPlace(value.Place),
		Gps:             projectGPS(value.GPS),
		KnownPlace:      projectKnownPlace(value.KnownPlace),
		Venue:           projectVenue(value.Venue),
		VenueCandidates: projectVenueCandidates(value.VenueCandidates),
		Camera:          projectCamera(value.Camera),
		Albums:          projectAlbums(value.Albums),
		Original:        projectOriginal(value.Original),
		Flags:           append([]string(nil), value.Flags...),
	}
	setOptionalString(&record.Address, value.Address)
	return record
}

func projectSource(value archive.OpenSource) *photosopenv1.Source {
	record := &photosopenv1.Source{State: value.State}
	setOptionalString(&record.FirstMissingAt, value.FirstMissingAt)
	setOptionalString(&record.SourceDeletedAt, value.SourceDeletedAt)
	return record
}

func projectCaptured(value *archive.OpenCaptured) *photosopenv1.Captured {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Captured{Local: value.Local}
	setOptionalString(&record.Timezone, value.Timezone)
	return record
}

func projectMedia(value *archive.OpenMedia) *photosopenv1.Media {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Media{}
	setOptionalString(&record.Kind, value.Kind)
	if value.Width != 0 {
		record.Width = recordInt64(value.Width)
	}
	if value.Height != 0 {
		record.Height = recordInt64(value.Height)
	}
	if value.DurationSeconds != 0 {
		record.DurationSeconds = recordFloat64(value.DurationSeconds)
	}
	return record
}

func projectPlace(value *archive.OpenPlace) *photosopenv1.Place {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Place{Latitude: value.Latitude, Longitude: value.Longitude}
	setOptionalString(&record.Name, value.Name)
	return record
}

func projectGPS(value *archive.OpenGPS) *photosopenv1.GPS {
	if value == nil {
		return nil
	}
	record := &photosopenv1.GPS{Latitude: value.Latitude, Longitude: value.Longitude}
	if value.HorizontalAccuracyMeters != 0 {
		record.HorizontalAccuracyMeters = recordFloat64(value.HorizontalAccuracyMeters)
	}
	return record
}

func projectKnownPlace(value *archive.OpenKnownPlace) *photosopenv1.KnownPlace {
	if value == nil {
		return nil
	}
	record := &photosopenv1.KnownPlace{Kind: value.Kind, Name: value.Name}
	if value.After {
		record.After = recordBool(true)
	}
	return record
}

func projectVenue(value *archive.OpenVenue) *photosopenv1.Venue {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Venue{Name: value.Name, Tier: value.Tier}
	setOptionalString(&record.Category, value.Category)
	if value.DistanceMeters != 0 {
		record.DistanceMeters = recordFloat64(value.DistanceMeters)
	}
	return record
}

func projectVenueCandidates(values []archive.OpenVenueCandidate) []*photosopenv1.VenueCandidate {
	records := make([]*photosopenv1.VenueCandidate, 0, len(values))
	for _, value := range values {
		record := &photosopenv1.VenueCandidate{Name: value.Name}
		setOptionalString(&record.Category, value.Category)
		setOptionalString(&record.Tier, value.Tier)
		if value.DistanceMeters != 0 {
			record.DistanceMeters = recordFloat64(value.DistanceMeters)
		}
		records = append(records, record)
	}
	return records
}

func projectCamera(value *archive.OpenCamera) *photosopenv1.Camera {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Camera{}
	setOptionalString(&record.Display, value.Display)
	setOptionalString(&record.Make, value.Make)
	setOptionalString(&record.Model, value.Model)
	setOptionalString(&record.LensModel, value.LensModel)
	if value.FocalLengthMM != 0 {
		record.FocalLengthMm = recordFloat64(value.FocalLengthMM)
	}
	if value.FocalLength35MM != 0 {
		record.FocalLength_35Mm = recordFloat64(value.FocalLength35MM)
	}
	if value.Aperture != 0 {
		record.Aperture = recordFloat64(value.Aperture)
	}
	setOptionalString(&record.ShutterSpeed, value.ShutterSpeed)
	if value.ISO != 0 {
		record.Iso = recordInt64(value.ISO)
	}
	return record
}

func projectAlbums(values []archive.OpenAlbum) []*photosopenv1.Album {
	records := make([]*photosopenv1.Album, 0, len(values))
	for _, value := range values {
		records = append(records, &photosopenv1.Album{Title: value.Title})
	}
	return records
}

func projectOriginal(value *archive.OpenOriginal) *photosopenv1.Original {
	if value == nil {
		return nil
	}
	record := &photosopenv1.Original{}
	setOptionalString(&record.Filename, value.Filename)
	if value.Bytes != 0 {
		record.Bytes = recordInt64(value.Bytes)
	}
	setOptionalString(&record.Availability, value.Availability)
	return record
}

func projectModel(value archive.OpenModel) *photosopenv1.Model {
	record := &photosopenv1.Model{Uncertainties: append([]string(nil), value.Uncertainties...)}
	setOptionalString(&record.PromptVersion, value.PromptVersion)
	setOptionalString(&record.ModelId, value.ModelID)
	setOptionalString(&record.Summary, value.Summary)
	setOptionalString(&record.Description, value.Description)
	setOptionalString(&record.OcrText, value.OCRText)
	setOptionalString(&record.VisibleText, value.VisibleText)
	if value.Location != nil {
		record.Location = &photosopenv1.ModelLocation{Kind: value.Location.Kind, Confidence: value.Location.Confidence, Reason: value.Location.Reason}
		setOptionalString(&record.Location.Name, value.Location.Name)
	}
	return record
}

func setOptionalString(target **string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*target = &value
	}
}

func recordInt64(value int64) *int64       { return &value }
func recordFloat64(value float64) *float64 { return &value }
func recordBool(value bool) *bool          { return &value }

func projectOpenPresentation(value archive.OpenResult) *presentationv1.PresentationDocument {
	return projectOpenPresentationWithResource(value, nil)
}

func projectOpenPresentationWithResource(value archive.OpenResult, resource *presentationv1.Resource) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value)
	title := "Photo"
	fields := make([]*presentationv1.Field, 0, 12)
	mechanical := record.Mechanical
	if mechanical != nil {
		if captured := mechanical.Captured; captured != nil {
			capturedAt := presentation.MustTimestamp(captured.Local)
			appendPresentationField(&fields, "Captured local time", capturedAt)
		}
		appendPresentationFieldWithAnchor(&fields, "Media", formatPresentationMedia(mechanical.Media), "media")
		appendPresentationFieldWithAnchor(&fields, "Place", formatPresentationPlace(mechanical.Place), "place")
		appendPresentationField(&fields, "GPS", formatPresentationGPS(mechanical.Gps))
		appendPresentationFieldWithAnchor(&fields, "Address", mechanical.GetAddress(), "address")
		appendPresentationFieldWithAnchor(&fields, "Known place", formatPresentationKnownPlace(mechanical.KnownPlace), "known-place")
		appendPresentationFieldWithAnchor(&fields, "Venue", formatPresentationVenue(mechanical.Venue), "venue")
		appendPresentationField(&fields, "Camera", formatPresentationCamera(mechanical.Camera))
		albumTitles := make([]string, 0, len(mechanical.Albums))
		for _, album := range mechanical.Albums {
			if album != nil && strings.TrimSpace(album.Title) != "" {
				albumTitles = append(albumTitles, strings.TrimSpace(album.Title))
			}
		}
		appendPresentationFieldWithAnchor(&fields, "Albums", strings.Join(albumTitles, ", "), "album")
		for _, signal := range value.Mechanical.Signals {
			appendPresentationFieldWithAnchor(&fields, "Photo signal", signal.Label, signal.AnchorID)
		}
		if filenames := presentationFilenames(mechanical.Original, value.Mechanical.Filenames); len(filenames) > 0 {
			label := "Original filename"
			if len(filenames) > 1 {
				label = "Filenames"
			}
			fields = append(fields, &presentationv1.Field{Label: label, Display: strings.Join(filenames, "\n"), AnchorId: "filename"})
		}
		if original := mechanical.Original; original != nil {
			if original.Bytes != nil {
				fields = append(fields, &presentationv1.Field{Label: "Original size", Display: presentation.Bytes(*original.Bytes)})
			}
			appendPresentationField(&fields, "Availability", original.GetAvailability())
		}
	}
	blocks := make([]*presentationv1.Block, 0, 4)
	hasAssetDetails := false
	if resource != nil {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "asset-details", Content: &presentationv1.Block_Resource{Resource: resource}})
		hasAssetDetails = true
	}
	if summary := strings.TrimSpace(record.Model.GetSummary()); summary != "" {
		block := &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: summary}}}
		if !hasAssetDetails {
			block.AnchorId = "asset-details"
			hasAssetDetails = true
		}
		blocks = append(blocks, block)
	}
	if description := strings.TrimSpace(record.Model.GetDescription()); description != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "description", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: description}}})
	}
	if visibleText := strings.TrimSpace(record.Model.GetVisibleText()); visibleText != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "visible-text", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: visibleText}}})
	}
	if ocr := strings.TrimSpace(record.Model.GetOcrText()); ocr != "" {
		blocks = append(blocks, &presentationv1.Block{AnchorId: "ocr", Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: ocr}}})
	}
	if location := record.Model.GetLocation(); location != nil {
		name := strings.TrimSpace(location.GetName())
		if name == "" {
			name = "No useful location"
		}
		appendPresentationFieldWithAnchor(&fields, "Model location", name+" · "+location.GetKind()+" · "+location.GetConfidence()+"\n"+location.GetReason(), "model-location")
	}
	if len(fields) > 0 {
		block := &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}
		if !hasAssetDetails {
			block.AnchorId = "asset-details"
		}
		blocks = append(blocks, block)
	}
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks, PrimaryAnchorId: "asset-details"}
	if banner := strings.TrimSpace(record.Stale.GetBanner()); banner != "" {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_WARNING, Message: banner})
	}
	for _, uncertainty := range record.Model.Uncertainties {
		if uncertainty = strings.TrimSpace(uncertainty); uncertainty != "" {
			document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_WARNING, Message: uncertainty})
		}
	}
	if value.Mechanical.SignalsTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Photo signals are truncated."})
	}
	return document
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}

func appendPresentationFieldWithAnchor(fields *[]*presentationv1.Field, label, value, anchorID string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value, AnchorId: anchorID})
	}
}

func presentationFilenames(original *photosopenv1.Original, values []string) []string {
	result := make([]string, 0, len(values)+1)
	seen := make(map[string]struct{}, len(values)+1)
	appendFilename := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if original != nil {
		appendFilename(original.GetFilename())
	}
	for _, value := range values {
		appendFilename(value)
	}
	return result
}

func formatPresentationMedia(value *photosopenv1.Media) string {
	if value == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if kind := strings.TrimSpace(value.GetKind()); kind != "" {
		parts = append(parts, kind)
	}
	if value.Width != nil && value.Height != nil {
		parts = append(parts, fmt.Sprintf("%d x %d", *value.Width, *value.Height))
	}
	if value.DurationSeconds != nil {
		parts = append(parts, formatPresentationFloat(*value.DurationSeconds)+"s")
	}
	return strings.Join(parts, ", ")
}

func formatPresentationPlace(value *photosopenv1.Place) string {
	if value == nil {
		return ""
	}
	if name := strings.TrimSpace(value.GetName()); name != "" {
		return name
	}
	if value.Latitude != nil && value.Longitude != nil {
		return formatPresentationFloat(*value.Latitude) + ", " + formatPresentationFloat(*value.Longitude)
	}
	return ""
}

func formatPresentationGPS(value *photosopenv1.GPS) string {
	if value == nil {
		return ""
	}
	text := formatPresentationFloat(value.Latitude) + ", " + formatPresentationFloat(value.Longitude)
	if value.HorizontalAccuracyMeters != nil {
		text += " (accuracy: " + formatPresentationFloat(*value.HorizontalAccuracyMeters) + " m)"
	}
	return text
}

func formatPresentationKnownPlace(value *photosopenv1.KnownPlace) string {
	if value == nil {
		return ""
	}
	name := strings.TrimSpace(value.Name)
	kind := strings.TrimSpace(value.Kind)
	if name == "" || kind == "" {
		return ""
	}
	text := name + " (" + kind + ")"
	if value.GetAfter() {
		text += ", after capture"
	}
	return text
}

func formatPresentationVenue(value *photosopenv1.Venue) string {
	if value == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, part := range []string{value.Name, value.GetCategory(), value.Tier} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if value.DistanceMeters != nil {
		parts = append(parts, formatPresentationFloat(*value.DistanceMeters)+" m away")
	}
	return strings.Join(parts, ", ")
}

func formatPresentationCamera(value *photosopenv1.Camera) string {
	if value == nil {
		return ""
	}
	if display := strings.TrimSpace(value.GetDisplay()); display != "" {
		return display
	}
	parts := make([]string, 0, 8)
	for _, part := range []string{value.GetMake(), value.GetModel(), value.GetLensModel()} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if value.FocalLengthMm != nil {
		parts = append(parts, formatPresentationFloat(*value.FocalLengthMm)+" mm")
	}
	if value.FocalLength_35Mm != nil {
		parts = append(parts, formatPresentationFloat(*value.FocalLength_35Mm)+" mm equivalent")
	}
	if value.Aperture != nil {
		parts = append(parts, "f/"+formatPresentationFloat(*value.Aperture))
	}
	if shutter := strings.TrimSpace(value.GetShutterSpeed()); shutter != "" {
		parts = append(parts, shutter)
	}
	if value.Iso != nil {
		parts = append(parts, fmt.Sprintf("ISO %d", *value.Iso))
	}
	return strings.Join(parts, ", ")
}

func formatPresentationFloat(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
