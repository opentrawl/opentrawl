package render

import (
	"fmt"
	"io"
	"math"
	"path"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

const photoRecognisedTextFieldDisplayName = "Recognised text (Apple)"

func PhotoFactsSearchFieldDisplayName(field photo.PhotoSearchSourceField) string {
	switch field {
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CAPTION:
		return "Caption"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TITLE:
		return "Title"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_FULL_ADDRESS:
		return "Address (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ISO_COUNTRY_CODE:
		return "Country code (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTRY:
		return "Country (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STATE:
		return "Region (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTY:
		return "County (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TOWN:
		return "Town (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_NEIGHBOURHOOD:
		return "Neighbourhood (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STREET:
		return "Street (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_POINT_OF_INTEREST:
		return "Point of interest (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_WATER:
		return "Water (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ISLAND:
		return "Island (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PLACE_AREA:
		return "Place area (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PERSON:
		return "Person (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ALBUM:
		return "Album"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_MOMENT:
		return "Moment (Apple)"
	case photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_RECOGNISED_TEXT:
		return photoRecognisedTextFieldDisplayName
	default:
		return ""
	}
}

// PhotoOpenedRecordPresentation gives app clients the same field names,
// values and order as the shared opened-record renderer.
func PhotoOpenedRecordPresentation(record *photo.OpenedPhotoRecord) *open.TrawlerSpecificOpenedRecordPresentation {
	fields := openedRecordFieldsFromAppleLibraryPhotoFacts(record)
	if modelCard := record.GetPhotoCard(); modelCard != nil {
		modelCardPresentation := openedRecordCardFromPhotoModelOutput(modelCard)
		fields = append(append([]cardField{{label: "Summary", value: modelCardPresentation.title}}, modelCardPresentation.fields...), fields...)
	}
	fields = append(fields, cardField{label: "Full picture", value: photoPictureArchiveStateForHumanOutput(record)})
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       photoMediaKindDisplayName(record.GetAppleLibraryPhotoFacts()),
		DetailDisplayNameAnchor: &identity.RecordAnchorIdentifier{RecordAnchorIdentifier: openrecord.PhotoDetailAnchorID},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		detail.FieldsInDisplayOrder = append(detail.FieldsInDisplayOrder, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: field.label,
			FieldValue: &presentation.TrawlerSpecificCommandPresentationValue{
				TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: field.value},
			},
		})
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{DetailPresentation: detail}
}

func PhotosInspectNeedsPhoto() CommandFailure { return commandIsWrong("Inspect needs a photo link.") }
func PhotosInspectionLayerIsUnknown(layer string) CommandFailure {
	return commandIsWrong(quotedForSentence(layer) + " is not a Photos inspection layer.")
}

func formatPhotoCaptureLocalTime(capture *photo.PhotoCaptureLocalTime, form archiveEventTimeWrittenForm) string {
	if capture.GetCaptureTimeUtc() == nil {
		return ""
	}
	offset := capture.CaptureUtcOffsetSeconds
	if offset == nil {
		utcOffset := int32(0)
		offset = &utcOffset
	}
	eventTime, valid := archiveEventTimeFromTimestamp(capture.GetCaptureTimeUtc(), offset)
	if !valid {
		return ""
	}
	return formatArchiveEventTime(eventTime, form)
}

func photoCaptureTimeZoneSourceDescription(capture *photo.PhotoCaptureLocalTime) string {
	switch capture.GetCaptureTimeZoneSource() {
	case photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_TIME_ZONE_MAP_AT_CAMERA_LOCATION:
		return "time-zone map at camera position"
	case photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_APPLE_DEVICE_UTC_OFFSET:
		return "Apple's device offset"
	case photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_APPLE_DEVICE_TIME_ZONE_NAME:
		return "Apple's device time-zone name at capture time"
	default:
		return "capture zone not known; UTC shown"
	}
}

func photoCaptureTimeZoneDescription(capture *photo.PhotoCaptureLocalTime) string {
	if name := capture.GetCaptureTimeZoneName(); name != "" {
		return name + " · " + photoCaptureTimeZoneSourceDescription(capture)
	}
	return photoCaptureTimeZoneSourceDescription(capture)
}

