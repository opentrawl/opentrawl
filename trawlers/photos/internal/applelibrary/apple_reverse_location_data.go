package applelibrary

import (
	"fmt"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// Apple's place type codes in mapItem.sortedPlaceInfos.
const (
	applePlaceTypeSeaOrOcean      = 7
	applePlaceTypePointOfInterest = 8
	applePlaceTypeInlandWater     = 9
	applePlaceTypeIsland          = 11
)

// decodeAppleReverseLocationData reads ZREVERSELOCATIONDATA, a keyed archive
// whose root is PLRevGeoLocationInfo. It returns nil when Apple stored none.
func decodeAppleReverseLocationData(reverseLocationData []byte) (*photo.AppleStoredPlace, error) {
	if len(reverseLocationData) == 0 {
		return nil, nil
	}
	keyedArchive, err := decodeAppleKeyedArchive(reverseLocationData)
	if err != nil {
		return nil, err
	}
	locationInfo := keyedArchive.rootObject
	appleSaysHome, ok := keyedArchive.resolve(locationInfo["isHome"]).(bool)
	if !ok {
		return nil, fmt.Errorf("stored place has no isHome value")
	}
	postalAddress := keyedArchive.dictionary(locationInfo["postalAddress"])
	postalAddressField := func(appleKey string) string {
		return strings.TrimSpace(keyedArchive.text(postalAddress[appleKey]))
	}
	appleStoredPlace := &photo.AppleStoredPlace{
		AppleStoredFullAddress:       strings.TrimSpace(keyedArchive.text(locationInfo["addressString"])),
		AppleStoredIsoCountryCode:    postalAddressField("_ISOCountryCode"),
		AppleStoredCountryName:       postalAddressField("_country"),
		AppleStoredStateName:         postalAddressField("_state"),
		AppleStoredCountyName:        postalAddressField("_subAdministrativeArea"),
		AppleStoredTownName:          postalAddressField("_city"),
		AppleStoredNeighbourhoodName: postalAddressField("_subLocality"),
		AppleStoredStreet:            postalAddressField("_street"),
		AppleSaysHome:                appleSaysHome,
	}
	if appleStoredPlace.AppleStoredIsoCountryCode == "" {
		appleStoredPlace.AppleStoredIsoCountryCode = keyedArchive.text(locationInfo["countryCode"])
	}
	mapItem := keyedArchive.dictionary(locationInfo["mapItem"])
	for _, placeInfoReference := range keyedArchive.array(mapItem["sortedPlaceInfos"]) {
		placeInfo := keyedArchive.dictionary(placeInfoReference)
		areaName := strings.TrimSpace(keyedArchive.text(placeInfo["name"]))
		if areaName == "" {
			continue
		}
		placeArea := &photo.AppleStoredPlaceArea{AreaName: areaName}
		if areaSquareMetres, ok := keyedArchive.number(placeInfo["area"]); ok && areaSquareMetres > 0 {
			placeArea.AppleAreaSquareMetres = &areaSquareMetres
		}
		if placeType, ok := keyedArchive.number(placeInfo["placeType"]); ok {
			placeArea.ApplePlaceType = int32(placeType)
		}
		appleStoredPlace.AppleStoredPlaceAreasInSourceOrder = append(appleStoredPlace.AppleStoredPlaceAreasInSourceOrder, placeArea)
		// Select the first source entry of each kind without reordering areas.
		switch placeArea.ApplePlaceType {
		case applePlaceTypePointOfInterest:
			setWhenEmpty(&appleStoredPlace.AppleStoredPointOfInterestName, areaName)
		case applePlaceTypeSeaOrOcean, applePlaceTypeInlandWater:
			setWhenEmpty(&appleStoredPlace.AppleStoredWaterName, areaName)
		case applePlaceTypeIsland:
			setWhenEmpty(&appleStoredPlace.AppleStoredIslandName, areaName)
		}
	}
	return appleStoredPlace, nil
}

func setWhenEmpty(field *string, text string) {
	if *field == "" {
		*field = text
	}
}
