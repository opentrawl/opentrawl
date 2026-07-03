package place

import (
	"math"
	"sort"
	"strings"
)

const (
	TierConfirmedVenue = "confirmed_venue"
	TierVenueCandidate = "venue_candidate"
	TierNearbyPOI      = "nearby_poi"
	TierAreaContext    = "area_context"
)

// TierCandidates assigns venue permissions from provider geometry only.
func TierCandidates(input Input, candidates []POICandidate) []POICandidate {
	out := append([]POICandidate{}, candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		left, leftOK := candidateDistance(input, out[i])
		right, rightOK := candidateDistance(input, out[j])
		switch {
		case leftOK && rightOK && left != right:
			return left < right
		case leftOK != rightOK:
			return leftOK
		default:
			return out[i].Name < out[j].Name
		}
	})
	threshold := venueCandidateThreshold(input.AccuracyMeters)
	for i := range out {
		distance, ok := candidateDistance(input, out[i])
		if !ok {
			out[i].Tier = TierNearbyPOI
			continue
		}
		out[i].DistanceM = distance
		if distance <= threshold && !hasCompetingCandidate(input, out, i, threshold) {
			out[i].Tier = TierVenueCandidate
			continue
		}
		out[i].Tier = TierNearbyPOI
	}
	return out
}

func venueCandidateThreshold(accuracy float64) float64 {
	if accuracy < 25 {
		return 25
	}
	return accuracy
}

func hasCompetingCandidate(input Input, candidates []POICandidate, index int, threshold float64) bool {
	distance, ok := candidateDistance(input, candidates[index])
	if !ok {
		return false
	}
	category := candidateType(candidates[index])
	for i, candidate := range candidates {
		if i == index || candidateType(candidate) == category {
			continue
		}
		otherDistance, otherOK := candidateDistance(input, candidate)
		if !otherOK || otherDistance > threshold {
			continue
		}
		if math.Round(otherDistance) == math.Round(distance) {
			return true
		}
	}
	return false
}

func candidateType(candidate POICandidate) string {
	category := strings.ToLower(strings.TrimSpace(candidate.Category))
	if category != "" {
		return category
	}
	return strings.ToLower(strings.TrimSpace(candidate.Source))
}

func candidateDistance(input Input, candidate POICandidate) (float64, bool) {
	if candidate.DistanceM > 0 {
		return candidate.DistanceM, true
	}
	if candidate.Coordinate == nil {
		return 0, false
	}
	return metersBetween(input.Location, *candidate.Coordinate), true
}

func metersBetween(a, b Coordinate) float64 {
	const earthRadiusMeters = 6371008.8
	lat1 := degreesToRadians(a.Latitude)
	lat2 := degreesToRadians(b.Latitude)
	dLat := degreesToRadians(b.Latitude - a.Latitude)
	dLon := degreesToRadians(b.Longitude - a.Longitude)
	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	h := sinLat*sinLat + math.Cos(lat1)*math.Cos(lat2)*sinLon*sinLon
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(h))
}

func degreesToRadians(value float64) float64 {
	return value * math.Pi / 180
}
