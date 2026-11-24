// Package photocard is step 4, write_photo_card: one hosted vision model call
// per photo, reading the picture and a plain-text card model input rendered
// from the photo's stored facts, and answering in labelled plain-text sections
// that map onto photo.PhotoCard.
package photocard

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
)

//go:embed photo_card_model_input_template.txt
var photoCardModelInputTemplateText string

var photoCardModelInputTemplate = template.Must(template.New("photo card model input").Parse(photoCardModelInputTemplateText))

// PhotoCardModelInputSources is everything the card model input is made from.
// The nearby places and the known place are nil when there are none.
type PhotoCardModelInputSources struct {
	AppleLibraryPhotoFacts   *photo.AppleLibraryPhotoFacts
	AppleLibraryTrips        []*photo.AppleLibraryTrip
	PictureFetchResult       *photo.PictureFetchResult
	NearbyPlacesLookupResult *photo.NearbyPlacesLookupResult
	KnownPlaceAtCamera       *knownplaces.KnownPlace
	KnownPlaceDistanceMetres float64
}

type photoCardModelInputTemplateValues struct {
	CaptureLocalTime          string
	AppleTrips                []string
	AppleMomentTitle          string
	PictureDescription        string
	CameraAndSettings         string
	CameraPositionIsKnown     bool
	AppleStoredPlace          string
	AppleStoredPlaceAreas     string
	CameraPosition            string
	CameraDirection           string
	KnownPlace                string
	NearbyPlaces              []string
	NearbyPlacesAbsenceReason string
	AppleNamedPeople          string
	AppleCaption              string
	AppleTitle                string
	AppleAlbums               string
	AppleIsFavourite          bool
	AppleRecognisedTextLines  []string
	AppleKnowsNothingMore     bool
}

// RenderPhotoCardModelInput renders the exact text the card model reads.
// Code only gathers facts and computes geometry here; it never decides what
// the photo shows.
func RenderPhotoCardModelInput(sources PhotoCardModelInputSources) (*photo.PhotoCardModelInput, error) {
	facts := sources.AppleLibraryPhotoFacts
	values := photoCardModelInputTemplateValues{
		CaptureLocalTime:   captureLocalTimeForCardModel(facts.GetCaptureLocalTime()),
		AppleMomentTitle:   facts.GetAppleMomentTitle(),
		PictureDescription: pictureDescriptionForCardModel(facts),
		CameraAndSettings:  cameraAndSettingsForCardModel(facts),
		AppleNamedPeople:   appleNamedPeopleForCardModel(facts),
		AppleCaption:       textUnlessOnlyFileName(facts.GetAppleCaption()),
		AppleTitle:         textUnlessOnlyFileName(facts.GetAppleTitle()),
		AppleAlbums:        appleAlbumsForCardModel(facts),
		AppleIsFavourite:   facts.GetIsFavourite(),
	}
	values.AppleRecognisedTextLines = facts.GetAppleRecognisedTextLines()
	values.AppleKnowsNothingMore = values.AppleNamedPeople == "" && values.AppleCaption == "" && values.AppleTitle == "" &&
		values.AppleAlbums == "" && !values.AppleIsFavourite && len(values.AppleRecognisedTextLines) == 0
	for _, trip := range sources.AppleLibraryTrips {
		values.AppleTrips = append(values.AppleTrips, appleTripForCardModel(trip))
	}
	if cameraLocation := facts.GetCameraLocation(); cameraLocation != nil {
		values.CameraPositionIsKnown = true
		values.AppleStoredPlace = appleStoredPlaceForCardModel(facts.GetAppleStoredPlace())
		values.AppleStoredPlaceAreas = appleStoredPlaceAreasForCardModel(facts.GetAppleStoredPlace())
		values.CameraPosition = cameraPositionForCardModel(cameraLocation)
		cameraView := cameraViewForPhoto(facts, sources.PictureFetchResult)
		values.CameraDirection = cameraView.description()
		if sources.KnownPlaceAtCamera != nil {
			values.KnownPlace = knownPlaceForCardModel(sources.KnownPlaceAtCamera, sources.KnownPlaceDistanceMetres)
		}
		values.NearbyPlaces, values.NearbyPlacesAbsenceReason = nearbyPlacesForCardModel(cameraLocation, sources.NearbyPlacesLookupResult)
	}
	var renderedText bytes.Buffer
	if err := photoCardModelInputTemplate.Execute(&renderedText, values); err != nil {
		return nil, fmt.Errorf("render card model input: %w", err)
	}
	modelInputText := renderedText.String()
	modelInputSHA256 := sha256.Sum256([]byte(modelInputText))
	return &photo.PhotoCardModelInput{
		ApplePhotoUuid:            facts.GetApplePhotoUuid(),
		PhotoCardModelInputText:   modelInputText,
		PhotoCardModelInputSha256: hex.EncodeToString(modelInputSHA256[:]),
	}, nil
}

