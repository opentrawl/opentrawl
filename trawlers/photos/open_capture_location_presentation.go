package photos

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	photosopen "github.com/opentrawl/opentrawl/trawlers/photos/proto/trawl/photos/open"
)

//go:embed open_capture_location_evidence.txt.tmpl
var openCaptureLocationEvidenceTemplateText string

var openCaptureLocationEvidenceTemplate, openCaptureLocationEvidenceTemplateError = template.New("capture-location-evidence").Funcs(template.FuncMap{
	"addressHierarchyParts": captureLocationAddressHierarchyParts,
	"join":                  strings.Join,
	"knownPlaceKind":        captureLocationKnownPlaceKind,
	"operationState":        captureLocationOperationState,
	"providerName":          captureLocationProviderName,
}).Parse(openCaptureLocationEvidenceTemplateText)

func formatPresentationCaptureLocationEvidence(evidence *photosopen.OpenedPhotoCaptureLocationEvidence) (string, error) {
	if evidence == nil {
		return "", nil
	}
	if openCaptureLocationEvidenceTemplateError != nil {
		return "", fmt.Errorf("parse capture location evidence template: %w", openCaptureLocationEvidenceTemplateError)
	}
	var rendered bytes.Buffer
	if err := openCaptureLocationEvidenceTemplate.ExecuteTemplate(&rendered, "capture-location-evidence", evidence); err != nil {
		return "", fmt.Errorf("render capture location evidence: %w", err)
	}
	return strings.TrimSpace(rendered.String()), nil
}

func captureLocationAddressHierarchyParts(address *locationwire.AddressHierarchy) []string {
	if address == nil {
		return nil
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
	appendDistinct(strings.Join(compactPresentationText([]string{address.GetStreet(), address.GetHouseNumber()}), " "))
	appendDistinct(address.GetPostcode())
	if len(parts) == 0 {
		appendDistinct(address.GetFormatted())
	}
	return parts
}

func compactPresentationText(values []string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}

func captureLocationKnownPlaceKind(kind locationwire.ConfiguredKnownPlaceKind) string {
	switch kind {
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_HOME:
		return "home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_FORMER_HOME:
		return "former home"
	case locationwire.ConfiguredKnownPlaceKind_CONFIGURED_KNOWN_PLACE_KIND_WORK:
		return "work"
	default:
		return "unknown type"
	}
}

func captureLocationProviderName(provider locationwire.LocationEvidenceProvider) string {
	switch provider {
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_REVERSE_GEOCODING:
		return "Apple reverse geocoding"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_APPLE_NEARBY_PLACES:
		return "Apple nearby places"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_REVERSE_GEOCODING:
		return "Geoapify reverse geocoding"
	case locationwire.LocationEvidenceProvider_LOCATION_EVIDENCE_PROVIDER_GEOAPIFY_PLACES:
		return "Geoapify nearby places"
	default:
		return "Unknown provider"
	}
}

func captureLocationOperationState(state locationwire.OperationState) string {
	switch state {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED:
		return "succeeded"
	case locationwire.OperationState_OPERATION_STATE_FAILED:
		return "failed"
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		return "not needed because a saved place matched"
	case locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		return "completed with no result"
	case locationwire.OperationState_OPERATION_STATE_REQUEST_RETAINED:
		return "request retained"
	case locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED:
		return "request started"
	case locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED:
		return "response retained"
	default:
		return "unknown state"
	}
}