func photoPictureArchiveStateForHumanOutput(record *photo.OpenedPhotoRecord) string {
	if fetchedPicture := record.GetPictureFetchResult(); fetchedPicture != nil {
		switch fetchedPicture.GetPictureFetchOutcome() {
		case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED:
			return "Fetched into the archive; its file location is not recorded."
		case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_NOT_AVAILABLE_IN_ICLOUD:
			return "The picture was not available from iCloud."
		case photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED:
			return "Fetching the picture failed: " + fetchedPicture.GetPictureFetchFailureMessage()
		}
	}
	if !record.GetPictureFetchStepIsBuilt() {
		return "Not fetched yet. Picture fetching is not built yet."
	}
	return "Not fetched yet."
}

func photoSharedLibraryContributorForHumanOutput(facts *photo.AppleLibraryPhotoFacts) string {
	if facts.GetAppleLibraryScope() != photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_ICLOUD_SHARED_LIBRARY {
		return ""
	}
	if facts.AppleSharedLibraryContributorIsOwner == nil {
		return "Not recorded by Apple"
	}
	if facts.GetAppleSharedLibraryContributorIsOwner() {
		return "Library owner"
	}
	return "Another contributor"
}

func photoNamedPeopleForHumanOutput(facts *photo.AppleLibraryPhotoFacts) string {
	var names []string
	for _, person := range facts.GetAppleNamedPeople() {
		if person.GetPersonFullName() != "" {
			names = append(names, person.GetPersonFullName())
		}
	}
	return strings.Join(names, ", ")
}
func photoAlbumNamesForHumanOutput(facts *photo.AppleLibraryPhotoFacts) string {
	var names []string
	for _, album := range facts.GetAppleAlbums() {
		if album.GetAlbumTitle() != "" {
			names = append(names, album.GetAlbumTitle())
		}
	}
	return strings.Join(names, ", ")
}
func photoMediaKindDisplayName(facts *photo.AppleLibraryPhotoFacts) string {
	if facts.GetAppleMediaKind() == 1 {
		return "Video"
	}
	if facts.GetIsAppleDetectedScreenshot() {
		return "Screenshot"
	}
	if facts.GetAppleMediaSubtype() == 2 {
		return "Live Photo"
	}
	return "Photo"
}
func photoListPrimaryHumanContent(facts *photo.AppleLibraryPhotoFacts) string {
	if facts.GetAppleCaption() != "" && !photoTextIsOnlyPictureFilename(facts.GetAppleCaption()) {
		return "Caption: " + facts.GetAppleCaption()
	}
	if facts.GetAppleTitle() != "" && !photoTextIsOnlyPictureFilename(facts.GetAppleTitle()) {
		return "Title: " + facts.GetAppleTitle()
	}
	if len(facts.GetAppleRecognisedTextLines()) > 0 {
		return photoRecognisedTextFieldDisplayName + ": " + strings.Join(facts.GetAppleRecognisedTextLines(), " · ")
	}
	if facts.GetAppleMomentTitle() != "" {
		return "Moment (Apple): " + facts.GetAppleMomentTitle()
	}
	return photoMediaKindDisplayName(facts)
}

func photoTextIsOnlyPictureFilename(text string) bool {
	filename := path.Base(strings.ReplaceAll(strings.TrimSpace(text), "\\", "/"))
	switch strings.ToLower(path.Ext(filename)) {
	case ".heic", ".heif", ".jpg", ".jpeg", ".png", ".gif", ".tif", ".tiff", ".dng", ".raw", ".mov", ".mp4", ".m4v", ".avi":
		return !strings.ContainsAny(filename, " \t\n\r")
	default:
		return false
	}
}