func captureLocalTimeForCardModel(capture *photo.PhotoCaptureLocalTime) string {
	captureTimeUTC := capture.GetCaptureTimeUtc().AsTime().UTC()
	if capture.CaptureUtcOffsetSeconds == nil {
		return captureTimeUTC.Format("Monday 2 January 2006 at 15:04:05") + " UTC; the local time zone is not known"
	}
	offsetSeconds := int(capture.GetCaptureUtcOffsetSeconds())
	localTime := captureTimeUTC.In(time.FixedZone("", offsetSeconds))
	zone := "UTC" + localTime.Format("-07:00")
	if name := capture.GetCaptureTimeZoneName(); name != "" {
		zone += ", " + name
	}
	return localTime.Format("Monday 2 January 2006 at 15:04:05") + " local time (" + zone + "), " + captureTimeUTC.Format("15:04:05") + " UTC"
}

func appleTripForCardModel(trip *photo.AppleLibraryTrip) string {
	tripName := strings.TrimSpace(trip.GetAppleTripPlaceTitle())
	if dateTitle := strings.TrimSpace(trip.GetAppleTripDateTitle()); dateTitle != "" {
		tripName = strings.TrimSpace(tripName + ", " + dateTitle)
	}
	return `"` + tripName + `"`
}

const (
	appleMediaKindVideo          = 1
	appleMediaSubtypeLivePhoto   = 2
	appleAdjustmentsStateEdited  = 2
	cameraPositionDecimalPlaces  = 6
	fullFrameDiagonalMillimetres = 43.27
)

func pictureDescriptionForCardModel(facts *photo.AppleLibraryPhotoFacts) string {
	var sentences []string
	switch {
	case facts.GetAppleMediaKind() == appleMediaKindVideo && facts.AppleDurationSeconds != nil:
		sentences = append(sentences, fmt.Sprintf("The attached picture is one frame from the middle of a video %s seconds long.", roundedNumber(facts.GetAppleDurationSeconds(), 1)))
	case facts.GetAppleMediaKind() == appleMediaKindVideo:
		sentences = append(sentences, "The attached picture is one frame from the middle of a video.")
	case facts.GetIsAppleDetectedScreenshot():
		sentences = append(sentences, "A screenshot of a device's screen.")
	case facts.GetAppleMediaSubtype() == appleMediaSubtypeLivePhoto:
		sentences = append(sentences, "A Live Photo; its still picture is attached.")
	default:
		sentences = append(sentences, "A photo.")
	}
	if width, height := facts.GetPixelWidth(), facts.GetPixelHeight(); width > 0 && height > 0 {
		orientation := "landscape"
		switch {
		case height > width:
			orientation = "portrait"
		case height == width:
			orientation = "square"
		}
		sentences = append(sentences, fmt.Sprintf("The original is %d × %d pixels, %s.", width, height, orientation))
	}
	if facts.GetAppleAdjustmentsState() == appleAdjustmentsStateEdited {
		sentences = append(sentences, "It was edited in Apple Photos, and the attached picture shows the edit.")
	}
	if facts.GetAppleLibraryScope() == photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_ICLOUD_SHARED_LIBRARY {
		switch {
		case facts.AppleSharedLibraryContributorIsOwner == nil:
			sentences = append(sentences, "It is in the iCloud Shared Library, and Apple Photos does not record who added it.")
		case facts.GetAppleSharedLibraryContributorIsOwner():
			sentences = append(sentences, "It is in the iCloud Shared Library, added by the person.")
		default:
			sentences = append(sentences, "It is in the iCloud Shared Library, added by another person who shares the library.")
		}
	}
	return strings.Join(sentences, " ")
}

