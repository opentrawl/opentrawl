package archive

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardformat"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

const topPOICandidateLimit = 5

type venueCandidate struct {
	place.POICandidate
	Plausibility venuePlausibility
}

func topPOICandidates(candidates []venueCandidate) []venueCandidate {
	if len(candidates) == 0 {
		return nil
	}
	ordered := append([]venueCandidate{}, candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return venueCandidateLess(ordered[i], ordered[j])
	})

	deduped := make([]venueCandidate, 0, len(ordered))
	seen := map[string]bool{}
	for _, candidate := range ordered {
		key := venueCandidateKey(candidate)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, candidate)
	}

	selected := make([]venueCandidate, 0, minInt(topPOICandidateLimit, len(deduped)))
	selectedKeys := map[string]bool{}
	add := func(candidate venueCandidate) bool {
		if len(selected) == topPOICandidateLimit {
			return false
		}
		key := venueCandidateKey(candidate)
		if key == "" || selectedKeys[key] {
			return true
		}
		selectedKeys[key] = true
		selected = append(selected, candidate)
		return len(selected) < topPOICandidateLimit
	}
	for _, candidate := range deduped {
		if !venueCandidateAlwaysIncluded(candidate.Tier) {
			continue
		}
		if !add(candidate) {
			break
		}
	}
	for _, candidate := range deduped {
		if !add(candidate) {
			break
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return venueCandidateLess(selected[i], selected[j])
	})
	return selected
}

func venueCandidateLess(left, right venueCandidate) bool {
	if left.DistanceM != right.DistanceM {
		if left.DistanceM <= 0 {
			return false
		}
		if right.DistanceM <= 0 {
			return true
		}
		return left.DistanceM < right.DistanceM
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return venueTierRank(left.Tier) < venueTierRank(right.Tier)
}

func venueCandidateAlwaysIncluded(tier string) bool {
	return tier == place.TierConfirmedVenue || tier == place.TierVenueCandidate
}

func venueCandidateKey(candidate venueCandidate) string {
	name := strings.ToLower(strings.TrimSpace(candidate.Name))
	tier := strings.TrimSpace(candidate.Tier)
	if name == "" {
		return ""
	}
	return name + "\x00" + tier
}

func venueTierRank(tier string) int {
	switch tier {
	case place.TierConfirmedVenue:
		return 0
	case place.TierVenueCandidate:
		return 1
	case place.TierNearbyPOI:
		return 2
	default:
		return 3
	}
}

func placeCandidateRows(rows []map[string]any) []venueCandidate {
	candidates := []venueCandidate{}
	for _, row := range rows {
		if rowString(row, "observation_type") != "poi_candidate" {
			continue
		}
		candidate := venueCandidate{
			POICandidate: place.POICandidate{
				Name:      strings.TrimSpace(rowString(row, "value_text")),
				Tier:      strings.TrimSpace(rowString(row, "tier")),
				DistanceM: rowFloat(row, "distance_meters"),
			},
		}
		var value map[string]any
		if json.Unmarshal([]byte(rowString(row, "value_json")), &value) == nil {
			candidate.Category = placeCategory(mapText(value, "category"))
			if distance := mapFloat(value, "distance_m"); distance > 0 {
				candidate.DistanceM = distance
			}
		}
		if candidate.Name != "" {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func promptVenueCandidateWithID(candidate venueCandidate, candidateID string) map[string]any {
	row := map[string]any{
		"name":            candidate.Name,
		"distance_meters": cardformat.Meters(candidate.DistanceM),
		"tier":            candidate.Tier,
	}
	if candidateID != "" {
		row["candidate_id"] = candidateID
	}
	if category := placeCategory(candidate.Category); category != "" {
		row["category"] = category
	}
	return row
}

func openVenueCandidate(candidate venueCandidate) OpenVenueCandidate {
	return OpenVenueCandidate{
		Name:           candidate.Name,
		Category:       placeCategory(candidate.Category),
		Tier:           candidate.Tier,
		DistanceMeters: cardformat.Meters(candidate.DistanceM),
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func venueCandidateID(index int) string {
	if index < 0 {
		return ""
	}
	return "venue_candidate_" + strconv.Itoa(index+1)
}
