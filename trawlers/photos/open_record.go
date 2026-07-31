package photos

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	photosopen "github.com/opentrawl/opentrawl/trawlers/photos/proto/trawl/photos/open"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ trawlkit.RecordOpener = (*Crawler)(nil)

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*open.OpenRecord, error) {
	value, err := c.loadOpenAsset(ctx, req, localShortReference)
	if err != nil {
		return nil, err
	}
	if captured := value.Mechanical.Captured; captured != nil {
		if err := presentation.ValidateTimestamps(captured.Local); err != nil {
			return nil, err
		}
	}
	openedPhotoRecord := projectOpenRecord(value)
	record := &open.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedPhotoRecord.GetCanonicalPhotoRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
				DetailPresentation: projectOpenDetailPresentation(value),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

func projectOpenRecord(value archive.OpenResult) *photosopen.OpenedPhotoRecord {
	return &photosopen.OpenedPhotoRecord{
		CanonicalPhotoRecordReference: trawlkit.NewCanonicalArchiveRecordReference(value.Ref),
		OutdatedDerivedDetails:        projectOutdatedDerivedDetails(value.Stale),
		PhotoSourceFacts:              projectMechanical(value.Mechanical),
		ModelDerivedDetails:           projectModel(value.Model),
	}
}

func projectOutdatedDerivedDetails(value *archive.OpenStale) *photosopen.OpenedPhotoOutdatedDerivedDetails {
	if value == nil {
		return nil
	}
	reason := strings.TrimSpace(value.Reason)
	if reason == "asset metadata changed in update (fingerprint drift)" || reason == "source details changed after this card was created" {
		reason = "Source details changed after this card was created"
	}
	return &photosopen.OpenedPhotoOutdatedDerivedDetails{
		DerivedDetailsBecameOutdatedTime:       openedPhotoTimestamp(value.Since),
		ReasonDerivedDetailsAreOutdated:        reason,
		OutdatedDerivedDetailsHumanDescription: "Outdated since " + sourceRecordDate(value.Since) + " · " + reason,
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

func projectMechanical(value archive.OpenMechanical) *photosopen.OpenedPhotoSourceFacts {
	record := &photosopen.OpenedPhotoSourceFacts{
		PhotoSourceAvailability:                 projectSource(value.Source),
		PhotoCaptureTime:                        projectCaptured(value.Captured),
		PhotoMediaDetails:                       projectMedia(value.Media),
		PhotoPlace:                              projectPlace(value.Place),
		PhotoGlobalPositioningSystemCoordinates: projectGPS(value.GPS),
		MatchedKnownPlace:                       projectKnownPlace(value.KnownPlace),
		MatchedVenue:                            projectVenue(value.Venue),
		VenueCandidatesInNearestFirstOrder:      projectVenueCandidates(value.VenueCandidates),
		PhotoCameraDetails:                      projectCamera(value.Camera),
		PhotoAlbumMemberships:                   projectAlbums(value.Albums),
		OriginalPhotoAssetDetails:               projectOriginal(value.Original),
		PhotoSourceFactFlags:                    append([]string(nil), value.Flags...),
	}
	setOptionalString(&record.PhotoPostalAddress, value.Address)
	return record
}

func projectSource(value archive.OpenSource) *photosopen.OpenedPhotoSourceAvailability {
	return &photosopen.OpenedPhotoSourceAvailability{
		PhotoSourceAvailabilityState: openedPhotoSourceAvailabilityState(value.State),
		PhotoSourceFirstMissingTime:  openedPhotoTimestamp(value.FirstMissingAt),
		PhotoSourceDeletedTime:       openedPhotoTimestamp(value.SourceDeletedAt),
	}
}

func openedPhotoSourceAvailabilityState(value string) photosopen.OpenedPhotoSourceAvailabilityState {
	switch strings.TrimSpace(value) {
	case "current":
		return photosopen.OpenedPhotoSourceAvailabilityState_OPENED_PHOTO_SOURCE_AVAILABILITY_STATE_CURRENT
	case "deleted_upstream":
		return photosopen.OpenedPhotoSourceAvailabilityState_OPENED_PHOTO_SOURCE_AVAILABILITY_STATE_DELETED_UPSTREAM
	default:
		return photosopen.OpenedPhotoSourceAvailabilityState_OPENED_PHOTO_SOURCE_AVAILABILITY_STATE_UNSPECIFIED
	}
}

func projectCaptured(value *archive.OpenCaptured) *photosopen.OpenedPhotoCaptureTime {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoCaptureTime{PhotoCaptureTime: openedPhotoTimestamp(value.Local)}
	setOptionalString(&record.PhotoCaptureTimeZoneIdentifier, value.Timezone)
	return record
}

func projectMedia(value *archive.OpenMedia) *photosopen.OpenedPhotoMediaDetails {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoMediaDetails{}
	setOptionalString(&record.PhotoMediaKind, value.Kind)
	if value.Width != 0 {
		record.PhotoPixelWidth = recordInt64(value.Width)
	}
	if value.Height != 0 {
		record.PhotoPixelHeight = recordInt64(value.Height)
	}
	if value.DurationSeconds != 0 {
		record.VideoDurationSeconds = recordFloat64(value.DurationSeconds)
	}
	return record
}

func projectPlace(value *archive.OpenPlace) *photosopen.OpenedPhotoPlace {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoPlace{PhotoPlaceLatitudeDegrees: value.Latitude, PhotoPlaceLongitudeDegrees: value.Longitude}
	setOptionalString(&record.PhotoPlaceDisplayName, value.Name)
	return record
}

func projectGPS(value *archive.OpenGPS) *photosopen.OpenedPhotoGlobalPositioningSystemCoordinates {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoGlobalPositioningSystemCoordinates{LatitudeDegrees: value.Latitude, LongitudeDegrees: value.Longitude}
	if value.HorizontalAccuracyMeters != 0 {
		record.HorizontalAccuracyMetres = recordFloat64(value.HorizontalAccuracyMeters)
	}
	return record
}

func projectKnownPlace(value *archive.OpenKnownPlace) *photosopen.OpenedPhotoMatchedKnownPlace {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoMatchedKnownPlace{KnownPlaceKind: value.Kind, KnownPlaceDisplayName: value.Name}
	if value.After {
		record.PhotoWasCapturedAfterKnownPlaceVisit = recordBool(true)
	}
	return record
}

func projectVenue(value *archive.OpenVenue) *photosopen.OpenedPhotoMatchedVenue {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoMatchedVenue{VenueDisplayName: value.Name, VenueMatchTier: value.Tier}
	setOptionalString(&record.VenueCategory, value.Category)
	if value.DistanceMeters != 0 {
		record.DistanceFromPhotoCoordinatesMetres = recordFloat64(value.DistanceMeters)
	}
	return record
}

func projectVenueCandidates(values []archive.OpenVenueCandidate) []*photosopen.OpenedPhotoVenueCandidate {
	records := make([]*photosopen.OpenedPhotoVenueCandidate, 0, len(values))
	for _, value := range values {
		record := &photosopen.OpenedPhotoVenueCandidate{VenueDisplayName: value.Name}
		setOptionalString(&record.VenueCategory, value.Category)
		setOptionalString(&record.VenueMatchTier, value.Tier)
		if value.DistanceMeters != 0 {
			record.DistanceFromPhotoCoordinatesMetres = recordFloat64(value.DistanceMeters)
		}
		records = append(records, record)
	}
	return records
}

func projectCamera(value *archive.OpenCamera) *photosopen.OpenedPhotoCameraDetails {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoCameraDetails{}
	setOptionalString(&record.CameraDisplayName, value.Display)
	setOptionalString(&record.CameraManufacturerName, value.Make)
	setOptionalString(&record.CameraModelName, value.Model)
	setOptionalString(&record.CameraLensModelName, value.LensModel)
	if value.FocalLengthMM != 0 {
		record.CameraFocalLengthMillimetres = recordFloat64(value.FocalLengthMM)
	}
	if value.FocalLength35MM != 0 {
		record.Camera_35MillimetreEquivalentFocalLength = recordFloat64(value.FocalLength35MM)
	}
	if value.Aperture != 0 {
		record.CameraApertureFNumber = recordFloat64(value.Aperture)
	}
	setOptionalString(&record.CameraShutterSpeedDisplayText, value.ShutterSpeed)
	if value.ISO != 0 {
		record.CameraIsoSensitivity = recordInt64(value.ISO)
	}
	return record
}

func projectAlbums(values []archive.OpenAlbum) []*photosopen.OpenedPhotoAlbumMembership {
	records := make([]*photosopen.OpenedPhotoAlbumMembership, 0, len(values))
	for _, value := range values {
		records = append(records, &photosopen.OpenedPhotoAlbumMembership{PhotoAlbumDisplayName: value.Title})
	}
	return records
}

func projectOriginal(value *archive.OpenOriginal) *photosopen.OpenedPhotoOriginalAssetDetails {
	if value == nil {
		return nil
	}
	record := &photosopen.OpenedPhotoOriginalAssetDetails{}
	setOptionalString(&record.OriginalPhotoAssetFilename, value.Filename)
	if value.Bytes != 0 {
		record.OriginalPhotoAssetByteCount = recordInt64(value.Bytes)
	}
	setOptionalString(&record.OriginalPhotoAssetAvailability, value.Availability)
	return record
}

func projectModel(value archive.OpenModel) *photosopen.OpenedPhotoModelDerivedDetails {
	record := &photosopen.OpenedPhotoModelDerivedDetails{ModelDerivedUncertainties: append([]string(nil), value.Uncertainties...)}
	setOptionalString(&record.ModelPromptIdentifier, value.PromptVersion)
	setOptionalString(&record.ModelIdentifier, value.ModelID)
	setOptionalString(&record.ModelDerivedPhotoSummary, value.Summary)
	setOptionalString(&record.ModelDerivedPhotoDescription, value.Description)
	setOptionalString(&record.OpticalCharacterRecognitionText, value.OCRText)
	setOptionalString(&record.ModelDerivedVisibleText, value.VisibleText)
	if value.Location != nil {
		record.ModelDerivedLocation = &photosopen.OpenedPhotoModelDerivedLocation{ModelDerivedLocationKind: value.Location.Kind, ModelDerivedLocationConfidence: value.Location.Confidence, ModelDerivedLocationReason: value.Location.Reason}
		setOptionalString(&record.ModelDerivedLocation.ModelDerivedLocationDisplayName, value.Location.Name)
	}
	return record
}

func setOptionalString(target **string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*target = &value
	}
}

func openedPhotoTimestamp(value string) *timestamppb.Timestamp {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsedTime, err := time.Parse(layout, strings.TrimSpace(value))
		if err == nil {
			return timestamppb.New(parsedTime)
		}
	}
	return nil
}

func recordInt64(value int64) *int64       { return &value }
func recordFloat64(value float64) *float64 { return &value }
func recordBool(value bool) *bool          { return &value }

func projectOpenDetailPresentation(value archive.OpenResult) *presentationcontract.TrawlerSpecificCommandDetailPresentation {
	record := projectOpenRecord(value)
	fields := make([]*presentationcontract.TrawlerSpecificCommandDetailPresentationField, 0, 16)
	mechanical := record.PhotoSourceFacts
	if mechanical != nil {
		if captured := mechanical.PhotoCaptureTime; captured != nil {
			if capturedAt := captured.GetPhotoCaptureTime(); capturedAt != nil {
				fields = append(fields, photosDetailExactTimeField("Captured local time", capturedAt.AsTime()))
			}
		}
		appendPhotosDetailTextField(&fields, "Media", formatPresentationMedia(mechanical.PhotoMediaDetails), "media")
		appendPhotosDetailTextField(&fields, "Place", formatPresentationPlace(mechanical.PhotoPlace), "place")
		appendPhotosDetailTextField(&fields, "GPS", formatPresentationGPS(mechanical.PhotoGlobalPositioningSystemCoordinates), "")
		appendPhotosDetailTextField(&fields, "Address", mechanical.GetPhotoPostalAddress(), "address")
		appendPhotosDetailTextField(&fields, "Known place", formatPresentationKnownPlace(mechanical.MatchedKnownPlace), "known-place")
		appendPhotosDetailTextField(&fields, "Venue", formatPresentationVenue(mechanical.MatchedVenue), "venue")
		appendPhotosDetailTextField(&fields, "Camera", formatPresentationCamera(mechanical.PhotoCameraDetails), "")
		albumTitles := make([]string, 0, len(mechanical.PhotoAlbumMemberships))
		for _, album := range mechanical.PhotoAlbumMemberships {
			if album != nil && strings.TrimSpace(album.PhotoAlbumDisplayName) != "" {
				albumTitles = append(albumTitles, strings.TrimSpace(album.PhotoAlbumDisplayName))
			}
		}
		appendPhotosDetailTextField(&fields, "Albums", strings.Join(albumTitles, ", "), "album")
		for _, signal := range value.Mechanical.Signals {
			appendPhotosDetailTextField(&fields, "Photo signal", signal.Label, signal.AnchorID)
		}
		if filenames := presentationFilenames(mechanical.OriginalPhotoAssetDetails, value.Mechanical.Filenames); len(filenames) > 0 {
			label := "Original filename"
			if len(filenames) > 1 {
				label = "Filenames"
			}
			appendPhotosDetailTextField(&fields, label, strings.Join(filenames, "\n"), "filename")
		}
		if original := mechanical.OriginalPhotoAssetDetails; original != nil {
			if original.OriginalPhotoAssetByteCount != nil {
				appendPhotosDetailTextField(&fields, "Original size", presentation.Bytes(*original.OriginalPhotoAssetByteCount), "")
			}
			appendPhotosDetailTextField(&fields, "Availability", original.GetOriginalPhotoAssetAvailability(), "")
		}
	}
	if location := record.ModelDerivedDetails.GetModelDerivedLocation(); location != nil {
		name := strings.TrimSpace(location.GetModelDerivedLocationDisplayName())
		if name == "" {
			name = "No useful location"
		}
		appendPhotosDetailTextField(&fields, "Model location", name+" · "+location.GetModelDerivedLocationKind()+" · "+location.GetModelDerivedLocationConfidence()+"\n"+location.GetModelDerivedLocationReason(), "model-location")
	}
	appendPhotosDetailTextField(&fields, "Derived details", record.OutdatedDerivedDetails.GetOutdatedDerivedDetailsHumanDescription(), "")
	for _, uncertainty := range record.ModelDerivedDetails.ModelDerivedUncertainties {
		appendPhotosDetailTextField(&fields, "Uncertainty", uncertainty, "")
	}
	if value.Mechanical.SignalsTruncated {
		appendPhotosDetailTextField(&fields, "Photo signals", "Some photo signals are omitted.", "")
	}

	bodyCandidates := []struct {
		fieldDisplayName      string
		text                  string
		fixedAnchorIdentifier string
	}{
		{fieldDisplayName: "Summary", text: record.ModelDerivedDetails.GetModelDerivedPhotoSummary(), fixedAnchorIdentifier: "asset-details"},
		{fieldDisplayName: "Description", text: record.ModelDerivedDetails.GetModelDerivedPhotoDescription(), fixedAnchorIdentifier: "description"},
		{fieldDisplayName: "Visible text", text: record.ModelDerivedDetails.GetModelDerivedVisibleText(), fixedAnchorIdentifier: "visible-text"},
		{fieldDisplayName: "OCR", text: record.ModelDerivedDetails.GetOpticalCharacterRecognitionText(), fixedAnchorIdentifier: "ocr"},
	}
	titleAnchorIdentifier := "asset-details"
	detail := &presentationcontract.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       "Photo",
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier(titleAnchorIdentifier),
		FieldsInDisplayOrder:    fields,
	}
	bodySelected := false
	for _, candidate := range bodyCandidates {
		candidate.text = strings.TrimSpace(candidate.text)
		if candidate.text == "" {
			continue
		}
		if !bodySelected {
			detail.Body = &presentationcontract.TrawlerSpecificCommandDetailPresentation_BodyText{
				BodyText: candidate.text,
			}
			if candidate.fixedAnchorIdentifier != titleAnchorIdentifier {
				detail.BodyAnchor = trawlkit.NewRecordAnchorIdentifier(candidate.fixedAnchorIdentifier)
			}
			bodySelected = true
			continue
		}
		appendPhotosDetailTextField(
			&detail.FieldsInDisplayOrder,
			candidate.fieldDisplayName,
			candidate.text,
			candidate.fixedAnchorIdentifier,
		)
	}
	return detail
}