func writePhotoListResponse(writer io.Writer, response *photo.PhotoListResponse, links GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference) error {
	records := response.GetPhotoRecordsNewestFirst()
	if len(records) == 0 {
		return writeLine(writer, "No photos match.")
	}
	if err := writeLines(writer, []string{fmt.Sprintf("Photos: showing %s of %s.", FormatInteger(int64(len(records))), FormatInteger(int64(response.GetTotalMatchingPhotoCount()))), ""}); err != nil {
		return err
	}
	columns := []TableColumn{archiveEventTimeColumn(columnHeaderWhen), {Header: "photo", MinimumWidth: 6, Wrap: true, MaximumWrappedLines: 3}}
	var rows [][]string
	photosWithoutKnownCaptureZone := 0
	showPlace, showPeople, showCamera := false, false, false
	for _, record := range records {
		facts := record.GetAppleLibraryPhotoFacts()
		showPlace = showPlace || len(appleStoredPlaceHierarchyNamesSmallToLarge(facts.GetAppleStoredPlace())) > 0 || facts.GetCameraLocation() != nil
		showPeople = showPeople || (response.GetPersonFilterIsApplied() || OutputWidth(writer) >= 120) && photoNamedPeopleForHumanOutput(facts) != ""
		showCamera = showCamera || OutputWidth(writer) >= 180 && strings.TrimSpace(facts.GetCameraMake()+facts.GetCameraModel()) != ""
	}
	if showPlace {
		columns = append(columns, TableColumn{Header: columnHeaderWhere, MinimumWidth: 28, Wrap: true, MaximumWrappedLines: 2})
	}
	if showPeople {
		columns = append(columns, TableColumn{Header: columnHeaderWho, Wrap: true, MaximumWrappedLines: 3})
	}
	if showCamera {
		columns = append(columns, TableColumn{Header: "camera", Wrap: true, MaximumWrappedLines: 3})
	}
	columns = append(columns, TableColumn{Header: columnHeaderLink, NeverTruncateCellValues: true})
	for _, record := range records {
		facts := record.GetAppleLibraryPhotoFacts()
		if facts.GetCaptureLocalTime().CaptureUtcOffsetSeconds == nil {
			photosWithoutKnownCaptureZone++
		}
		content := photoListPrimaryHumanContent(facts)
		if facts.GetIsFavourite() {
			content += " · favourite"
		}
		mediaName := photoMediaKindDisplayName(facts)
		if mediaName != "Photo" && content != mediaName {
			content = mediaName + " · " + content
		}
		if facts.GetAppleAdjustmentsState() == 2 {
			content += " · edited"
		}
		row := []string{formatPhotoCaptureLocalTime(facts.GetCaptureLocalTime(), localTimeWithUTCOffsetForListRow), content}
		if showPlace {
			row = append(row, photoListPlaceForHumanOutput(facts.GetAppleStoredPlace(), facts.GetCameraLocation()))
		}
		if showPeople {
			row = append(row, photoNamedPeopleForHumanOutput(facts))
		}
		if showCamera {
			row = append(row, photoCameraNameForHumanOutput(facts))
		}
		row = append(row, globallyRoutableTrawlLinkText(links.globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(record.GetCanonicalRecordReference())))
		rows = append(rows, row)
	}
	primaryColumn := 1
	if showPlace {
		placeColumnWidth := tableRenderColumnsWithPrimaryHumanContentColumn(columns, rows, OutputWidth(writer), primaryColumn)[2].Width
		columns[2].Width = placeColumnWidth
		for index, record := range records {
			facts := record.GetAppleLibraryPhotoFacts()
			rows[index][2] = photoListPlaceWithinTwoLinesForHumanOutput(facts.GetAppleStoredPlace(), facts.GetCameraLocation(), placeColumnWidth)
		}
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, rows, primaryColumn); err != nil {
		return err
	}
	if photosWithoutKnownCaptureZone > 0 {
		return writeLines(writer, Wrap(FormatInteger(int64(photosWithoutKnownCaptureZone))+" displayed photos have no known capture zone; their times are shown in UTC.", OutputWidth(writer)))
	}
	return nil
}

func writeOpenedPhotoRecord(writer io.Writer, record *photo.OpenedPhotoRecord) error {
	if record == nil {
		return fmt.Errorf("opened photo record is missing")
	}
	// A card, when produced, is the first evidence an assistant reads.
	if card := record.GetPhotoCard(); card != nil {
		if err := writeCard(writer, openedRecordCardFromPhotoModelOutput(card)); err != nil {
			return err
		}
		if err := writeLine(writer, ""); err != nil {
			return err
		}
	}
	if err := writeCard(writer, card{title: photoMediaKindDisplayName(record.GetAppleLibraryPhotoFacts()), fields: openedRecordFieldsFromAppleLibraryPhotoFacts(record)}); err != nil {
		return err
	}
	if err := writeLines(writer, Wrap("Full picture: "+photoPictureArchiveStateForHumanOutput(record), OutputWidth(writer))); err != nil {
		return err
	}
	return writeNextCommands(writer, []NextCommand{nextCommandWithLabel(nextCommandLabelMoreDetail, "photos", "inspect", globallyRoutableTrawlLinkText(record.GetPhotoTrawlLink()))})
}

