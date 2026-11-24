package render

import (
	"io"
	"math"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func writePhotoNearbyPlacesInspection(writer io.Writer, response *photo.PhotoInspectionResponse, link string) error {
	nextCommands := []NextCommand{nextCommandOpeningRecord(link)}
	if response.GetPhotoRecord().GetAppleLibraryPhotoFacts().GetCameraLocation() == nil {
		if err := writeLine(writer, "Nearby places: this photo has no camera position, so no places are looked up for it."); err != nil {
			return err
		}
		return writeNextCommands(writer, nextCommands)
	}
	switch response.GetNearbyPlacesLookupOutcome() {
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_UNSPECIFIED:
		if err := writeLine(writer, "Nearby places: the step has not run for this photo."); err != nil {
			return err
		}
		requiredStepCommandLines := trawlCommandLinesWrappedBetweenArguments("Required step: ", wrappedTrawlCommandContinuationIndent,
			quotedTrawlCommandArguments(writer, []string{"photos", "run", "look_up_places_near_spot", "--photo", link}), OutputWidth(writer))
		if err := writeLines(writer, requiredStepCommandLines); err != nil {
			return err
		}
		return writeNextCommands(writer, nextCommands)
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED:
		if err := writeLines(writer, Wrap("Nearby places: the lookup failed. "+response.GetNearbyPlacesLookupFailureMessage(), OutputWidth(writer))); err != nil {
			return err
		}
		return writeNextCommands(writer, nextCommands)
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_NO_PLACES:
		if err := writeLine(writer, "Nearby places: OpenStreetMap has no named places near this photo."); err != nil {
			return err
		}
		return writeNextCommands(writer, nextCommands)
	case photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_PLACES_FOUND:
	}
	rows := make([][]string, 0, len(response.GetNearbyPlacesFromCameraNearestFirst()))
	for _, placeFromCamera := range response.GetNearbyPlacesFromCameraNearestFirst() {
		rows = append(rows, []string{
			placeFromCamera.GetNearbyPlace().GetPlaceName(),
			placeFromCamera.GetNearbyPlace().GetPlaceKind(),
			photoDistanceMetresForHumanOutput(placeFromCamera.GetDistanceFromCameraMetres()),
			photoCompassDirectionForHumanOutput(placeFromCamera.GetDirectionFromCameraDegrees()),
		})
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, []TableColumn{
		{Header: "nearby place", Wrap: true, MaximumWrappedLines: 2},
		{Header: "kind", Wrap: true, MaximumWrappedLines: 2},
		{Header: "distance", AlignRight: true},
		{Header: "direction"},
	}, rows, 0); err != nil {
		return err
	}
	return writeNextCommands(writer, nextCommands)
}

var compassPointNamesClockwiseFromNorth = []string{
	"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW",
}

func photoCompassDirectionForHumanOutput(degreesFromTrueNorth float64) string {
	roundedDegrees := math.Mod(math.Round(degreesFromTrueNorth), 360)
	compassPointIndex := int(math.Round(roundedDegrees/22.5)) % len(compassPointNamesClockwiseFromNorth)
	return compassPointNamesClockwiseFromNorth[compassPointIndex] + " " + photoRoundedDecimalForHumanOutput(roundedDegrees, 0) + "°"
}