func appendPhotosDetailTextField(
	fields *[]*presentationcontract.TrawlerSpecificCommandDetailPresentationField,
	fieldDisplayName string,
	textValue string,
	fixedAnchorIdentifier string,
) {
	if textValue = strings.TrimSpace(textValue); textValue != "" {
		field := photosDetailTextField(fieldDisplayName, textValue)
		if fixedAnchorIdentifier != "" {
			field.FieldAnchor = trawlkit.NewRecordAnchorIdentifier(fixedAnchorIdentifier)
		}
		*fields = append(*fields, field)
	}
}

func presentationFilenames(original *photosopen.OpenedPhotoOriginalAssetDetails, values []string) []string {
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
		appendFilename(original.GetOriginalPhotoAssetFilename())
	}
	for _, value := range values {
		appendFilename(value)
	}
	return result
}

func formatPresentationMedia(value *photosopen.OpenedPhotoMediaDetails) string {
	if value == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if kind := strings.TrimSpace(value.GetPhotoMediaKind()); kind != "" {
		parts = append(parts, kind)
	}
	if value.PhotoPixelWidth != nil && value.PhotoPixelHeight != nil {
		parts = append(parts, fmt.Sprintf("%d x %d", *value.PhotoPixelWidth, *value.PhotoPixelHeight))
	}
	if value.VideoDurationSeconds != nil {
		parts = append(parts, formatPresentationFloat(*value.VideoDurationSeconds)+"s")
	}
	return strings.Join(parts, ", ")
}