func openedRecordCardFromPhotoModelOutput(modelCard *photo.PhotoCard) card {
	fields := []cardField{{label: "Place shown", value: modelCard.GetPlaceShownName()}, {label: "Description", value: modelCard.GetDescription()}}
	for _, group := range modelCard.GetVisibleTextGroups() {
		fields = append(fields, cardField{label: group.GetWhereTextAppears(), value: group.GetTextAsWritten()})
	}
	return card{title: modelCard.GetOneLineSummary(), fields: fields}
}

func openedRecordFieldsFromAppleLibraryPhotoFacts(record *photo.OpenedPhotoRecord) []cardField {
	facts := record.GetAppleLibraryPhotoFacts()
	capture := facts.GetCaptureLocalTime()
	library := "Personal library"
	if facts.GetAppleLibraryScope() == photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_ICLOUD_SHARED_LIBRARY {
		library = "iCloud Shared Library"
	}
	camera := photoCameraNameForHumanOutput(facts)
	var settings []string
	if facts.FocalLengthMillimetres != nil {
		settings = append(settings, photoFocalLengthForHumanOutput(facts.GetFocalLengthMillimetres()))
	}
	if facts.FocalLength_35MmEquivalentMillimetres != nil {
		settings = append(settings, fmt.Sprintf("%d mm equivalent", facts.GetFocalLength_35MmEquivalentMillimetres()))
	}
	if facts.ApertureFNumber != nil {
		settings = append(settings, photoApertureForHumanOutput(facts.GetApertureFNumber()))
	}
	if facts.ExposureTimeSeconds != nil {
		settings = append(settings, photoExposureTimeForHumanOutput(facts.GetExposureTimeSeconds()))
	}
	if facts.IsoSpeed != nil {
		settings = append(settings, fmt.Sprintf("ISO %d", facts.GetIsoSpeed()))
	}
	favourite := "No"
	if facts.GetIsFavourite() {
		favourite = "Yes"
	}
	cameraPlace := photoPlaceForHumanOutput(facts.GetAppleStoredPlace(), nil)
	fields := []cardField{
		{label: "Captured", value: formatPhotoCaptureLocalTime(capture, localTimeWithUTCOffsetAndUTCForOpenedRecord)},
		{label: "Capture zone", value: photoCaptureTimeZoneDescription(capture)},
		{label: "Where camera stood (Apple)", value: cameraPlace},
	}
	if cameraLocation := facts.GetCameraLocation(); cameraLocation != nil {
		if cameraLocation.CameraHorizontalAccuracyMetres != nil {
			fields = append(fields, cardField{label: "GPS horizontal accuracy (Apple)", value: "about " + photoDistanceMetresForHumanOutput(cameraLocation.GetCameraHorizontalAccuracyMetres())})
		}
		fields = append(fields, cardField{label: "Camera GPS (standard coordinates)", value: fmt.Sprintf("Latitude %.6f, longitude %.6f", cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude())})
		if cameraLocation.CameraAltitudeMetres != nil {
			fields = append(fields, cardField{label: "Camera altitude (Apple)", value: photoAltitudeForHumanOutput(cameraLocation.GetCameraAltitudeMetres())})
		}
	}
	fields = append(fields, []cardField{
		{label: "Water (Apple)", value: facts.GetAppleStoredPlace().GetAppleStoredWaterName()},
		{label: "Island (Apple)", value: facts.GetAppleStoredPlace().GetAppleStoredIslandName()},
		{label: "Camera", value: camera}, {label: "Lens", value: facts.GetLensModel()}, {label: "Settings", value: strings.Join(settings, " · ")},
		{label: "People (Apple)", value: photoNamedPeopleForHumanOutput(facts)}, {label: "Albums", value: photoAlbumNamesForHumanOutput(facts)},
		{label: "Favourite", value: favourite}, {label: "Library", value: library},
		{label: "Shared Library contributor (Apple)", value: photoSharedLibraryContributorForHumanOutput(facts)},
		{label: "Title", value: photoDescriptionExcludingPictureFilename(facts.GetAppleTitle())}, {label: "Caption", value: photoDescriptionExcludingPictureFilename(facts.GetAppleCaption())},
		{label: "Editing state (Apple)", value: photoEditingStateForHumanOutput(facts.GetAppleAdjustmentsState())},
		{label: "Moment (Apple)", value: facts.GetAppleMomentTitle()},
	}...)
	if facts.GetPixelWidth() > 0 && facts.GetPixelHeight() > 0 {
		fields = append(fields, cardField{label: "Dimensions", value: fmt.Sprintf("%d × %d pixels", facts.GetPixelWidth(), facts.GetPixelHeight())})
	}
	if facts.AppleDurationSeconds != nil {
		fields = append(fields, cardField{label: "Duration", value: photoDurationForHumanOutput(facts.GetAppleDurationSeconds())})
	}
	for _, trip := range record.GetAppleTrips() {
		tripTitle := photoTripForHumanOutput(trip)
		fields = append(fields, cardField{label: "Trip (Apple)", value: tripTitle})
	}
	fields = append(fields, cardField{label: photoRecognisedTextFieldDisplayName, value: strings.Join(facts.GetAppleRecognisedTextLines(), "\n")})
	return fields
}

