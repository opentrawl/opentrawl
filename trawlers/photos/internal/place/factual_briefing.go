package place

import (
	"fmt"
	"strings"
)

// ComposeFactualLocationBriefing produces model context from source and
// provider facts. It never names a photographed subject or chooses a venue.
func ComposeFactualLocationBriefing(knownCapturePlaceMatch *KnownCapturePlaceMatch, evidence []FactualLocationEvidence) string {
	lines := []string{"Capture location context. The camera coordinate and nearby places do not identify the photographed subject."}
	if knownCapturePlaceMatch != nil {
		knownContext := strings.TrimSpace(knownCapturePlaceMatch.Relationship)
		if displayName := strings.TrimSpace(knownCapturePlaceMatch.DisplayName); displayName != "" {
			knownContext += ": " + displayName
		}
		if knownContext != "" {
			lines = append(lines, "Known capture context: "+knownContext+".")
		}
	}
	for _, record := range evidence {
		switch record.Operation {
		case AppleReverseLocationOperation:
			appendFactualAddressLine(&lines, "Apple reverse-geocoded capture address", record.Address)
		case GeoapifyReverseLocationOperation:
			appendFactualAddressLine(&lines, "Geoapify reverse-geocoded capture address", record.Address)
		case AppleNearbyLocationOperation:
			appendNearbyProviderCandidates(&lines, record)
		}
	}
	return strings.Join(lines, "\n")
}

func appendFactualAddressLine(lines *[]string, label string, address *Address) {
	if text := FormatAddress(address); text != "" {
		*lines = append(*lines, label+": "+text+".")
	}
}

func appendNearbyProviderCandidates(lines *[]string, record FactualLocationEvidence) {
	if len(record.Candidates) == 0 {
		*lines = append(*lines, "Apple nearby places: no named places returned.")
		return
	}
	*lines = append(*lines, "Apple nearby places, in provider order:")
	seenProviderPlaceIdentities := map[string]struct{}{}
	for _, candidate := range record.Candidates {
		providerPlaceIdentity := strings.TrimSpace(candidate.ProviderPlaceIdentity)
		if providerPlaceIdentity != "" {
			if _, seen := seenProviderPlaceIdentities[providerPlaceIdentity]; seen {
				continue
			}
			seenProviderPlaceIdentities[providerPlaceIdentity] = struct{}{}
		}
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			continue
		}
		details := append([]string{}, candidate.Categories...)
		if candidate.DistanceMeters > 0 {
			details = append(details, fmt.Sprintf("%.0f m from capture coordinate", candidate.DistanceMeters))
		}
		if len(details) == 0 {
			*lines = append(*lines, "- "+name)
			continue
		}
		*lines = append(*lines, "- "+name+" ("+strings.Join(details, ", ")+")")
	}
}
