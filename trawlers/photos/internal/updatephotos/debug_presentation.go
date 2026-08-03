package updatephotos

import (
	"bytes"
	"embed"
	"encoding/hex"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed debug_output.txt.tmpl
var debugOutputTemplateFile embed.FS

var debugOutputTemplate = template.Must(template.New("debug-output").Funcs(template.FuncMap{
	"add":                           func(left, right int) int { return left + right },
	"addressParts":                  debugAddressHierarchyParts,
	"join":                          strings.Join,
	"appleNearbyMethod":             debugAppleNearbyPlaceSearchMethod,
	"appleReverseMethod":            debugAppleReverseGeocodingMethod,
	"evidenceUse":                   debugProviderEvidenceUse,
	"geoapifyReverseResponseFormat": debugGeoapifyReverseGeocodingResponseFormat,
	"knownPlaceKind":                debugKnownPlaceKind,
	"imageMetadataValue":            debugImageMetadataValue,
	"photoKitDelivery":              debugCurrentRenderedStillDeliveryMode,
	"photoKitResize":                debugCurrentRenderedStillResizeMode,
	"photoKitVersion":               debugCurrentRenderedStillPhotoKitVersion,
	"isSelectedResource":            debugIsSelectedOriginalResource,
	"placeName":                     debugPlaceName,
	"relationship":                  debugKnownPlaceRelationship,
	"sha256":                        hex.EncodeToString,
	"state":                         debugOperationState,
	"time":                          debugTimestamp,
	"trim":                          strings.TrimSpace,
}).ParseFS(debugOutputTemplateFile, "debug_output.txt.tmpl"))

type debugReverseGeocodingTemplateData struct {
	Outcome *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome
}

type debugAppleNearbyPlacesTemplateData struct {
	Outcome *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome
}

type debugGeoapifyPlacesTemplateData struct {
	Outcome *locationwire.AcquireGeoapifyPhotographedPlaceCandidateEvidenceOutcome
}

type debugGeoapifyReverseGeocodingTemplateData struct {
	Outcome *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome
}

func renderDebugOutput(templateName string, data any) (string, error) {
	var rendered bytes.Buffer
	if err := debugOutputTemplate.ExecuteTemplate(&rendered, templateName, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(rendered.String()), nil
}

func debugOperationState(state locationwire.OperationState) string {
	return debugEnumName(state.String(), "OPERATION_STATE_")
}

func debugKnownPlaceKind(kind locationwire.ConfiguredKnownPlaceKind) string {
	return debugEnumName(kind.String(), "CONFIGURED_KNOWN_PLACE_KIND_")
}

func debugKnownPlaceRelationship(relationship locationwire.ConfiguredKnownPlaceRelationshipAtCapture) string {
	return debugEnumName(relationship.String(), "CONFIGURED_KNOWN_PLACE_RELATIONSHIP_AT_CAPTURE_")
}

func debugAppleReverseGeocodingMethod(method locationwire.AppleReverseGeocodingMethod) string {
	if method == locationwire.AppleReverseGeocodingMethod_APPLE_REVERSE_GEOCODING_METHOD_MAP_KIT_REVERSE_GEOCODING_REQUEST {
		return "MapKit reverse geocoding"
	}
	return "Legacy Apple request; the acquisition method was not retained"
}

func debugAppleNearbyPlaceSearchMethod(method locationwire.AppleNearbyPlaceSearchMethod) string {
	if method == locationwire.AppleNearbyPlaceSearchMethod_APPLE_NEARBY_PLACE_SEARCH_METHOD_MAP_KIT_LOCAL_SEARCH {
		return "MapKit local search"
	}
	return "Legacy Apple request; the acquisition method was not retained"
}

func debugProviderEvidenceUse(evidenceUse locationwire.ProviderEvidenceUse) string {
	return debugEnumName(evidenceUse.String(), "PROVIDER_EVIDENCE_USE_")
}

func debugGeoapifyReverseGeocodingResponseFormat(responseFormat locationwire.GeoapifyReverseGeocodingResponseFormat) string {
	if responseFormat == locationwire.GeoapifyReverseGeocodingResponseFormat_GEOAPIFY_REVERSE_GEOCODING_RESPONSE_FORMAT_GEOJSON {
		return "GeoJSON"
	}
	return "Unknown response format"
}

func debugEnumName(value, prefix string) string {
	value = strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(value, prefix)), "_", " ")
	if value == "" || value == "unspecified" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func debugTimestamp(value *timestamppb.Timestamp) string {
	if value == nil || !value.IsValid() {
		return ""
	}
	return value.AsTime().Format(time.RFC3339)
}

func debugCurrentRenderedStillPhotoKitVersion(version mediawire.CurrentRenderedStillPhotoKitVersion) string {
	return debugEnumName(version.String(), "CURRENT_RENDERED_STILL_PHOTO_KIT_VERSION_")
}

func debugCurrentRenderedStillDeliveryMode(deliveryMode mediawire.CurrentRenderedStillPhotoKitDeliveryMode) string {
	return debugEnumName(deliveryMode.String(), "CURRENT_RENDERED_STILL_PHOTO_KIT_DELIVERY_MODE_")
}

func debugCurrentRenderedStillResizeMode(resizeMode mediawire.CurrentRenderedStillPhotoKitResizeMode) string {
	return debugEnumName(resizeMode.String(), "CURRENT_RENDERED_STILL_PHOTO_KIT_RESIZE_MODE_")
}

func debugIsSelectedOriginalResource(outcome *mediawire.ImmutableOriginalImageFactsOutcome, providerPosition int32) bool {
	return outcome != nil && outcome.SelectedPhotoKitCandidatePosition != nil && outcome.GetSelectedPhotoKitCandidatePosition() == providerPosition
}

func debugImageMetadataValue(value *mediawire.ImageMetadataValue) string {
	if value == nil {
		return ""
	}
	switch typedValue := value.GetValue().(type) {
	case *mediawire.ImageMetadataValue_Text:
		return strings.TrimSpace(typedValue.Text)
	case *mediawire.ImageMetadataValue_Integer:
		return strconv.FormatInt(typedValue.Integer, 10)
	case *mediawire.ImageMetadataValue_Decimal:
		return strconv.FormatFloat(typedValue.Decimal, 'f', -1, 64)
	case *mediawire.ImageMetadataValue_Boolean:
		return strconv.FormatBool(typedValue.Boolean)
	case *mediawire.ImageMetadataValue_Time:
		return debugTimestamp(typedValue.Time)
	case *mediawire.ImageMetadataValue_TextList:
		return strings.Join(typedValue.TextList.GetValues(), ", ")
	case *mediawire.ImageMetadataValue_IntegerList:
		values := make([]string, 0, len(typedValue.IntegerList.GetValues()))
		for _, integer := range typedValue.IntegerList.GetValues() {
			values = append(values, strconv.FormatInt(integer, 10))
		}
		return strings.Join(values, ", ")
	case *mediawire.ImageMetadataValue_DecimalList:
		values := make([]string, 0, len(typedValue.DecimalList.GetValues()))
		for _, decimal := range typedValue.DecimalList.GetValues() {
			values = append(values, strconv.FormatFloat(decimal, 'f', -1, 64))
		}
		return strings.Join(values, ", ")
	default:
		return ""
	}
}

func debugAddressHierarchyParts(address *locationwire.AddressHierarchy) []string {
	if address == nil {
		return nil
	}
	parts := make([]string, 0, 10+len(address.GetAreas()))
	seen := make(map[string]struct{}, cap(parts))
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
	appendDistinct(strings.Join(debugCompactValues([]string{address.GetHouseNumber(), address.GetStreet()}), " "))
	appendDistinct(address.GetPostcode())
	if len(parts) == 0 {
		appendDistinct(address.GetFormatted())
	}
	return parts
}

func debugCompactValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func debugPlaceName(candidate *locationwire.PlaceCandidate) string {
	if candidate == nil || strings.TrimSpace(candidate.GetName()) == "" {
		return "Unnamed place"
	}
	return strings.TrimSpace(candidate.GetName())
}