func cameraAndSettingsForCardModel(facts *photo.AppleLibraryPhotoFacts) string {
	var parts []string
	cameraName := strings.TrimSpace(facts.GetCameraModel())
	if makeName := strings.TrimSpace(facts.GetCameraMake()); makeName != "" && !strings.HasPrefix(strings.ToLower(cameraName), strings.ToLower(makeName)) {
		cameraName = strings.TrimSpace(makeName + " " + cameraName)
	}
	if cameraName != "" {
		parts = append(parts, cameraName)
	}
	if lens := strings.TrimSpace(facts.GetLensModel()); lens != "" {
		parts = append(parts, "lens "+lens)
	}
	if facts.FocalLength_35MmEquivalentMillimetres != nil {
		parts = append(parts, fmt.Sprintf("%d mm focal length (35 mm equivalent)", facts.GetFocalLength_35MmEquivalentMillimetres()))
	} else if facts.FocalLengthMillimetres != nil {
		parts = append(parts, roundedNumber(facts.GetFocalLengthMillimetres(), 1)+" mm focal length")
	}
	if facts.ApertureFNumber != nil {
		parts = append(parts, "f/"+roundedNumber(facts.GetApertureFNumber(), 1))
	}
	if facts.ExposureTimeSeconds != nil {
		exposure := facts.GetExposureTimeSeconds()
		if exposure > 0 && exposure <= 0.5 {
			parts = append(parts, fmt.Sprintf("1/%.0f s", math.Round(1/exposure)))
		} else {
			parts = append(parts, roundedNumber(exposure, 1)+" s")
		}
	}
	if facts.IsoSpeed != nil {
		parts = append(parts, fmt.Sprintf("ISO %d", facts.GetIsoSpeed()))
	}
	return strings.Join(parts, ", ")
}

func appleStoredPlaceForCardModel(place *photo.AppleStoredPlace) string {
	if place == nil {
		return ""
	}
	description := strings.Join(strings.Fields(place.GetAppleStoredFullAddress()), " ")
	for _, namedPlace := range []struct{ label, name string }{
		{"point of interest", place.GetAppleStoredPointOfInterestName()},
		{"water", place.GetAppleStoredWaterName()},
		{"island", place.GetAppleStoredIslandName()},
	} {
		if namedPlace.name == "" || strings.Contains(description, namedPlace.name) {
			continue
		}
		description = strings.TrimPrefix(description+"; "+namedPlace.label+": "+namedPlace.name, "; ")
	}
	if place.GetAppleSaysHome() {
		description = strings.TrimPrefix(description+"; Apple Photos marks this as the person's home", "; ")
	}
	return description
}

func appleStoredPlaceAreasForCardModel(place *photo.AppleStoredPlace) string {
	var areaNames []string
	for _, area := range place.GetAppleStoredPlaceAreasInSourceOrder() {
		if name := strings.TrimSpace(area.GetAreaName()); name != "" {
			areaNames = append(areaNames, name)
		}
	}
	return strings.Join(areaNames, ", ")
}

