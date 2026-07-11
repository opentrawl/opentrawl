package photoscrawl

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	photosopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/photos/open/v1"
)

const sourceRecordSchemaVersion = 5

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
