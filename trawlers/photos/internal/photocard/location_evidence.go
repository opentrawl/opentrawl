package photocard

import (
	"errors"
	"fmt"
	"strings"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
)

type HumanReadableLocationEvidence struct {
	Text               string
	SuppliedCandidates []SuppliedPhotographedPlaceCandidate
}

const maximumAppleNearbyPlaceCandidatesInPhotoCardBriefing = 10

func BuildHumanReadableLocationEvidence(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) (HumanReadableLocationEvidence, error) {
	if outcome == nil {
		return HumanReadableLocationEvidence{Text: "Location evidence: capture location is absent; no known place, provider hierarchy or nearby candidate is supplied."}, nil
	}
	if outcome.State != locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		return HumanReadableLocationEvidence{}, fmt.Errorf("composed photo location evidence is not successful: %s", outcome.State)
	}

	var rendered strings.Builder
	rendered.WriteString("Location evidence:\n")
	candidates := make([]SuppliedPhotographedPlaceCandidate, 0, len(outcome.KnownPlaceMatches)+len(outcome.AppleNearbyCandidates)+len(outcome.GeoapifyPhotographedPlaceCandidates))

	if len(outcome.KnownPlaceMatches) != 0 {
		rendered.WriteString("\nKnown places near the camera:\n")
		for index, match := range outcome.KnownPlaceMatches {
			identifier := fmt.Sprintf("known-place-%d", index+1)
			humanName := strings.TrimSpace(match.DisplayName)
			if humanName == "" {
				return HumanReadableLocationEvidence{}, errors.New("known-place match has no human-readable name")
			}
			candidates = append(candidates, SuppliedPhotographedPlaceCandidate{Identifier: identifier, HumanName: humanName})
			fmt.Fprintf(&rendered, "- Exact supplied_candidate_identifier %q; display text %q; kind %s; %.0f metres from camera; relationship at capture %s.\n", identifier, humanName, humanKnownPlaceKind(match.Kind), match.DistanceMeters, humanKnownPlaceRelationship(match.RelationshipAtCapture))
		}
	}
	renderAddressHierarchy(&rendered, "Apple camera-location hierarchy", outcome.AppleAddress)

	if outcome.NearbySuppressedForKnownPlace {
		rendered.WriteString("\nNearby points of interest were suppressed because a configured known place matched.\n")
	} else {
		appleNearbyCandidates := outcome.AppleNearbyCandidates
		if len(appleNearbyCandidates) > maximumAppleNearbyPlaceCandidatesInPhotoCardBriefing {
			appleNearbyCandidates = appleNearbyCandidates[:maximumAppleNearbyPlaceCandidatesInPhotoCardBriefing]
		}
		appendNearbyCandidates(&rendered, "Apple nearby places", "apple-nearby", appleNearbyCandidates, &candidates)
		appendNearbyCandidates(&rendered, "Geoapify potential photographed places", "geoapify-place", outcome.GeoapifyPhotographedPlaceCandidates, &candidates)
	}
	if caution := strings.TrimSpace(outcome.Caution); caution != "" {
		fmt.Fprintf(&rendered, "\nCaution: %s\n", caution)
	}
	return HumanReadableLocationEvidence{Text: strings.TrimSpace(rendered.String()), SuppliedCandidates: candidates}, nil
}

func renderAddressHierarchy(rendered *strings.Builder, heading string, address *locationwire.AddressHierarchy) {
	if address == nil {
		return
	}
	parts := compactStrings([]string{
		labelledValue("country", address.Country),
		labelledValue("region", address.Region),
		labelledValue("county", address.County),
		labelledValue("city", address.City),
		labelledValue("district", address.District),
		labelledValue("neighbourhood", address.Neighbourhood),
	})
	for _, area := range address.Areas {
		if area != nil && strings.TrimSpace(area.Name) != "" {
			parts = append(parts, labelledValue(humanNamedAreaKind(area.Kind), area.Name))
		}
	}
	streetAddress := strings.TrimSpace(strings.Join(compactStrings([]string{address.Street, address.HouseNumber}), " "))
	if streetAddress != "" {
		parts = append(parts, labelledValue("street address", streetAddress))
	}
	if postcode := strings.TrimSpace(address.Postcode); postcode != "" {
		parts = append(parts, labelledValue("postcode", postcode))
	}
	if len(parts) == 0 && strings.TrimSpace(address.Formatted) == "" {
		return
	}
	fmt.Fprintf(rendered, "\n%s (outside to inside):\n", heading)
	for _, part := range parts {
		fmt.Fprintf(rendered, "- %s\n", part)
	}
	if formatted := strings.TrimSpace(address.Formatted); formatted != "" {
		fmt.Fprintf(rendered, "- provider-formatted address: %s\n", formatted)
	}
}

