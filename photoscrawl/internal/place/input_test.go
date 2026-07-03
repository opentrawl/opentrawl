package place

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadInputAcceptsEvalMetadataShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.json")
	data := []byte(`{
	  "asset": {
	    "local_identifier": "asset-1",
	    "creation_date": "2026-05-30T12:00:00Z",
	    "location": {
	      "latitude": 52.379189,
	      "longitude": 4.899431,
	      "horizontal_accuracy": 8.5
	    }
	  }
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := loadInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if input.AssetID != "asset-1" || input.TakenAt == "" {
		t.Fatalf("input identifiers = %#v", input)
	}
	if input.Location.Latitude != 52.379189 || input.Location.Longitude != 4.899431 {
		t.Fatalf("location = %#v", input.Location)
	}
	if input.AccuracyMeters != 8.5 {
		t.Fatalf("accuracy = %f", input.AccuracyMeters)
	}
}

func TestValidateCompleteRejectsPartialProviderResult(t *testing.T) {
	if err := validateComplete(Result{}); err == nil {
		t.Fatal("expected missing address to fail")
	}
	if err := validateComplete(Result{Address: &Address{Formatted: "Somewhere"}}); err != nil {
		t.Fatalf("complete result failed: %v", err)
	}
	if err := validateComplete(Result{
		Address:   &Address{Formatted: "Somewhere"},
		POIStatus: "maybe",
	}); err == nil {
		t.Fatal("expected unknown poi_status to fail")
	}
}

func TestLoadResultRejectsUnknownPOIStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "place.json")
	data := []byte(`{"poi_status":"maybe","poi_candidates":[]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResult(path); err == nil {
		t.Fatal("expected invalid poi_status to fail")
	}
}

func TestRenderCardKeepsAddressAndCapsPOIs(t *testing.T) {
	card := RenderCard(Result{
		RadiusMeters: 150,
		Address: &Address{
			Formatted:          "Example Airport Terminal, Example City, Example Country",
			Country:            "Example Country",
			AdministrativeArea: "Example State",
			Locality:           "Example City",
			TimeZone:           "Etc/UTC",
		},
		MapFeatures: []MapFeature{
			{Name: "Example International Airport", Kind: "airport", Relation: "nearby", DistanceM: 20},
		},
		POIStatus: POIStatusFound,
		POICandidates: []POICandidate{
			{Name: "Terminal 2", Category: "MKPOICategoryAirport", DistanceM: 10},
			{Name: "Example International Airport", Category: "MKPOICategoryAirport", DistanceM: 25},
			{Name: "Extra 1", Category: "MKPOICategoryStore", DistanceM: 30},
			{Name: "Extra 2", Category: "MKPOICategoryStore", DistanceM: 31},
			{Name: "Extra 3", Category: "MKPOICategoryStore", DistanceM: 32},
			{Name: "Extra 4", Category: "MKPOICategoryStore", DistanceM: 33},
		},
	})
	for _, want := range []string{
		"Full: Example Airport Terminal, Example City, Example Country",
		"Area: Example Country > Example State > Example City",
		"Time zone: Etc/UTC",
		"Example International Airport",
		"Terminal 2",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q:\n%s", want, card)
		}
	}
	if strings.Contains(card, "Top candidates") || strings.Contains(card, "Extra") {
		t.Fatalf("card leaked provider count or store noise:\n%s", card)
	}
}

func TestRenderCardNoPOIUsesMapContext(t *testing.T) {
	card := RenderCard(Result{
		RadiusMeters: 150,
		Address: &Address{
			Formatted:          "Example Ridge Trail, Example Valley, Example Country",
			Country:            "Example Country",
			AdministrativeArea: "Example Region",
			Locality:           "Example Valley",
		},
		MapFeatures: []MapFeature{
			{Name: "Example Ridge Trail", Kind: "highway path", Relation: "on/near"},
		},
		POIStatus: POIStatusNone,
	})
	for _, want := range []string{
		"Example Ridge Trail (trail, on/near)",
		"No named nearby POIs within 150m",
	} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q:\n%s", want, card)
		}
	}
}

func TestRenderCardDedupesAddressAndUsesAreasOfInterestAsMapContext(t *testing.T) {
	card := RenderCard(Result{
		RadiusMeters: 150,
		Address: &Address{
			Name:            "23 Example Street",
			SubThoroughfare: "23",
			Thoroughfare:    "Example Street",
			Locality:        "Example City",
			Country:         "Example Country",
			Formatted:       "23 Example Street, 23 Example Street, Example City, Example Country",
			AreasOfInterest: []string{"Example National Park"},
			Source:          "apple_corelocation_reverse",
		},
		POIStatus: POIStatusNone,
	})
	if strings.Count(card, "23 Example Street") != 1 {
		t.Fatalf("card repeated address:\n%s", card)
	}
	if !strings.Contains(card, "Example National Park (area)") {
		t.Fatalf("card did not render address area of interest as map context:\n%s", card)
	}
}

func TestFormatAddressDedupesReversedStreetNumber(t *testing.T) {
	address := FormatAddress(&Address{
		Name:            "Example Street 23",
		SubThoroughfare: "23",
		Thoroughfare:    "Example Street",
		SubLocality:     "Example District",
		Locality:        "Example City",
		Country:         "Example Country",
		Formatted:       "Example Street 23, 23 Example Street, Example District, Example City, Example Country",
	})
	want := "Example Street 23, Example District, Example City, Example Country"
	if address != want {
		t.Fatalf("address = %q, want %q", address, want)
	}
}
