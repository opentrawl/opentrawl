package updatephotos

import (
	"bytes"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"
	"text/template"
	"time"

	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"google.golang.org/protobuf/types/known/timestamppb"
)

//go:embed debug_output.txt.tmpl
var debugOutputTemplateFile embed.FS

var debugOutputTemplate = template.Must(template.New("debug-output").Funcs(template.FuncMap{
	"add":            func(left, right int) int { return left + right },
	"addressParts":   debugAddressHierarchyParts,
	"coordinate":     func(value float64) string { return fmt.Sprintf("%.6f", value) },
	"distance":       func(value float64) string { return fmt.Sprintf("%.0f", value) },
	"join":           strings.Join,
	"knownPlaceKind": debugKnownPlaceKind,
	"placeName":      debugPlaceName,
	"relationship":   debugKnownPlaceRelationship,
	"sha256":         hex.EncodeToString,
	"state":          debugOperationState,
	"time":           debugTimestamp,
	"trim":           strings.TrimSpace,
}).ParseFS(debugOutputTemplateFile, "debug_output.txt.tmpl"))

type debugReverseGeocodingTemplateData struct {
	Exchange *locationwire.ProviderExchange
	Address  *locationwire.AddressHierarchy
}

type debugNearbyPlacesTemplateData struct {
	Exchange   *locationwire.ProviderExchange
	Candidates []*locationwire.PlaceCandidate
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

func debugAddressHierarchyParts(address *locationwire.AddressHierarchy) []string {
	if address == nil {
		return nil
	}
	if formatted := strings.TrimSpace(address.GetFormatted()); formatted != "" {
		return []string{formatted}
	}
	streetAddress := strings.TrimSpace(strings.Join(debugCompactValues([]string{address.GetHouseNumber(), address.GetStreet()}), " "))
	return debugCompactValues([]string{
		address.GetName(), streetAddress, address.GetNeighbourhood(), address.GetDistrict(), address.GetCity(),
		address.GetCounty(), address.GetRegion(), address.GetPostcode(), address.GetCountry(),
	})
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
