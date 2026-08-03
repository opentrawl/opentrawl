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
	"addressParts":          addressHierarchyParts,
	"candidateName":         candidateCanonicalHumanName,
	"categories":            humanProviderCategories,
	"enum":                  humanReadableEnumValue,
	"hasProviderCandidates": hasProviderCandidates,
	"join":                  strings.Join,
	"timestamp":             humanTimestamp,
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

func humanReadableEnumValue(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	value = strings.ToLower(strings.ReplaceAll(value, "_", " "))
	if value == "unspecified" {
		return ""
	}
	return value
}

func humanProviderCategories(categories []string) string {
	conciseCategories := make([]string, 0, len(categories))
	seen := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		category = strings.TrimSpace(category)
		if finalSeparator := strings.LastIndexByte(category, '.'); finalSeparator >= 0 {
			category = category[finalSeparator+1:]
		}
		category = humanProviderCategory(category)
		if category == "" {
			continue
		}
		comparisonKey := strings.ToLower(category)
		if _, alreadyIncluded := seen[comparisonKey]; alreadyIncluded {
			continue
		}
		seen[comparisonKey] = struct{}{}
		conciseCategories = append(conciseCategories, category)
	}
	return strings.Join(conciseCategories, ", ")
}

func humanProviderCategory(category string) string {
	category = strings.TrimPrefix(category, "MKPOICategory")
	category = strings.ReplaceAll(category, "_", " ")
	runes := []rune(category)
	var human strings.Builder
	for index, current := range runes {
		if index > 0 && current >= 'A' && current <= 'Z' {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' || previous >= 'A' && previous <= 'Z' && nextIsLower {
				human.WriteByte(' ')
			}
		}
		human.WriteRune(current)
	}
	return strings.ToLower(strings.TrimSpace(human.String()))
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
	}
	return ""
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
