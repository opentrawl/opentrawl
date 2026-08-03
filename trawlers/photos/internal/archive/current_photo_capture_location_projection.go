package archive

import (
	"fmt"
	"strings"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

type currentPhotoCaptureLocationProjection struct {
	CaptureLocation *OpenPlace
	KnownPlace      *OpenKnownPlace
	SearchText      string
}

func currentPhotoCaptureLocationProjectionFromEvidence(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) currentPhotoCaptureLocationProjection {
	if outcome == nil || !composedPhotoLocationEvidenceIsCurrent(outcome) {
		return currentPhotoCaptureLocationProjection{}
	}
	briefing := outcome.GetBriefing()
	if briefing == nil {
		return currentPhotoCaptureLocationProjection{}
	}
	displayLines := make([]string, 0, len(briefing.GetKnownPlaceMatches())+1)
	var primaryKnownPlace *OpenKnownPlace
	for _, match := range briefing.GetKnownPlaceMatches() {
		if match == nil || strings.TrimSpace(match.GetDisplayName()) == "" {
			continue
		}
		knownPlace := openKnownPlaceFromCurrentEvidence(match)
		if primaryKnownPlace == nil {
			primaryKnownPlace = knownPlace
		}
		displayLines = append(displayLines, "Known place: "+formatCurrentPhotoKnownPlace(knownPlace))
	}
	if hierarchy := outsideToInsideAddressHierarchy(briefing.GetAppleCameraLocation()); hierarchy != "" {
		displayLines = append(displayLines, "Apple: "+hierarchy)
	}
	if len(displayLines) == 0 {
		return currentPhotoCaptureLocationProjection{KnownPlace: primaryKnownPlace}
	}
	displayText := strings.Join(displayLines, "\n")
	return currentPhotoCaptureLocationProjection{
		CaptureLocation: &OpenPlace{Name: displayText},
		KnownPlace:      primaryKnownPlace,
		SearchText:      displayText,
	}
}

func openKnownPlaceFromCurrentEvidence(match *locationwire.ConfiguredKnownPlaceMatch) *OpenKnownPlace {
	return &OpenKnownPlace{
		Kind:  currentPhotoKnownPlaceKind(match.GetKind()),
		Name:  strings.TrimSpace(match.GetDisplayName()),
		After: match.GetRelationshipAtCapture() == locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_VISITED_AFTER_KNOWN_PERIOD,
	}
}

func formatCurrentPhotoKnownPlace(knownPlace *OpenKnownPlace) string {
	if knownPlace == nil {
		return ""
	}
	detail := strings.TrimSpace(knownPlace.Kind)
	if knownPlace.After {
		detail = strings.TrimSpace(strings.Join(compactOpenText([]string{detail, "photo captured after the known period"}), ", "))
	}
	if detail == "" {
		return strings.TrimSpace(knownPlace.Name)
	}
	return fmt.Sprintf("%s (%s)", strings.TrimSpace(knownPlace.Name), detail)
}

func currentPhotoKnownPlaceKind(kind locationwire.ConfiguredKnownPlaceKind) string {
	switch kind {
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME:
		return "home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME:
		return "former home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK:
		return "work"
	default:
		return ""
	}
}

func outsideToInsideAddressHierarchy(address *locationwire.AddressHierarchy) string {
	if address == nil {
		return ""
	}
	parts := make([]string, 0, 10+len(address.GetAreas()))
	seen := make(map[string]struct{}, 10+len(address.GetAreas()))
	appendDistinct := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, value)
	}
	appendDistinct(address.GetCountry())
	appendDistinct(address.GetRegion())
	appendDistinct(address.GetCounty())
	appendDistinct(address.GetCity())
	appendDistinct(address.GetDistrict())
	appendDistinct(address.GetNeighbourhood())
	for _, area := range address.GetAreas() {
		if area != nil {
			appendDistinct(area.GetName())
		}
	}
	appendDistinct(strings.TrimSpace(strings.Join(compactOpenText([]string{address.GetStreet(), address.GetHouseNumber()}), " ")))
	appendDistinct(address.GetPostcode())
	if len(parts) != 0 {
		return strings.Join(parts, " → ")
	}
	return strings.TrimSpace(address.GetFormatted())
}