func formatPresentationPlace(value *photosopen.OpenedPhotoPlace) string {
	if value == nil {
		return ""
	}
	if name := strings.TrimSpace(value.GetPhotoPlaceDisplayName()); name != "" {
		return name
	}
	if value.PhotoPlaceLatitudeDegrees != nil && value.PhotoPlaceLongitudeDegrees != nil {
		return formatPresentationFloat(*value.PhotoPlaceLatitudeDegrees) + ", " + formatPresentationFloat(*value.PhotoPlaceLongitudeDegrees)
	}
	return ""
}

func formatPresentationGPS(value *photosopen.OpenedPhotoGlobalPositioningSystemCoordinates) string {
	if value == nil {
		return ""
	}
	text := formatPresentationFloat(value.LatitudeDegrees) + ", " + formatPresentationFloat(value.LongitudeDegrees)
	if value.HorizontalAccuracyMetres != nil {
		text += " (accuracy: " + formatPresentationFloat(*value.HorizontalAccuracyMetres) + " m)"
	}
	return text
}

func formatPresentationKnownPlace(value *photosopen.OpenedPhotoMatchedKnownPlace) string {
	if value == nil {
		return ""
	}
	name := strings.TrimSpace(value.KnownPlaceDisplayName)
	kind := strings.TrimSpace(value.KnownPlaceKind)
	if name == "" || kind == "" {
		return ""
	}
	text := name + " (" + kind + ")"
	if value.GetPhotoWasCapturedAfterKnownPlaceVisit() {
		text += ", after capture"
	}
	return text
}

