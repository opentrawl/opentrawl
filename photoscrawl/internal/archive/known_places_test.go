package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/place"
)

func TestKnownPlaceMatchRequiresWindowAndRadius(t *testing.T) {
	t.Parallel()
	places := []KnownPlace{{
		LabelKind:    KnownPlaceKindFormerHome,
		DisplayName:  "Example Residence",
		Latitude:     52.0000,
		Longitude:    4.0000,
		RadiusMeters: 75,
		ValidFrom:    "2026-01-01T00:00:00Z",
		ValidUntil:   "2026-12-31T23:59:59Z",
	}}
	if match := matchKnownPlace(places, 52.0003, 4.0000, "2026-05-27T12:00:00Z"); match == nil || match.Kind != KnownPlaceKindFormerHome {
		t.Fatalf("inside match = %#v", match)
	}
	if match := matchKnownPlace(places, 52.0003, 4.0000, "2025-12-31T23:59:59Z"); match != nil {
		t.Fatalf("outside window matched: %#v", match)
	}
	if match := matchKnownPlace(places, 52.0010, 4.0000, "2026-05-27T12:00:00Z"); match != nil {
		t.Fatalf("outside radius matched: %#v", match)
	}
}

func TestSetKnownPlacesUpsertsByKindAndName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	first, err := SetKnownPlaces(ctx, paths, []KnownPlace{{
		LabelKind:   KnownPlaceKindWork,
		DisplayName: "Example Studio",
		Latitude:    52.0,
		Longitude:   4.0,
		ValidFrom:   "2026-01-01T00:00:00Z",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Upserted != 1 || first.Places[0].RadiusMeters != defaultKnownPlaceRadiusMeters {
		t.Fatalf("first set = %#v", first)
	}
	if _, err := SetKnownPlaces(ctx, paths, []KnownPlace{{
		LabelKind:    KnownPlaceKindWork,
		DisplayName:  "Example Studio",
		Latitude:     52.0002,
		Longitude:    4.0002,
		RadiusMeters: 90,
		ValidFrom:    "2026-01-01T00:00:00Z",
	}}); err != nil {
		t.Fatal(err)
	}
	listed, err := ListKnownPlaces(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Places) != 1 {
		t.Fatalf("listed places = %#v", listed.Places)
	}
	got := listed.Places[0]
	if got.Latitude != 52.0002 || got.RadiusMeters != 90 {
		t.Fatalf("upserted place = %#v", got)
	}
}

func TestPhotoCardMetadataJSONIncludesKnownPlaceAndSuppressesPOIs(t *testing.T) {
	t.Parallel()
	metadata, err := photoCardMetadataJSON(classifyInput{
		CreationDate:   "2026-05-27T10:00:00Z",
		MediaType:      "image",
		Width:          100,
		Height:         80,
		HasLocation:    true,
		Latitude:       52,
		Longitude:      4,
		AccuracyMeters: 8,
		KnownPlace: &KnownPlaceMatch{
			Kind: KnownPlaceKindHome,
			Name: "Example Residence",
		},
		Place: &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				POIStatus: place.POIStatusFound,
				Address:   &place.Address{Formatted: "23 Example Street, Example City, Example Country"},
				POICandidates: []place.POICandidate{{
					Name:      "Synthetic Consultancy",
					Category:  "business",
					DistanceM: 12,
					Tier:      place.TierVenueCandidate,
					Source:    "fixture",
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(metadata, &payload); err != nil {
		t.Fatal(err)
	}
	location := payload["location"].(map[string]any)
	knownPlace := location["known_place"].(map[string]any)
	if knownPlace["kind"] != KnownPlaceKindHome || knownPlace["name"] != "Example Residence" {
		t.Fatalf("known place sidecar = %#v", knownPlace)
	}
	placeContext := location["place_context"].(map[string]any)
	if candidates := placeContext["venue_candidates"].([]any); len(candidates) != 0 {
		t.Fatalf("known place sidecar kept POIs: %#v", candidates)
	}
	if strings.Contains(string(metadata), "Synthetic Consultancy") {
		t.Fatalf("known place sidecar leaked POI candidate:\n%s", metadata)
	}
}

func TestKnownPlaceOpenSearchAndPOISuppression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	assetID := ""
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, 0, "")
		if err != nil {
			return err
		}
		input := inputs[0]
		assetID = input.AssetID
		input.KnownPlace = &KnownPlaceMatch{
			Kind:           KnownPlaceKindFormerHome,
			Name:           "Example Residence",
			DistanceMeters: 12,
		}
		input.Place = &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider:     "apple",
				Source:       "fixture",
				RadiusMeters: 150,
				GeneratedAt:  fixedClock("2026-05-28T10:05:00Z")(),
				Address: &place.Address{
					Name:            "Example Street 23",
					SubThoroughfare: "23",
					Thoroughfare:    "Example Street",
					Locality:        "Example City",
					Country:         "Example Country",
					Formatted:       "Example Street 23, 23 Example Street, Example City, Example Country",
				},
				POIStatus: place.POIStatusFound,
				POICandidates: []place.POICandidate{
					{Name: "Synthetic Consultancy", Category: "business", DistanceM: 10, Tier: place.TierVenueCandidate, Source: "fixture"},
					{Name: "Synthetic Shop", Category: "shop", DistanceM: 20, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Synthetic Gym", Category: "fitness centre", DistanceM: 30, Tier: place.TierNearbyPOI, Source: "fixture"},
				},
			},
		}
		_, err = writePlaceClassification(ctx, tx, input, venuePlausibility{
			CandidateName: "Synthetic Consultancy",
			Verdict:       venueVerdictPlausible,
			Reason:        "synthetic fixture reason",
		}, fixedClock("2026-05-28T10:15:00Z")())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var poiCount, venueCount int
	if err := db.DB().QueryRowContext(ctx, `
select count(*) from place_observation where asset_id = ? and observation_type = 'poi_candidate'
`, assetID).Scan(&poiCount); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `
select count(*) from place_observation where asset_id = ? and observation_type = 'venue'
`, assetID).Scan(&venueCount); err != nil {
		t.Fatal(err)
	}
	if poiCount != 2 || venueCount != 0 {
		t.Fatalf("place rows poi=%d venue=%d, want poi=2 venue=0", poiCount, venueCount)
	}

	opened, err := Open(ctx, paths, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Mechanical.KnownPlace == nil || opened.Mechanical.KnownPlace.Kind != KnownPlaceKindFormerHome {
		t.Fatalf("known place = %#v", opened.Mechanical.KnownPlace)
	}
	if opened.Mechanical.Address == "" {
		t.Fatalf("address should still render: %#v", opened.Mechanical)
	}
	if opened.Mechanical.Venue != nil || len(opened.Mechanical.VenueCandidates) != 0 {
		t.Fatalf("known place leaked venue surfaces: %#v", opened.Mechanical)
	}
	data, err := json.Marshal(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"known_place"`) || strings.Contains(string(data), "Synthetic Consultancy") {
		t.Fatalf("open JSON shape wrong:\n%s", data)
	}

	search, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].Where != "Example Residence (home at the time)" {
		t.Fatalf("search where = %#v", search.Results)
	}
}