func photoInspectionRecordedText(text string) string {
	if text == "" {
		return "Not recorded by Apple"
	}
	return text
}

func photoInspectionPictureFileText(text string) string {
	if text == "" {
		return "Not in the picture file"
	}
	return text
}

func photoDescriptionExcludingPictureFilename(text string) string {
	if photoTextIsOnlyPictureFilename(text) {
		return ""
	}
	return text
}

func photoCameraNameForHumanOutput(facts *photo.AppleLibraryPhotoFacts) string {
	makeName, modelName := strings.TrimSpace(facts.GetCameraMake()), strings.TrimSpace(facts.GetCameraModel())
	if makeName == "" || strings.EqualFold(makeName, modelName) || strings.HasPrefix(strings.ToLower(modelName), strings.ToLower(makeName)+" ") {
		return modelName
	}
	makeWords, modelWords := strings.Fields(makeName), strings.Fields(modelName)
	if len(makeWords) > 0 && len(modelWords) > 0 && strings.EqualFold(makeWords[0], modelWords[0]) {
		return modelName
	}
	return strings.TrimSpace(makeName + " " + modelName)
}

func photoEditingStateForHumanOutput(appleEditingState int32) string {
	switch appleEditingState {
	case 0:
		return "Not edited"
	case 2:
		return "Edited"
	default:
		return "Editing state not known"
	}
}

func photoMediaSubtypeForHumanOutput(facts *photo.AppleLibraryPhotoFacts) string {
	if facts.GetAppleMediaSubtype() == 2 && facts.GetAppleMediaKind() == 0 {
		return "Live Photo"
	}
	if facts.GetAppleMediaSubtype() == 0 {
		return "No additional subtype"
	}
	return "Subtype not known"
}

func photoTripForHumanOutput(trip *photo.AppleLibraryTrip) string {
	tripName := trip.GetAppleTripPlaceTitle()
	if trip.GetAppleTripStartTimeUtc() == nil || trip.GetAppleTripEndTimeUtc() == nil {
		return tripName
	}
	return tripName + " · " + trip.GetAppleTripStartTimeUtc().AsTime().Format(calendarDateLayout) + " – " + trip.GetAppleTripEndTimeUtc().AsTime().Format(calendarDateLayout) + " (UTC dates)"
}

func photoInspectionYesOrNo(recorded bool) string {
	if recorded {
		return "Yes"
	}
	return "No"
}

func photoInspectionUTCOffset(offsetSeconds *int32) string {
	if offsetSeconds == nil {
		return "Not recorded by Apple"
	}
	sign := "+"
	if *offsetSeconds < 0 {
		sign = "-"
	}
	absoluteSeconds := int(math.Abs(float64(*offsetSeconds)))
	offsetText := fmt.Sprintf("%s%02d:%02d", sign, absoluteSeconds/3600, absoluteSeconds/60%60)
	if absoluteSeconds%60 != 0 {
		offsetText += fmt.Sprintf(":%02d", absoluteSeconds%60)
	}
	return fmt.Sprintf("%s (%d seconds)", offsetText, *offsetSeconds)
}