func appendNearbyCandidates(rendered *strings.Builder, heading, identifierPrefix string, nearby []*locationwire.PlaceCandidate, candidates *[]SuppliedPhotographedPlaceCandidate) {
	if len(nearby) == 0 {
		return
	}
	renderedHeading := false
	for index, candidate := range nearby {
		if candidate == nil {
			continue
		}
		humanName := candidateHumanName(candidate)
		if humanName == "" {
			continue
		}
		if !renderedHeading {
			fmt.Fprintf(rendered, "\n%s (near the camera, not necessarily depicted):\n", heading)
			renderedHeading = true
		}
		identifier := fmt.Sprintf("%s-%d", identifierPrefix, index+1)
		*candidates = append(*candidates, SuppliedPhotographedPlaceCandidate{Identifier: identifier, HumanName: humanName})
		fmt.Fprintf(rendered, "- Exact supplied_candidate_identifier %q; display text %q; %.0f metres from camera", identifier, humanName, candidate.DistanceMeters)
		if len(candidate.Categories) != 0 {
			fmt.Fprintf(rendered, "; categories %s", strings.Join(candidate.Categories, ", "))
		}
		if candidateAddress := compactAddress(candidate.Address); candidateAddress != "" {
			fmt.Fprintf(rendered, "; address %s", candidateAddress)
		}
		rendered.WriteString(".\n")
	}
}

func candidateHumanName(candidate *locationwire.PlaceCandidate) string {
	if name := strings.TrimSpace(candidate.Name); name != "" {
		return name
	}
	if candidate.Address == nil {
		return ""
	}
	if name := strings.TrimSpace(candidate.Address.Name); name != "" {
		return name
	}
	return strings.TrimSpace(candidate.Address.Formatted)
}

func compactAddress(address *locationwire.AddressHierarchy) string {
	if address == nil {
		return ""
	}
	if formatted := strings.TrimSpace(address.Formatted); formatted != "" {
		return formatted
	}
	streetAddress := strings.TrimSpace(strings.Join(compactStrings([]string{address.Street, address.HouseNumber}), " "))
	return strings.Join(compactStrings([]string{streetAddress, address.Neighbourhood, address.District, address.City, address.Region, address.Country}), ", ")
}

func labelledValue(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimSpace(label) + ": " + value
}

func compactStrings(values []string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func humanKnownPlaceKind(kind locationwire.ConfiguredKnownPlaceKind) string {
	switch kind {
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME:
		return "home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME:
		return "former home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK:
		return "work"
	default:
		return "unspecified"
	}
}

func humanKnownPlaceRelationship(relationship locationwire.ConfiguredKnownPlaceRelationshipAtCapture) string {
	switch relationship {
	case locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_ACTIVE_DURING_KNOWN_PERIOD:
		return "active during known period"
	case locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_VISITED_AFTER_KNOWN_PERIOD:
		return "visited after known period"
	default:
		return "unspecified"
	}
}

func humanNamedAreaKind(kind locationwire.NamedAreaKind) string {
	switch kind {
	case locationwire.NamedAreaKind_NAMED_AREA_KIND_AREA_OF_INTEREST:
		return "area of interest"
	case locationwire.NamedAreaKind_NAMED_AREA_KIND_SUBURB:
		return "suburb"
	case locationwire.NamedAreaKind_NAMED_AREA_KIND_MUNICIPALITY:
		return "municipality"
	default:
		return "named area"
	}
}
