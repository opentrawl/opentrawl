// Package locationbriefing renders retained typed location evidence for humans
// and future model prompts. It does not decide what a photo depicts.
package locationbriefing

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"text/template"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed location_briefing.txt.tmpl
var locationBriefingTemplateText string

var locationBriefingTemplate, locationBriefingTemplateParseError = template.New("photo-location-briefing").Funcs(template.FuncMap{
	"attribution":             humanLocationEvidenceAttribution,
	"candidateName":           candidateHumanName,
	"candidateReference":      humanCandidateReference,
	"categories":              humanProviderCategories,
	"compactAddressHierarchy": compactAddressHierarchy,
	"failure":                 humanOperationFailure,
	"hasProviderCandidates":   hasProviderCandidates,
	"knownPlaceKind":          humanKnownPlaceKind,
	"knownPlaceRelationship":  humanKnownPlaceRelationship,
	"operationState":          humanOperationState,
	"providerEvidenceUse":     humanProviderEvidenceUse,
	"providerName":            humanLocationEvidenceProvider,
	"timestamp":               humanTimestamp,
}).Parse(locationBriefingTemplateText)

func Render(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) (string, error) {
	if locationBriefingTemplateParseError != nil {
		return "", fmt.Errorf("parse photo location briefing template: %w", locationBriefingTemplateParseError)
	}
	briefing := &locationwire.PhotoLocationBriefing{}
	if outcome != nil {
		if outcome.GetState() != locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
			return "", fmt.Errorf("composed photo location evidence is not successful: %s", outcome.GetState())
		}
		if outcome.GetBriefing() == nil {
			return "", errors.New("composed photo location evidence has no briefing")
		}
		briefing = outcome.GetBriefing()
	}

	var rendered bytes.Buffer
	if err := locationBriefingTemplate.ExecuteTemplate(&rendered, "photo-location-briefing", briefing); err != nil {
		return "", fmt.Errorf("render photo location briefing: %w", err)
	}
	return strings.TrimSpace(rendered.String()), nil
}

func humanCandidateReference(reference *locationwire.PhotoLocationCandidateReference) string {
	if reference == nil {
		return "Provider candidate"
	}
	return fmt.Sprintf("%s candidate %d", humanLocationEvidenceProvider(reference.GetProvider()), reference.GetZeroBasedProviderPosition()+1)
}

func hasProviderCandidates(providerEvidence []*locationwire.PhotoLocationProviderEvidence) bool {
	for _, evidence := range providerEvidence {
		if len(evidence.GetCandidatesInProviderOrder()) > 0 {
			return true
		}
	}
	return false
}

func humanTimestamp(timestamp *timestamppb.Timestamp) string {
	if timestamp == nil || !timestamp.IsValid() {
		return ""
	}
	return timestamp.AsTime().Format(time.RFC3339)
}

func humanLocationEvidenceProvider(provider locationwire.LocationEvidenceProvider) string {
	switch provider {
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_CORE_LOCATION:
		return "Apple Core Location"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_MAP_KIT:
		return "Apple MapKit"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES:
		return "Geoapify Places"
	default:
		return "Unknown provider"
	}
}

func humanOperationState(state locationwire.OperationState) string {
	switch state {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED:
		return "Succeeded"
	case locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		return "No result"
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		return "Not sent because a configured known place matched"
	case locationwire.OperationState_OPERATION_STATE_FAILED:
		return "Failed"
	default:
		return "Incomplete"
	}
}

func humanOperationFailure(failure *locationwire.OperationFailure) string {
	if failure == nil {
		return ""
	}
	parts := []string{humanReadableEnumValue(failure.GetClass().String(), "OPERATION_FAILURE_CLASS_")}
	if detail := strings.TrimSpace(failure.GetDetail()); detail != "" {
		parts = append(parts, detail)
	}
	if retryNotBefore := humanTimestamp(failure.GetRetryNotBefore()); retryNotBefore != "" {
		parts = append(parts, "retry not before "+retryNotBefore)
	}
	return strings.Join(compactStrings(parts), "; ")
}

func humanReadableEnumValue(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	value = strings.ToLower(strings.ReplaceAll(value, "_", " "))
	if value == "unspecified" {
		return ""
	}
	return value
}

func humanProviderEvidenceUse(evidenceUse locationwire.ProviderEvidenceUse) string {
	switch evidenceUse {
	case locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_ACQUIRED:
		return "Acquired for this provider request"
	case locationwire.ProviderEvidenceUse_PROVIDER_EVIDENCE_USE_REUSED:
		return "Reused from the same provider request"
	default:
		return ""
	}
}

func humanLocationEvidenceAttribution(attribution *locationwire.LocationEvidenceAttribution) string {
	if attribution == nil {
		return ""
	}
	parts := compactStrings([]string{
		attribution.GetProviderName(),
		labelledValue("data source", attribution.GetDataSourceName()),
		attribution.GetDataSourceCredit(),
		labelledValue("license", attribution.GetDataSourceLicense()),
		attribution.GetDataSourceUrl(),
	})
	return strings.Join(parts, "; ")
}

func humanProviderCategories(categories []string) string {
	return strings.Join(compactStrings(categories), ", ")
}

func candidateHumanName(candidate *locationwire.PlaceCandidate) string {
	if humanName := candidateCanonicalHumanName(candidate); humanName != "" {
		return humanName
	}
	return "Unnamed provider candidate"
}

func candidateCanonicalHumanName(candidate *locationwire.PlaceCandidate) string {
	if candidate == nil {
		return ""
	}
	if name := strings.TrimSpace(candidate.GetName()); name != "" {
		return name
	}
	if candidate.GetAddress() != nil {
		if name := strings.TrimSpace(candidate.GetAddress().GetName()); name != "" {
			return name
		}
		if formatted := strings.TrimSpace(candidate.GetAddress().GetFormatted()); formatted != "" {
			return formatted
		}
	}
	return ""
}

func compactAddressHierarchy(address *locationwire.AddressHierarchy) string {
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
		if _, alreadyPresent := seen[key]; alreadyPresent {
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
	appendDistinct(strings.Join(compactStrings([]string{address.GetStreet(), address.GetHouseNumber()}), " "))
	appendDistinct(address.GetPostcode())
	if len(parts) > 0 {
		return strings.Join(parts, " → ")
	}
	return strings.TrimSpace(address.GetFormatted())
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
		return "known place"
	}
}

func humanKnownPlaceRelationship(relationship locationwire.ConfiguredKnownPlaceRelationshipAtCapture) string {
	switch relationship {
	case locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_ACTIVE_DURING_KNOWN_PERIOD:
		return "active at capture time"
	case locationwire.ConfiguredKnownPlaceRelationshipAtCapture_CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_VISITED_AFTER_KNOWN_PERIOD:
		return "visited after the known period"
	default:
		return "capture-time relationship not known"
	}
}

func labelledValue(label, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return label + ": " + value
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