func cameraPositionForCardModel(cameraLocation *photo.PhotoCameraLocation) string {
	position := fmt.Sprintf("%.*f, %.*f", cameraPositionDecimalPlaces, cameraLocation.GetCameraLatitude(), cameraPositionDecimalPlaces, cameraLocation.GetCameraLongitude())
	if cameraLocation.CameraHorizontalAccuracyMetres != nil {
		position += ", accurate to about " + distanceForCardModel(cameraLocation.GetCameraHorizontalAccuracyMetres())
	}
	if cameraLocation.CameraAltitudeMetres != nil {
		position += ", " + roundedNumber(cameraLocation.GetCameraAltitudeMetres(), 0) + " m above sea level"
	}
	return position
}

// cameraView is where the camera pointed and how wide it saw, worked out
// from the picture file's compass bearing and the lens.
type cameraView struct {
	bearingIsKnown              bool
	bearingDegrees              float64
	bearingReference            photo.CameraBearingReference
	horizontalFieldOfViewKnown  bool
	horizontalFieldOfViewDegree float64
	pictureOrientation          string
	focalLength35mmEquivalent   int32
}

func cameraViewForPhoto(facts *photo.AppleLibraryPhotoFacts, pictureFetchResult *photo.PictureFetchResult) cameraView {
	view := cameraView{}
	if pictureFetchResult == nil || pictureFetchResult.CameraBearingDegrees == nil {
		return view
	}
	view.bearingIsKnown = true
	view.bearingDegrees = math.Mod(pictureFetchResult.GetCameraBearingDegrees()+360, 360)
	view.bearingReference = pictureFetchResult.GetCameraBearingReference()
	width, height := float64(facts.GetPixelWidth()), float64(facts.GetPixelHeight())
	if facts.FocalLength_35MmEquivalentMillimetres == nil || facts.GetFocalLength_35MmEquivalentMillimetres() <= 0 || width <= 0 || height <= 0 {
		return view
	}
	// A 35 mm equivalent focal length matches the diagonal of a 36 × 24 mm
	// frame, so the picture's own shape gives its horizontal width.
	view.focalLength35mmEquivalent = facts.GetFocalLength_35MmEquivalentMillimetres()
	equivalentWidthMillimetres := fullFrameDiagonalMillimetres * width / math.Hypot(width, height)
	view.horizontalFieldOfViewDegree = 2 * math.Atan(equivalentWidthMillimetres/(2*float64(view.focalLength35mmEquivalent))) * 180 / math.Pi
	view.horizontalFieldOfViewKnown = true
	view.pictureOrientation = "landscape"
	if height > width {
		view.pictureOrientation = "portrait"
	}
	return view
}

func (view cameraView) description() string {
	if !view.bearingIsKnown {
		return "The picture file does not record which way the camera faced."
	}
	north := "true north"
	switch view.bearingReference {
	case photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_TRUE_NORTH:
		north = "true north"
	case photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_MAGNETIC_NORTH:
		north = "magnetic north"
	case photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_UNSPECIFIED:
		north = "north (the file does not say whether true or magnetic)"
	}
	description := fmt.Sprintf("The camera faced %s° from %s (%s).", roundedCompassDegrees(view.bearingDegrees), north, compassPointName(view.bearingDegrees))
	if !view.horizontalFieldOfViewKnown {
		return description + " How wide it saw is not known."
	}
	halfView := view.horizontalFieldOfViewDegree / 2
	return description + fmt.Sprintf(" With a %d mm equivalent lens and a %s picture it saw about %s° across, from %s° to %s°.",
		view.focalLength35mmEquivalent, view.pictureOrientation, roundedNumber(view.horizontalFieldOfViewDegree, 0),
		roundedCompassDegrees(view.bearingDegrees-halfView), roundedCompassDegrees(view.bearingDegrees+halfView))
}

func knownPlaceForCardModel(knownPlace *knownplaces.KnownPlace, distanceMetres float64) string {
	kind := "a place the person knows"
	switch knownPlace.GetKnownPlaceKind() {
	case knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_HOME:
		kind = "the person's home at the time"
	case knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_WORK:
		kind = "the person's workplace at the time"
	case knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_UNSPECIFIED, knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_OTHER:
	}
	return fmt.Sprintf(`The camera stood %s from "%s", %s. The named places below around it are neighbours, shops and streets near that place.`,
		distanceForCardModel(distanceMetres), knownPlace.GetKnownPlaceName(), kind)
}

