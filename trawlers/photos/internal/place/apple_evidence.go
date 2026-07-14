package place

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	appleEvidenceProvider  = "apple"
	appleEvidenceOperation = "place"
)

func captureAppleEvidence(ctx context.Context, opts EvidenceOptions, runner evidenceRunner) evidenceCapture {
	request, requestErr := appleRequestJSON(opts.Input, opts.RadiusMeters)
	if requestErr != nil {
		return stoppedCapture(opts.Input, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", nil, []byte(requestErr.Error()), 0, parsedEvidence{}, requestErr)
	}
	if cached, ok := cachedCapture(opts.CacheDir, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", request, opts.Input, parseAppleEvidence); ok {
		return cached
	}
	boundary := runner.callApple(ctx, opts.Input, opts.RadiusMeters)
	if !bytes.Equal(boundary.Request, request) {
		err := errors.New("Apple boundary request does not match the selected coordinate request")
		return stoppedCapture(opts.Input, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", request, boundary.Response, 0, parsedEvidence{}, err)
	}
	if boundary.Err != nil {
		return stoppedCapture(opts.Input, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", request, boundary.Response, 0, parsedEvidence{}, boundary.Err)
	}
	parsed, err := parseAppleEvidence(boundary.Response, 0, opts.Input)
	if err != nil {
		return stoppedCapture(opts.Input, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", request, boundary.Response, 0, parsed, err)
	}
	return completeCapture(opts.Input, appleEvidenceProvider, appleEvidenceOperation, opts.CoordinateVariant, "", request, boundary.Response, 0, parsed)
}

func parseAppleEvidence(raw []byte, _ int, input Input) (parsedEvidence, error) {
	if len(raw) == 0 {
		return parsedEvidence{}, errors.New("Apple returned an empty response")
	}
	var result appleEvidenceResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return parsedEvidence{}, fmt.Errorf("parse raw Apple response: %w", err)
	}
	parsed := parsedEvidence{}
	for index, item := range result.ReverseItems {
		candidate := appleEvidenceCandidate(index, item, input)
		parsed.candidates = append(parsed.candidates, candidate)
		if parsed.address == nil && candidate.Address != nil {
			parsed.address = candidate.Address
		}
	}
	for index, item := range result.NearbyItems {
		parsed.candidates = append(parsed.candidates, appleEvidenceCandidate(index, item, input))
	}
	if len(result.ReverseItems) == 0 || parsed.address == nil {
		return parsed, ErrProviderNoResult
	}
	if len(result.NearbyItems) == 0 {
		return parsed, ErrProviderNoResult
	}
	return parsed, nil
}

type appleEvidenceResponse struct {
	ReverseItems []appleEvidenceItem `json:"reverse_items"`
	NearbyItems  []appleEvidenceItem `json:"nearby_items"`
}

type appleEvidenceItem struct {
	Name       string      `json:"name,omitempty"`
	Category   string      `json:"category,omitempty"`
	Coordinate *Coordinate `json:"coordinate,omitempty"`
	DistanceM  float64     `json:"distance_m,omitempty"`
	Address    *Address    `json:"address,omitempty"`
	Source     string      `json:"source,omitempty"`
}

func appleEvidenceCandidate(index int, item appleEvidenceItem, input Input) EvidenceCandidate {
	categories := []string{}
	if category := strings.TrimSpace(item.Category); category != "" {
		categories = append(categories, category)
	}
	distance := item.DistanceM
	if distance <= 0 && item.Coordinate != nil {
		distance = metersBetween(input.Location, *item.Coordinate)
	}
	return EvidenceCandidate{
		ProviderIndex: index,
		Name:          item.Name,
		Categories:    categories,
		Coordinate:    item.Coordinate,
		DistanceM:     distance,
		Address:       item.Address,
		Source:        item.Source,
	}
}