func formatPresentationVenue(value *photosopen.OpenedPhotoMatchedVenue) string {
	if value == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, part := range []string{value.VenueDisplayName, value.GetVenueCategory(), value.VenueMatchTier} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if value.DistanceFromPhotoCoordinatesMetres != nil {
		parts = append(parts, formatPresentationFloat(*value.DistanceFromPhotoCoordinatesMetres)+" m away")
	}
	return strings.Join(parts, ", ")
}

func formatPresentationCamera(value *photosopen.OpenedPhotoCameraDetails) string {
	if value == nil {
		return ""
	}
	if display := strings.TrimSpace(value.GetCameraDisplayName()); display != "" {
		return display
	}
	parts := make([]string, 0, 8)
	for _, part := range []string{value.GetCameraManufacturerName(), value.GetCameraModelName(), value.GetCameraLensModelName()} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	if value.CameraFocalLengthMillimetres != nil {
		parts = append(parts, formatPresentationFloat(*value.CameraFocalLengthMillimetres)+" mm")
	}
	if value.Camera_35MillimetreEquivalentFocalLength != nil {
		parts = append(parts, formatPresentationFloat(*value.Camera_35MillimetreEquivalentFocalLength)+" mm equivalent")
	}
	if value.CameraApertureFNumber != nil {
		parts = append(parts, "f/"+formatPresentationFloat(*value.CameraApertureFNumber))
	}
	if shutter := strings.TrimSpace(value.GetCameraShutterSpeedDisplayText()); shutter != "" {
		parts = append(parts, shutter)
	}
	if value.CameraIsoSensitivity != nil {
		parts = append(parts, fmt.Sprintf("ISO %d", *value.CameraIsoSensitivity))
	}
	return strings.Join(parts, ", ")
}

func formatPresentationFloat(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