func nearbyPlacesForCardModel(cameraLocation *photo.PhotoCameraLocation, lookupResult *photo.NearbyPlacesLookupResult) ([]string, string) {
	switch lookupResult.GetPlaceLookupOutcome() {
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_UNSPECIFIED:
		return nil, "No OpenStreetMap lookup has been made for this position."
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED:
		return nil, "The OpenStreetMap lookup for this position failed, so no named places near it are known."
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_NO_PLACES:
		return nil, "OpenStreetMap has no named places near this position."
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_PLACES_FOUND:
	}
	var lines []string
	for _, placeFromCamera := range placelookup.NearbyPlacesFromCameraNearestFirst(cameraLocation, lookupResult.GetNearbyPlacesNearestFirst()) {
		direction := placeFromCamera.GetDirectionFromCameraDegrees()
		line := strings.Join([]string{
			placeFromCamera.GetNearbyPlace().GetPlaceName(),
			placeFromCamera.GetNearbyPlace().GetPlaceKind(),
			distanceForCardModel(placeFromCamera.GetDistanceFromCameraMetres()),
			compassPointName(direction) + " " + roundedCompassDegrees(direction) + "°",
		}, " · ")
		lines = append(lines, line)
	}
	return lines, ""
}

func appleNamedPeopleForCardModel(facts *photo.AppleLibraryPhotoFacts) string {
	var names []string
	for _, person := range facts.GetAppleNamedPeople() {
		if name := strings.TrimSpace(person.GetPersonFullName()); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func appleAlbumsForCardModel(facts *photo.AppleLibraryPhotoFacts) string {
	var titles []string
	for _, album := range facts.GetAppleAlbums() {
		if title := strings.TrimSpace(album.GetAlbumTitle()); title != "" {
			titles = append(titles, `"`+title+`"`)
		}
	}
	return strings.Join(titles, ", ")
}

var pictureFileNameExtensions = []string{".heic", ".heif", ".jpg", ".jpeg", ".png", ".gif", ".tif", ".tiff", ".dng", ".mov", ".mp4"}

// textUnlessOnlyFileName drops a caption or title that only repeats the
// picture's file name, which some imports store.
func textUnlessOnlyFileName(text string) string {
	text = strings.TrimSpace(text)
	if strings.ContainsAny(text, " \t\n") {
		return text
	}
	for _, extension := range pictureFileNameExtensions {
		if strings.HasSuffix(strings.ToLower(text), extension) {
			return ""
		}
	}
	return text
}

var compassPointNames = []string{"north", "north-north-east", "north-east", "east-north-east", "east", "east-south-east", "south-east", "south-south-east",
	"south", "south-south-west", "south-west", "west-south-west", "west", "west-north-west", "north-west", "north-north-west"}

func compassPointName(degreesFromNorth float64) string {
	normalized := math.Mod(math.Mod(degreesFromNorth, 360)+360, 360)
	return compassPointNames[int(math.Round(normalized/22.5))%len(compassPointNames)]
}

func roundedCompassDegrees(degreesFromNorth float64) string {
	normalized := math.Mod(math.Mod(degreesFromNorth, 360)+360, 360)
	rounded := int(math.Round(normalized)) % 360
	return fmt.Sprintf("%d", rounded)
}

func distanceForCardModel(metres float64) string {
	if metres >= 1000 {
		return roundedNumber(metres/1000, 1) + " km"
	}
	return roundedNumber(metres, 0) + " m"
}

func roundedNumber(number float64, decimalPlaces int) string {
	text := fmt.Sprintf("%.*f", decimalPlaces, number)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" {
		return "0"
	}
	return text
}
