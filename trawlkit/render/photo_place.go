package render

import (
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

const photoPlaceHierarchySeparator = " › "

func appleStoredPlaceHierarchyNamesSmallToLarge(place *photo.AppleStoredPlace) []string {
	localNames := []string{place.GetAppleStoredPointOfInterestName(), place.GetAppleStoredStreet(), place.GetAppleStoredNeighbourhoodName(), place.GetAppleStoredTownName()}
	names := append(localNames, place.GetAppleStoredWaterName(), place.GetAppleStoredIslandName(), place.GetAppleStoredCountyName(), place.GetAppleStoredStateName(), place.GetAppleStoredCountryName())
	if place.GetAppleStoredWaterName() != "" {
		hasLocalLandName := false
		for _, name := range localNames {
			if name != "" && !strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(place.GetAppleStoredWaterName())) {
				hasLocalLandName = true
			}
		}
		if !hasLocalLandName {
			names = []string{place.GetAppleStoredWaterName(), place.GetAppleStoredIslandName()}
		}
	}
	var uniqueNames []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		alreadyIncluded := false
		for _, earlierName := range uniqueNames {
			if strings.EqualFold(earlierName, name) {
				alreadyIncluded = true
				break
			}
		}
		if !alreadyIncluded {
			uniqueNames = append(uniqueNames, name)
		}
	}
	if len(uniqueNames) == 0 && place.GetAppleStoredFullAddress() != "" {
		uniqueNames = append(uniqueNames, place.GetAppleStoredFullAddress())
	}
	if len(uniqueNames) == 0 && place.GetAppleStoredIsoCountryCode() != "" {
		uniqueNames = append(uniqueNames, place.GetAppleStoredIsoCountryCode())
	}
	return uniqueNames
}

func photoLocationUncertaintyForHumanOutput(location *photo.PhotoCameraLocation) string {
	if location.GetCameraHorizontalAccuracyMetres() < 1000 {
		return ""
	}
	return " (GPS within " + photoDistanceMetresForHumanOutput(location.GetCameraHorizontalAccuracyMetres()) + ")"
}

func photoPlaceForHumanOutput(place *photo.AppleStoredPlace, location *photo.PhotoCameraLocation) string {
	placeNames := appleStoredPlaceHierarchyNamesSmallToLarge(place)
	if len(placeNames) == 0 {
		placeNames = []string{"Place not recorded"}
	}
	placeDescription := strings.Join(placeNames, photoPlaceHierarchySeparator)
	if uncertainty := photoLocationUncertaintyForHumanOutput(location); uncertainty != "" {
		placeDescription = strings.TrimSpace(uncertainty) + " · " + placeDescription
	}
	return placeDescription
}

func photoListPlaceForHumanOutput(place *photo.AppleStoredPlace, location *photo.PhotoCameraLocation) string {
	return photoListPlaceWithNameWidthForHumanOutput(place, location, 0)
}

func photoListPlaceWithinTwoLinesForHumanOutput(place *photo.AppleStoredPlace, location *photo.PhotoCameraLocation, columnWidth int) string {
	placeDescription := photoListPlaceForHumanOutput(place, location)
	for nameWidth := columnWidth; len(Wrap(placeDescription, columnWidth)) > 2 && nameWidth > 0; nameWidth-- {
		placeDescription = photoListPlaceWithNameWidthForHumanOutput(place, location, nameWidth)
	}
	return placeDescription
}

func photoListPlaceWithNameWidthForHumanOutput(place *photo.AppleStoredPlace, location *photo.PhotoCameraLocation, maximumNameWidth int) string {
	placeNames := nonEmptySearchResultNames([]string{place.GetAppleStoredStreet(), place.GetAppleStoredPointOfInterestName(), place.GetAppleStoredNeighbourhoodName(), place.GetAppleStoredTownName(), place.GetAppleStoredWaterName(), place.GetAppleStoredIslandName(), place.GetAppleStoredStateName()})
	placeHierarchyNames := appleStoredPlaceHierarchyNamesSmallToLarge(place)
	isOverWater := place.GetAppleStoredWaterName() != "" && len(placeHierarchyNames) > 0 && strings.EqualFold(placeHierarchyNames[0], place.GetAppleStoredWaterName())
	if isOverWater {
		placeNames = nonEmptySearchResultNames([]string{place.GetAppleStoredWaterName(), place.GetAppleStoredIslandName()})
	}
	placeNames = placeNames[:min(len(placeNames), 2)]
	if maximumNameWidth > 0 {
		for index, name := range placeNames {
			placeNames[index] = Truncate(name, maximumNameWidth)
		}
	}
	if !isOverWater {
		country := place.GetAppleStoredIsoCountryCode()
		if country == "" {
			country = place.GetAppleStoredCountryName()
		}
		placeNames = nonEmptySearchResultNames(append(placeNames, country))
	}
	if len(placeNames) == 0 {
		return photoPlaceForHumanOutput(place, location)
	}
	placeDescription := strings.Join(placeNames, photoPlaceHierarchySeparator)
	if uncertainty := strings.TrimSpace(photoLocationUncertaintyForHumanOutput(location)); uncertainty != "" {
		placeDescription = uncertainty + " · " + placeDescription
	}
	return placeDescription
}
