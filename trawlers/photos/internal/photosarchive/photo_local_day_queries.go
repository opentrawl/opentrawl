package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type selectedPhotoLocalDay struct {
	localDate                *presentation.CalendarDate
	photoUUIDsInCaptureOrder []string
}

const maximumPhotoDayStayDistanceFromFirstPhotoMetres = 300

type applePhotoDayStayNameSpecificity uint8

const (
	applePhotoDayStayNamePointOfInterest applePhotoDayStayNameSpecificity = iota
	applePhotoDayStayNameNamedAreaOrStreet
	applePhotoDayStayNameStreetAddress
	applePhotoDayStayNameAdministrativeArea
)

type applePhotoDayStayNameCandidate struct {
	applePlaceName                   string
	applePlaceNameSpecificity        applePhotoDayStayNameSpecificity
	appleStoredPlaceWithSelectedName *photo.AppleStoredPlace
}

func (photosArchive *PhotosArchive) LocalPhotoDays(ctx context.Context, from, to *presentation.CalendarDate, limit int, knownPlaces *knownplaces.KnownPlacesFile) (*photo.PhotoLocalDayListResponse, error) {
	rows, err := photosArchive.database.QueryContext(ctx, `select apple_photo_uuid,capture_time_utc,capture_utc_offset_seconds from apple_library_photo where absent_from_apple_library_since_time_utc is null order by capture_time_utc,apple_photo_uuid`)
	if err != nil {
		return nil, fmt.Errorf("select photo local days: %w", err)
	}
	daysByDate := map[int32]*selectedPhotoLocalDay{}
	response := &photo.PhotoLocalDayListResponse{KnownPlacesAreConfigured: len(knownPlaces.GetKnownPlaces()) > 0}
	for rows.Next() {
		var uuid, captureUTC string
		var offset sql.NullInt32
		if err := rows.Scan(&uuid, &captureUTC, &offset); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read photo local day: %w", err)
		}
		captureTime, err := parseArchiveTime(captureUTC)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		localTime := &photo.PhotoCaptureLocalTime{CaptureTimeUtc: timestamppb.New(captureTime), CaptureUtcOffsetSeconds: optionalInt32FromColumn(offset)}
		if localTime.CaptureUtcOffsetSeconds == nil {
			response.PhotosWithoutKnownCaptureLocalDayCount++
			continue
		}
		localDate := photoLocalCalendarDate(localTime)
		if !photoLocalDateMatches(localDate, nil, from, to) {
			continue
		}
		dateNumber := photoCalendarDateNumber(localDate)
		if daysByDate[dateNumber] == nil {
			daysByDate[dateNumber] = &selectedPhotoLocalDay{localDate: localDate}
		}
		daysByDate[dateNumber].photoUUIDsInCaptureOrder = append(daysByDate[dateNumber].photoUUIDsInCaptureOrder, uuid)
	}
	rowError := rows.Err()
	_ = rows.Close()
	if rowError != nil {
		return nil, fmt.Errorf("read photo local days: %w", rowError)
	}
	dateNumbers := make([]int32, 0, len(daysByDate))
	for dateNumber := range daysByDate {
		dateNumbers = append(dateNumbers, dateNumber)
	}
	sort.Slice(dateNumbers, func(first, second int) bool { return dateNumbers[first] > dateNumbers[second] })
	response.TotalMatchingLocalDayCount = uint64(len(dateNumbers))
	if limit > 0 && len(dateNumbers) > limit {
		response.MoreMatchingLocalDaysExist = true
		dateNumbers = dateNumbers[:limit]
	}
	for _, dateNumber := range dateNumbers {
		selectedDay := daysByDate[dateNumber]
		facts, err := photosArchive.readPhotoFactsInBatches(ctx, selectedDay.photoUUIDsInCaptureOrder)
		if err != nil {
			return nil, err
		}
		day := &photo.PhotoLocalDayRecord{PhotoLocalDate: selectedDay.localDate, FirstPhotoCaptureLocalTime: facts[0].GetCaptureLocalTime(), LastPhotoCaptureLocalTime: facts[len(facts)-1].GetCaptureLocalTime()}
		day.PhotoDayStaysInCaptureOrder, day.PhotosWithoutPlaceCount = photoDayConsecutiveStays(facts, selectedDay.localDate, knownPlaces)
		response.PhotoLocalDayRecordsNewestFirst = append(response.PhotoLocalDayRecordsNewestFirst, day)
	}
	return response, nil
}

