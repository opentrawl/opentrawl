// Package knownplacesfile reads the known-places file that a person or their
// assistant keeps. It reads strictly: a file that does not match
// KnownPlacesFile fails, naming the line, and nothing is mapped or guessed.
package knownplacesfile

import (
	"fmt"
	"os"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/encoding/prototext"
)

func ReadKnownPlacesFile(knownPlacesFilePath string) (*knownplaces.KnownPlacesFile, error) {
	knownPlacesFileText, err := os.ReadFile(knownPlacesFilePath)
	if err != nil {
		// Not wrapped: a missing known-places file must not read as a missing
		// archive.
		return nil, fmt.Errorf("read the known-places file: %v", err)
	}
	knownPlacesFile := &knownplaces.KnownPlacesFile{}
	if err := prototext.Unmarshal(knownPlacesFileText, knownPlacesFile); err != nil {
		return nil, fmt.Errorf("the known-places file %s is not a trawl.photos.knownplaces.KnownPlacesFile: %w", knownPlacesFilePath, err)
	}
	for knownPlaceIndex, knownPlace := range knownPlacesFile.GetKnownPlaces() {
		if err := checkKnownPlace(knownPlace); err != nil {
			return nil, fmt.Errorf("the known-places file %s: known place %d (%q): %w",
				knownPlacesFilePath, knownPlaceIndex+1, knownPlace.GetKnownPlaceName(), err)
		}
	}
	return knownPlacesFile, nil
}

func checkKnownPlace(knownPlace *knownplaces.KnownPlace) error {
	switch knownPlace.GetKnownPlaceKind() {
	case knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_HOME,
		knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_WORK,
		knownplaces.KnownPlaceKind_KNOWN_PLACE_KIND_OTHER:
	default:
		return fmt.Errorf("known_place_kind must be KNOWN_PLACE_KIND_HOME, KNOWN_PLACE_KIND_WORK or KNOWN_PLACE_KIND_OTHER, not %v", knownPlace.GetKnownPlaceKind())
	}
	if knownPlace.GetKnownPlaceName() == "" {
		return fmt.Errorf("known_place_name is missing")
	}
	if knownPlace.KnownPlaceLatitude == nil || knownPlace.KnownPlaceLongitude == nil {
		return fmt.Errorf("known_place_latitude and known_place_longitude are both needed")
	}
	if latitude := knownPlace.GetKnownPlaceLatitude(); latitude < -90 || latitude > 90 {
		return fmt.Errorf("known_place_latitude %v is not between -90 and 90", latitude)
	}
	if longitude := knownPlace.GetKnownPlaceLongitude(); longitude < -180 || longitude > 180 {
		return fmt.Errorf("known_place_longitude %v is not between -180 and 180", longitude)
	}
	if knownPlace.GetKnownPlaceRadiusMetres() <= 0 {
		return fmt.Errorf("known_place_radius_metres must be more than 0")
	}
	validFromDate, err := calendarDateWhenSet(knownPlace.GetKnownPlaceValidFromDate())
	if err != nil {
		return fmt.Errorf("known_place_valid_from_date: %w", err)
	}
	validUntilDate, err := calendarDateWhenSet(knownPlace.GetKnownPlaceValidUntilDate())
	if err != nil {
		return fmt.Errorf("known_place_valid_until_date: %w", err)
	}
	if !validFromDate.IsZero() && !validUntilDate.IsZero() && validUntilDate.Before(validFromDate) {
		return fmt.Errorf("known_place_valid_until_date is before known_place_valid_from_date")
	}
	return nil
}

// calendarDateWhenSet returns the zero time for an unset date.
func calendarDateWhenSet(calendarDate *presentation.CalendarDate) (time.Time, error) {
	if calendarDate == nil {
		return time.Time{}, nil
	}
	year, month, day := int(calendarDate.GetCalendarYear()), time.Month(calendarDate.GetCalendarMonthNumber()), int(calendarDate.GetCalendarDayOfMonth())
	date := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	if date.Year() != year || date.Month() != month || date.Day() != day {
		return time.Time{}, fmt.Errorf("%04d-%02d-%02d is not a date", year, month, day)
	}
	return date, nil
}