func photoInspectionAppleDeviceOffsetSource(capture *photo.PhotoCaptureLocalTime) string {
	switch capture.GetAppleDeviceUtcOffsetSource() {
	case photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_ADDITIONAL_ATTRIBUTES:
		return "Apple's additional photo attributes"
	case photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_EXTENDED_ATTRIBUTES:
		return "Apple's extended photo attributes"
	case photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_INFERRED:
		return "Inferred by Apple"
	default:
		return "Not recorded by Apple"
	}
}

func photoInspectionApplePlaceType(placeType int32) string {
	kind := "Place"
	switch placeType {
	case 1:
		kind = "Country"
	case 2:
		kind = "Region or administrative area"
	case 3:
		kind = "County or district"
	case 4:
		kind = "Town"
	case 6:
		kind = "Neighbourhood"
	case 7:
		kind = "Sea or ocean"
	case 8:
		kind = "Point of interest"
	case 9:
		kind = "Inland water"
	case 11:
		kind = "Island"
	case 17:
		kind = "Street address"
	}
	return fmt.Sprintf("%s (Apple place type code %d)", kind, placeType)
}

func inspectedRecordFieldsFromAppleLibraryPhotoFacts(record *photo.OpenedPhotoRecord) []cardField {
	facts := record.GetAppleLibraryPhotoFacts()
	capture := facts.GetCaptureLocalTime()
	library := "Personal library"
	if facts.GetAppleLibraryScope() == photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_ICLOUD_SHARED_LIBRARY {
		library = "iCloud Shared Library"
	}
	fields := []cardField{
		{label: "Captured", value: formatPhotoCaptureLocalTime(capture, localTimeWithUTCOffsetAndUTCForOpenedRecord)},
		{label: "Captured UTC (exact)", value: photoInspectionRecordedText(formatExactUTCTimeForInspection(capture.GetCaptureTimeUtc()))},
		{label: "Capture zone", value: photoInspectionRecordedText(capture.GetCaptureTimeZoneName())},
		{label: "Capture offset", value: photoInspectionUTCOffset(capture.CaptureUtcOffsetSeconds)},
		{label: "Capture zone source", value: photoCaptureTimeZoneSourceDescription(capture)},
		{label: "Apple device zone", value: photoInspectionRecordedText(capture.GetAppleDeviceTimeZoneName())},
		{label: "Apple device offset", value: photoInspectionUTCOffset(capture.AppleDeviceUtcOffsetSeconds)},
		{label: "Apple device offset source", value: photoInspectionAppleDeviceOffsetSource(capture)},
		{label: "Library", value: library},
		{label: "Shared Library contributor (Apple)", value: photoSharedLibraryContributorForHumanOutput(facts)},
		{label: "Media kind (Apple)", value: fmt.Sprintf("%s (Apple media kind code %d)", photoMediaKindDisplayName(facts), facts.GetAppleMediaKind())},
		{label: "Media subtype (Apple)", value: fmt.Sprintf("%s (Apple media subtype code %d)", photoMediaSubtypeForHumanOutput(facts), facts.GetAppleMediaSubtype())},
		{label: "Screenshot detected by Apple", value: photoInspectionYesOrNo(facts.GetIsAppleDetectedScreenshot())},
	}
	if location := facts.GetCameraLocation(); location != nil {
		fields = append(fields, cardField{label: "Camera GPS (standard coordinates)", value: fmt.Sprintf("Latitude %.6f, longitude %.6f", location.GetCameraLatitude(), location.GetCameraLongitude())})
		altitude, accuracy := "Not recorded by Apple", "Not recorded by Apple"
		if location.CameraAltitudeMetres != nil {
			altitude = photoAltitudeForHumanOutput(location.GetCameraAltitudeMetres())
		}
		if location.CameraHorizontalAccuracyMetres != nil {
			accuracy = "about " + photoDistanceMetresForHumanOutput(location.GetCameraHorizontalAccuracyMetres())
		}
		fields = append(fields, cardField{label: "Camera altitude (Apple)", value: altitude}, cardField{label: "GPS horizontal accuracy (Apple)", value: accuracy})
	} else {
		fields = append(fields, cardField{label: "Camera GPS", value: "Not recorded by Apple"})
	}
	if chinaGrid := facts.GetAppleChinaShiftedLocation(); chinaGrid != nil {
		fields = append(fields, cardField{label: "Apple China-grid coordinates", value: fmt.Sprintf("Latitude %.6f, longitude %.6f", chinaGrid.GetAppleChinaShiftedLatitude(), chinaGrid.GetAppleChinaShiftedLongitude())})
	}
	place := facts.GetAppleStoredPlace()
	for _, placeField := range []cardField{
		{label: "Address (Apple)", value: place.GetAppleStoredFullAddress()}, {label: "Country code (Apple)", value: place.GetAppleStoredIsoCountryCode()},
		{label: "Country (Apple)", value: place.GetAppleStoredCountryName()}, {label: "Region (Apple)", value: place.GetAppleStoredStateName()},
		{label: "County (Apple)", value: place.GetAppleStoredCountyName()}, {label: "Town (Apple)", value: place.GetAppleStoredTownName()},
		{label: "Neighbourhood (Apple)", value: place.GetAppleStoredNeighbourhoodName()}, {label: "Street (Apple)", value: place.GetAppleStoredStreet()},
		{label: "Point of interest (Apple)", value: place.GetAppleStoredPointOfInterestName()}, {label: "Water (Apple)", value: place.GetAppleStoredWaterName()},
		{label: "Island (Apple)", value: place.GetAppleStoredIslandName()},
	} {
		placeField.value = photoInspectionRecordedText(placeField.value)
		fields = append(fields, placeField)
	}
	if place != nil {
		fields = append(fields, cardField{label: "Apple calls this Home", value: photoInspectionYesOrNo(place.GetAppleSaysHome())})
	}
	for areaIndex, area := range place.GetAppleStoredPlaceAreasInSourceOrder() {
		areaText := area.GetAreaName() + " · " + photoInspectionApplePlaceType(area.GetApplePlaceType())
		if area.AppleAreaSquareMetres != nil {
			areaText += " · " + photoAreaForHumanOutput(area.GetAppleAreaSquareMetres())
		} else {
			areaText += " · size not recorded"
		}
		fields = append(fields, cardField{label: fmt.Sprintf("Area %d (Apple source order)", areaIndex+1), value: areaText})
	}
	fields = append(fields,
		cardField{label: "Camera make", value: photoInspectionPictureFileText(facts.GetCameraMake())},
		cardField{label: "Camera model", value: photoInspectionPictureFileText(facts.GetCameraModel())},
		cardField{label: "Lens", value: photoInspectionPictureFileText(facts.GetLensModel())},
	)
	focalLength, equivalentFocalLength, aperture, exposure, isoSpeed, duration := "Not in the picture file", "Not in the picture file", "Not in the picture file", "Not in the picture file", "Not in the picture file", "Not recorded by Apple"
	if facts.FocalLengthMillimetres != nil {
		focalLength = photoFocalLengthForHumanOutput(facts.GetFocalLengthMillimetres())
	}
	if facts.FocalLength_35MmEquivalentMillimetres != nil {
		equivalentFocalLength = fmt.Sprintf("%d mm", facts.GetFocalLength_35MmEquivalentMillimetres())
	}
	if facts.ApertureFNumber != nil {
		aperture = photoApertureForHumanOutput(facts.GetApertureFNumber())
	}
	if facts.ExposureTimeSeconds != nil {
		exposure = photoExposureTimeForHumanOutput(facts.GetExposureTimeSeconds())
	}
	if facts.IsoSpeed != nil {
		isoSpeed = fmt.Sprintf("ISO %d", facts.GetIsoSpeed())
	}
	if facts.AppleDurationSeconds != nil {
		duration = photoDurationForHumanOutput(facts.GetAppleDurationSeconds())
	}
	fields = append(fields,
		cardField{label: "Focal length", value: focalLength}, cardField{label: "Focal length (35 mm equivalent)", value: equivalentFocalLength},
		cardField{label: "Aperture", value: aperture}, cardField{label: "Exposure", value: exposure}, cardField{label: "Sensitivity", value: isoSpeed},
		cardField{label: "Pixel width", value: fmt.Sprintf("%d pixels", facts.GetPixelWidth())}, cardField{label: "Pixel height", value: fmt.Sprintf("%d pixels", facts.GetPixelHeight())},
		cardField{label: "Duration", value: duration},
	)
	fields = append(fields,
		cardField{label: "Title", value: photoInspectionRecordedText(facts.GetAppleTitle())}, cardField{label: "Caption", value: photoInspectionRecordedText(facts.GetAppleCaption())},
		cardField{label: "Favourite", value: photoInspectionYesOrNo(facts.GetIsFavourite())}, cardField{label: "Hidden", value: photoInspectionYesOrNo(facts.GetIsHidden())},
		cardField{label: "Editing state (Apple)", value: fmt.Sprintf("%s (Apple editing state code %d)", photoEditingStateForHumanOutput(facts.GetAppleAdjustmentsState()), facts.GetAppleAdjustmentsState())},
		cardField{label: "Modified UTC (exact)", value: photoInspectionRecordedText(formatExactUTCTimeForInspection(facts.GetAppleModificationTimeUtc()))},
		cardField{label: "Moment (Apple)", value: photoInspectionRecordedText(facts.GetAppleMomentTitle())},
		cardField{label: "Part of an Apple burst", value: photoInspectionYesOrNo(facts.GetAppleBurstUuid() != "")},
	)
	for personIndex, person := range facts.GetAppleNamedPeople() {
		fields = append(fields, cardField{label: fmt.Sprintf("Person %d (Apple)", personIndex+1), value: person.GetPersonFullName()})
	}
	for albumIndex, album := range facts.GetAppleAlbums() {
		fields = append(fields, cardField{label: fmt.Sprintf("Album %d", albumIndex+1), value: album.GetAlbumTitle()})
	}
	for tripIndex, trip := range record.GetAppleTrips() {
		fields = append(fields,
			cardField{label: fmt.Sprintf("Trip %d (Apple)", tripIndex+1), value: trip.GetAppleTripPlaceTitle()},
			cardField{label: fmt.Sprintf("Trip %d UTC range", tripIndex+1), value: formatExactUTCTimeForInspection(trip.GetAppleTripStartTimeUtc()) + " – " + formatExactUTCTimeForInspection(trip.GetAppleTripEndTimeUtc())},
		)
	}
	fields = append(fields, cardField{label: photoRecognisedTextFieldDisplayName, value: photoInspectionRecordedText(strings.Join(facts.GetAppleRecognisedTextLines(), "\n"))})
	return fields
}

