// Package locationbriefing renders composed location evidence for humans.
// It does not decide what a photo depicts.
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
	"addressParts": addressHierarchyParts,
	"enum":         humanReadableEnumValue,
	"join":         strings.Join,
	"timestamp":    humanTimestamp,
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

func humanTimestamp(timestamp *timestamppb.Timestamp) string {
	if timestamp == nil || !timestamp.IsValid() {
		return ""
	}
	return timestamp.AsTime().Format(time.RFC3339)
}

func humanReadableEnumValue(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	value = strings.ToLower(strings.ReplaceAll(value, "_", " "))
	if value == "unspecified" {
		return ""
	}
	return value
}

func addressHierarchyParts(address *locationwire.AddressHierarchy) []string {
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
	if len(parts) == 0 {
		appendDistinct(address.GetFormatted())
	}
	return parts
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
