package place

import "testing"

func TestTierCandidatesUsesGeometryOnly(t *testing.T) {
	input := Input{
		Location:       Coordinate{Latitude: 52, Longitude: 4},
		AccuracyMeters: 8,
	}
	candidates := TierCandidates(input, []POICandidate{
		{Name: "Synthetic Cafe", Category: "cafe", DistanceM: 12, Source: "fixture"},
		{Name: "Synthetic Station", Category: "station", DistanceM: 80, Source: "fixture"},
	})
	if candidates[0].Name != "Synthetic Cafe" || candidates[0].Tier != TierVenueCandidate {
		t.Fatalf("top candidate = %#v", candidates[0])
	}
	if candidates[1].Tier != TierNearbyPOI {
		t.Fatalf("nearby candidate = %#v", candidates[1])
	}
}

func TestTierCandidatesBlocksEqualCompetingType(t *testing.T) {
	input := Input{
		Location:       Coordinate{Latitude: 52, Longitude: 4},
		AccuracyMeters: 20,
	}
	candidates := TierCandidates(input, []POICandidate{
		{Name: "Synthetic Cafe", Category: "cafe", DistanceM: 12.2, Source: "fixture"},
		{Name: "Synthetic Shop", Category: "shop", DistanceM: 12.4, Source: "fixture"},
	})
	for _, candidate := range candidates {
		if candidate.Tier != TierNearbyPOI {
			t.Fatalf("candidate should not be venue tier with equal competitor: %#v", candidate)
		}
	}
}