func photoDayConsecutiveStays(facts []*photo.AppleLibraryPhotoFacts, localDate *presentation.CalendarDate, knownPlaces *knownplaces.KnownPlacesFile) ([]*photo.PhotoDayStay, uint64) {
	var stays []*photo.PhotoDayStay
	var photosWithoutPlaceCount uint64
	var currentStayFacts []*photo.AppleLibraryPhotoFacts
	appendCurrentStay := func() {
		if len(currentStayFacts) == 0 {
			return
		}
		firstPhoto := currentStayFacts[0]
		representativeName := mostSharedApplePhotoDayStayName(currentStayFacts)
		matchedKnownPlace, distance := knownPlaceAtPhoto(firstPhoto, localDate, knownPlaces)
		stay := &photo.PhotoDayStay{
			FirstPhotoCaptureLocalTime:     firstPhoto.GetCaptureLocalTime(),
			LastPhotoCaptureLocalTime:      currentStayFacts[len(currentStayFacts)-1].GetCaptureLocalTime(),
			RepresentativeAppleStoredPlace: representativeName.appleStoredPlaceWithSelectedName,
			MostSharedApplePlaceName:       representativeName.applePlaceName,
			MatchedKnownPlaceName:          matchedKnownPlace,
			DistanceFromHomeMetres:         distance,
			PhotoCount:                     uint64(len(currentStayFacts)),
		}
		for _, fact := range currentStayFacts {
			stay.CameraLocation = photoCameraLocationWithLargestRecordedHorizontalAccuracy(stay.GetCameraLocation(), fact.GetCameraLocation())
		}
		stays = append(stays, stay)
	}
	for _, fact := range facts {
		if applePhotoDayStayName(fact.GetAppleStoredPlace()).applePlaceName == "" {
			photosWithoutPlaceCount++
			continue
		}
		if len(currentStayFacts) > 0 && !photoBelongsToStayAnchoredAtFirstPhoto(fact, currentStayFacts[0]) {
			appendCurrentStay()
			currentStayFacts = nil
		}
		currentStayFacts = append(currentStayFacts, fact)
	}
	appendCurrentStay()
	return stays, photosWithoutPlaceCount
}

func photoBelongsToStayAnchoredAtFirstPhoto(nextPhoto, firstPhoto *photo.AppleLibraryPhotoFacts) bool {
	if nextPhoto.GetCaptureLocalTime().GetCaptureUtcOffsetSeconds() != firstPhoto.GetCaptureLocalTime().GetCaptureUtcOffsetSeconds() {
		return false
	}
	firstLocation := firstPhoto.GetCameraLocation()
	nextLocation := nextPhoto.GetCameraLocation()
	if firstLocation != nil && nextLocation != nil {
		return photoCameraDistanceMetres(firstLocation.GetCameraLatitude(), firstLocation.GetCameraLongitude(), nextLocation.GetCameraLatitude(), nextLocation.GetCameraLongitude()) <= maximumPhotoDayStayDistanceFromFirstPhotoMetres
	}
	if firstLocation != nil || nextLocation != nil {
		return false
	}
	return applePhotoDayStoredPlaceIdentity(nextPhoto.GetAppleStoredPlace()) == applePhotoDayStoredPlaceIdentity(firstPhoto.GetAppleStoredPlace())
}

func mostSharedApplePhotoDayStayName(facts []*photo.AppleLibraryPhotoFacts) applePhotoDayStayNameCandidate {
	candidatePhotoCounts := map[string]uint64{}
	var selectedName applePhotoDayStayNameCandidate
	var selectedPhotoCount uint64
	for _, fact := range facts {
		candidate := applePhotoDayStayName(fact.GetAppleStoredPlace())
		candidateIdentity := strings.ToLower(candidate.applePlaceName)
		candidatePhotoCounts[candidateIdentity]++
		candidatePhotoCount := candidatePhotoCounts[candidateIdentity]
		if selectedName.applePlaceName == "" || candidate.applePlaceNameSpecificity < selectedName.applePlaceNameSpecificity || candidate.applePlaceNameSpecificity == selectedName.applePlaceNameSpecificity && candidatePhotoCount > selectedPhotoCount {
			selectedName = candidate
			selectedPhotoCount = candidatePhotoCount
		} else if strings.EqualFold(candidate.applePlaceName, selectedName.applePlaceName) {
			selectedPhotoCount = candidatePhotoCount
		}
	}
	return selectedName
}