func writePhotoInspectionResponse(writer io.Writer, response *photo.PhotoInspectionResponse, links GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference) error {
	record := response.GetPhotoRecord()
	if record == nil {
		return fmt.Errorf("photo inspection record is missing")
	}
	link := globallyRoutableTrawlLinkText(links.globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(record.GetCanonicalRecordReference()))
	switch response.GetPhotoInspectionLayer() {
	case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_APPLE_FACTS:
		if err := writeCard(writer, card{title: "Apple facts", fields: inspectedRecordFieldsFromAppleLibraryPhotoFacts(record)}); err != nil {
			return err
		}
		return writeNextCommands(writer, []NextCommand{nextCommandOpeningRecord(link)})
	default:
		layerName, stepName := "", ""
		switch response.GetPhotoInspectionLayer() {
		case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_PICTURE_METADATA:
			layerName = "Picture metadata"
			stepName = "fetch_picture"
		case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_NEARBY_PLACES:
			layerName = "Nearby places"
			stepName = "look_up_places_near_spot"
		case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD_MODEL_INPUT:
			layerName = "Card model input"
			stepName = "write_card_model_input"
		case photo.PhotoInspectionLayer_PHOTO_INSPECTION_LAYER_CARD:
			layerName = "Card"
			stepName = "write_photo_card"
		default:
			return fmt.Errorf("photo inspection layer is missing")
		}
		if err := writeLine(writer, layerName+": the step has not run for this photo. The step is not built yet."); err != nil {
			return err
		}
		requiredStepCommandLines := trawlCommandLinesWrappedBetweenArguments("Required step (not built): ", wrappedTrawlCommandContinuationIndent, quotedTrawlCommandArguments(writer, []string{"photos", "run", stepName, "--photo", link}), OutputWidth(writer))
		if err := writeLines(writer, requiredStepCommandLines); err != nil {
			return err
		}
		return writeNextCommands(writer, []NextCommand{nextCommandOpeningRecord(link)})
	}
}
