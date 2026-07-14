package archive

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenPlaceCardLineSelectsBestPlaceAndFormatsCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		places    []map[string]any
		locations []map[string]any
		want      string
	}{
		{
			name: "confirmed venue beats nearby poi",
			places: []map[string]any{
				{"observation_type": "poi_candidate", "value_text": "Nearby cafe", "tier": "nearby_poi", "distance_meters": 8.0},
				{"observation_type": "venue", "value_text": "Confirmed teahouse", "tier": "confirmed_venue", "distance_meters": 22.0},
				{"observation_type": "address", "value_text": "Example city", "tier": "area_context"},
			},
			locations: []map[string]any{{"latitude": 30.6608618253355, "longitude": 104.043701425124}},
			want:      "Confirmed teahouse · 30.6608 N, 104.0437 E",
		},
		{
			name: "nearest nearby poi wins",
			places: []map[string]any{
				{"observation_type": "poi_candidate", "value_text": "Far shop", "tier": "nearby_poi", "distance_meters": 40.0},
				{"observation_type": "poi_candidate", "value_text": "Near bakery", "tier": "nearby_poi", "distance_meters": 0.0},
				{"observation_type": "address", "value_text": "Example city", "tier": "area_context"},
			},
			locations: []map[string]any{{"latitude": -33.86885, "longitude": -151.20930}},
			want:      "Near bakery · 33.8688 S, 151.2093 W",
		},
		{
			name: "selected venue candidate beats nearby poi",
			places: []map[string]any{
				{"observation_type": "poi_candidate", "value_text": "Nearby shop", "tier": "nearby_poi", "distance_meters": 4.0},
				{"observation_type": "venue", "value_text": "Selected restaurant", "tier": "venue_candidate", "distance_meters": 32.0},
				{"observation_type": "address", "value_text": "Example city", "tier": "area_context"},
			},
			locations: []map[string]any{{"latitude": 52.36761, "longitude": 4.90411}},
			want:      "Selected restaurant · 52.3676 N, 4.9041 E",
		},
		{
			name: "raw rejected venue candidate does not beat nearby poi",
			places: []map[string]any{
				{"observation_type": "poi_candidate", "value_text": "Rejected restaurant", "tier": "venue_candidate", "distance_meters": 2.0},
				{"observation_type": "poi_candidate", "value_text": "Nearby shop", "tier": "nearby_poi", "distance_meters": 4.0},
				{"observation_type": "address", "value_text": "Example city", "tier": "area_context"},
			},
			locations: []map[string]any{{"latitude": 52.36761, "longitude": 4.90411}},
			want:      "Nearby shop · 52.3676 N, 4.9041 E",
		},
		{
			name: "known place beats area context",
			places: []map[string]any{
				{"observation_type": "address", "value_text": "Example city", "tier": "area_context"},
				{"observation_type": "known_place", "value_text": "work - Example Studio", "value_json": `{"kind":"work","name":"Example Studio"}`, "tier": "known_place", "distance_meters": 6.0},
			},
			want: "At work (Example Studio)",
		},
		{
			name: "area context is fallback",
			places: []map[string]any{
				{"observation_type": "address", "value_text": "Example city, Example country", "tier": "area_context"},
			},
			locations: []map[string]any{{"latitude": 52.36761, "longitude": 4.90411}},
			want:      "Example city, Example country · 52.3676 N, 4.9041 E",
		},
		{
			name:      "location without place still renders coordinates",
			locations: []map[string]any{{"latitude": 0.12349, "longitude": -0.98769}},
			want:      "0.1234 N, 0.9876 W",
		},
		{
			name: "no place or location omits line",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OpenPlaceCardLine(openPlace(tc.places, tc.locations))
			if got != tc.want {
				t.Fatalf("place line = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenPlaceIsJSONVisible(t *testing.T) {
	data, err := json.Marshal(OpenResult{
		Mechanical: OpenMechanical{
			Place: openPlace(
				[]map[string]any{{"observation_type": "venue", "value_text": "Synthetic Pier", "tier": "confirmed_venue"}},
				[]map[string]any{{"latitude": 52.367619, "longitude": 4.904119}},
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"place"`, `"name":"Synthetic Pier"`, `"latitude":52.3676`, `"longitude":4.9041`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("open JSON missing %s: %s", want, data)
		}
	}
	for _, forbidden := range []string{"52.367619", "4.904119"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("open JSON leaked raw coordinate %s: %s", forbidden, data)
		}
	}
}

func TestOpenResultProjectsStalenessAndAlbumsWithoutStorageTokens(t *testing.T) {
	asset := map[string]any{"id": "fixture"}
	locations := []map[string]any{{"latitude": 52.3676, "longitude": 4.9041, "horizontal_accuracy": 8.0}}
	albums := []map[string]any{{"album_title": "Synthetic Album", "album_kind": "generic_album:2:0"}}
	modelObservations := []map[string]any{{"stale_since": "2026-07-10T12:00:00Z", "stale_reason": "asset metadata changed in sync (fingerprint drift)"}}
	input, err := json.Marshal(map[string]any{
		"asset":              asset,
		"locations":          locations,
		"albums":             albums,
		"model_observations": modelObservations,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=archive_open_projection input=%s", input)
	result := newOpenResult(
		asset,
		nil,
		locations,
		albums,
		modelObservations,
		nil,
		nil,
	)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=archive_open_projection output=%s", data)
	got := string(data)
	for _, want := range []string{
		`"schema_version":5`,
		`"since":"2026-07-10T12:00:00Z"`,
		`"reason":"source details changed after this card was created"`,
		`"banner":"Card status: Stale · source details changed after this card was created · since 10 July 2026"`,
		`"albums":[{"title":"Synthetic Album"}]`,
		`"gps":{"latitude":52.3676,"longitude":4.9041,"horizontal_accuracy_meters":8}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("open JSON missing %s: %s", want, got)
		}
	}
	for _, forbidden := range []string{"generic_album:2:0", `"address"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("open JSON leaked %s: %s", forbidden, got)
		}
	}
}