func applePhotoDayStayName(place *photo.AppleStoredPlace) applePhotoDayStayNameCandidate {
	if name := strings.TrimSpace(place.GetAppleStoredPointOfInterestName()); name != "" {
		return applePhotoDayStayNameCandidate{applePlaceName: name, applePlaceNameSpecificity: applePhotoDayStayNamePointOfInterest, appleStoredPlaceWithSelectedName: place}
	}
	for _, name := range []string{place.GetAppleStoredNeighbourhoodName(), place.GetAppleStoredWaterName(), place.GetAppleStoredIslandName()} {
		if name = strings.TrimSpace(name); name != "" {
			return applePhotoDayStayNameCandidate{applePlaceName: name, applePlaceNameSpecificity: applePhotoDayStayNameNamedAreaOrStreet, appleStoredPlaceWithSelectedName: place}
		}
	}
	if name := strings.TrimSpace(place.GetAppleStoredStreet()); name != "" {
		applePlaceNameSpecificity := applePhotoDayStayNameNamedAreaOrStreet
		firstCharacter, _ := utf8.DecodeRuneInString(name)
		if unicode.IsDigit(firstCharacter) {
			applePlaceNameSpecificity = applePhotoDayStayNameStreetAddress
		}
		return applePhotoDayStayNameCandidate{applePlaceName: name, applePlaceNameSpecificity: applePlaceNameSpecificity, appleStoredPlaceWithSelectedName: place}
	}
	for _, name := range []string{place.GetAppleStoredTownName(), place.GetAppleStoredCountyName(), place.GetAppleStoredStateName(), place.GetAppleStoredCountryName(), place.GetAppleStoredFullAddress(), place.GetAppleStoredIsoCountryCode()} {
		if name = strings.TrimSpace(name); name != "" {
			return applePhotoDayStayNameCandidate{applePlaceName: name, applePlaceNameSpecificity: applePhotoDayStayNameAdministrativeArea, appleStoredPlaceWithSelectedName: place}
		}
	}
	return applePhotoDayStayNameCandidate{}
}

func applePhotoDayStoredPlaceIdentity(place *photo.AppleStoredPlace) string {
	return strings.ToLower(strings.Join([]string{applePhotoDayStayName(place).applePlaceName, place.GetAppleStoredTownName(), place.GetAppleStoredStateName(), place.GetAppleStoredCountryName()}, "\x00"))
}

func photoCameraLocationWithLargestRecordedHorizontalAccuracy(currentCameraLocation, nextCameraLocation *photo.PhotoCameraLocation) *photo.PhotoCameraLocation {
	if nextCameraLocation == nil {
		return currentCameraLocation
	}
	if currentCameraLocation == nil {
		return nextCameraLocation
	}
	if nextCameraLocation.CameraHorizontalAccuracyMetres != nil && (currentCameraLocation.CameraHorizontalAccuracyMetres == nil || nextCameraLocation.GetCameraHorizontalAccuracyMetres() > currentCameraLocation.GetCameraHorizontalAccuracyMetres()) {
		return nextCameraLocation
	}
	return currentCameraLocation
}

func knownPlaceAtPhoto(fact *photo.AppleLibraryPhotoFacts, localDate *presentation.CalendarDate, knownPlaces *knownplaces.KnownPlacesFile) (string, *float64) {
	location := fact.GetCameraLocation()
	if location == nil {
		return "", nil
	}
	var matchedPlaceName string
	var nearestHomeDistance *float64
	nearestMatchedDistance := math.Inf(1)
	for _, knownPlace := range knownPlaces.GetKnownPlaces() {
		if !photoLocalDateMatches(localDate, nil, knownPlace.GetKnownPlaceValidFromDate(), knownPlace.GetKnownPlaceValidUntilDate()) {
			continue
		}
		distance := photoCameraDistanceMetres(location.GetCameraLatitude(), location.GetCameraLongitude(), knownPlace.GetKnownPlaceLatitude(), knownPlace.GetKnownPlaceLongitude())
		if distance <= knownPlace.GetKnownPlaceRadiusMetres() && distance < nearestMatchedDistance {
			matchedPlaceName = knownPlace.GetKnownPlaceName()
			nearestMatchedDistance = distance
		}
		if knownPlace.GetKnownPlaceKind() == knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_HOME && (nearestHomeDistance == nil || distance < *nearestHomeDistance) {
			nearestHomeDistance = new(distance)
		}
	}
	return matchedPlaceName, nearestHomeDistance
}

func photoCameraDistanceMetres(latitude, longitude, otherLatitude, otherLongitude float64) float64 {
	latitudeRadians := latitude * math.Pi / 180
	otherLatitudeRadians := otherLatitude * math.Pi / 180
	latitudeDifference := otherLatitudeRadians - latitudeRadians
	longitudeDifference := (otherLongitude - longitude) * math.Pi / 180
	squaredSineDistance := math.Pow(math.Sin(latitudeDifference/2), 2) + math.Cos(latitudeRadians)*math.Cos(otherLatitudeRadians)*math.Pow(math.Sin(longitudeDifference/2), 2)
	return 6371008.8 * 2 * math.Asin(math.Sqrt(min(1, squaredSineDistance)))
}
