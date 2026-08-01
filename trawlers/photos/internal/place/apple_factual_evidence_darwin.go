//go:build darwin

package place

/*
#cgo darwin LDFLAGS: -framework Foundation -framework CoreLocation -framework MapKit
#include <stdlib.h>

char *photoscrawl_factual_location_evidence_json(const char *requestJSON, char **errorOut);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

func acquireAppleFactualLocationEvidence(ctx context.Context, input Input, operation string, radiusMeters float64) (FactualLocationEvidence, error) {
	select {
	case <-ctx.Done():
		return FactualLocationEvidence{}, ctx.Err()
	default:
	}
	requestBytes, err := json.Marshal(struct {
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		RadiusMeters float64 `json:"radius_meters"`
		Operation    string  `json:"operation"`
	}{input.Location.Latitude, input.Location.Longitude, radiusMeters, operation})
	if err != nil {
		return FactualLocationEvidence{}, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cRequest := C.CString(string(requestBytes))
	defer C.free(unsafe.Pointer(cRequest))
	var cError *C.char
	cResponse := C.photoscrawl_factual_location_evidence_json(cRequest, &cError)
	if cError != nil {
		defer C.free(unsafe.Pointer(cError))
		return FactualLocationEvidence{}, classifyBridgeError(C.GoString(cError))
	}
	if cResponse == nil {
		return FactualLocationEvidence{}, errors.New("Apple factual location operation returned no response")
	}
	defer C.free(unsafe.Pointer(cResponse))
	rawResponse := []byte(C.GoString(cResponse))
	var decoded struct {
		Address    *Address `json:"address"`
		Candidates []struct {
			Name       string      `json:"name"`
			Category   string      `json:"category"`
			Coordinate *Coordinate `json:"coordinate"`
			Address    *Address    `json:"address"`
			DistanceM  float64     `json:"distance_m"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rawResponse, &decoded); err != nil {
		return FactualLocationEvidence{}, fmt.Errorf("decode Apple factual location response: %w", err)
	}
	if operation == AppleNearbyLocationOperation && len(decoded.Candidates) > MaximumNearbyPlaceCandidates {
		return FactualLocationEvidence{}, errors.New("Apple nearby operation exceeded candidate limit")
	}
	candidates := make([]FactualLocationCandidate, 0, len(decoded.Candidates))
	for _, candidate := range decoded.Candidates {
		categories := []string{}
		if candidate.Category != "" {
			categories = append(categories, candidate.Category)
		}
		candidates = append(candidates, FactualLocationCandidate{
			Name:           candidate.Name,
			Categories:     categories,
			Coordinate:     candidate.Coordinate,
			Address:        candidate.Address,
			DistanceMeters: candidate.DistanceM,
		})
	}
	return FactualLocationEvidence{
		ProviderIdentity: AppleLocationProviderIdentity,
		Operation:        operation,
		Address:          decoded.Address,
		Candidates:       candidates,
		RawResponse:      rawResponse,
	}, nil
}
