package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func TestUsageMentionsLabVerbs(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected usage error")
	}
	if !strings.Contains(err.Error(), "usage: photoscrawl-lab <place-evidence|place-context|eval-card|known-places>") {
		t.Fatalf("unexpected usage error: %v", err)
	}
}

func TestPlaceEvidencePassesExactCheckedConfiguration(t *testing.T) {
	root := t.TempDir()
	paths := archive.Paths{
		ConfigPath: filepath.Join(root, "config.toml"),
		DataDir:    filepath.Join(root, "data"),
		CacheDir:   filepath.Join(root, "cache"),
	}
	config := `library_path = "/tmp/Synthetic.photoslibrary"

[place_evidence.geoapify]
provider_identity = "synthetic-osm"
reverse_endpoint = "https://geo.example.com/configured/reverse"
nearby_endpoint = "https://geo.example.com/configured/nearby"
credential_env = "SYNTHETIC_OSM_KEY"
credential_parameter = "syntheticKey"
nearby_categories = ["natural", "tourism.museum"]
reverse_limit = 2
nearby_limit = 50
`
	if err := os.WriteFile(paths.ConfigPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(root, "input.json")
	input := `{"asset_id":"synthetic-asset","location":{"latitude":52.36,"longitude":4.89},"accuracy_meters":8}`
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SYNTHETIC_OSM_KEY", "synthetic-secret")
	outDir := filepath.Join(root, "evidence")
	var got place.EvidenceOptions
	var stdout bytes.Buffer
	err := runPlaceEvidenceWith(context.Background(), paths, []string{
		"--input", inputPath,
		"--coordinate-variant", "source-coordinate",
		"--radius", "175",
		"--out", outDir,
		"--json",
	}, &stdout, func(_ context.Context, opts place.EvidenceOptions) (place.EvidenceResult, error) {
		got = opts
		return place.EvidenceResult{State: "complete", CoordinateVariant: opts.CoordinateVariant}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Input.Location.Latitude != 52.36 || got.Input.Location.Longitude != 4.89 || got.RadiusMeters != 175 {
		t.Fatalf("coordinate boundary = %#v", got)
	}
	if got.CoordinateVariant != "source-coordinate" || got.OutputDir != outDir {
		t.Fatalf("run boundary = %#v", got)
	}
	if got.Geoapify.ProviderIdentity != "synthetic-osm" || got.Geoapify.ReverseEndpoint != "https://geo.example.com/configured/reverse" || got.Geoapify.NearbyEndpoint != "https://geo.example.com/configured/nearby" {
		t.Fatalf("provider boundary = %#v", got.Geoapify)
	}
	if got.Geoapify.CredentialReference != "SYNTHETIC_OSM_KEY" || got.Geoapify.CredentialParameter != "syntheticKey" || got.Geoapify.Credential != "synthetic-secret" {
		t.Fatalf("credential boundary = %#v", got.Geoapify)
	}
	if strings.Contains(stdout.String(), "synthetic-secret") {
		t.Fatalf("command output leaked credential: %s", stdout.String())
	}
	t.Logf("RAW CONFIG %q", config)
	t.Logf("RAW COORDINATE INPUT %q", input)
}

func TestSplitList(t *testing.T) {
	got := splitList("gemma4:31b, gemini-flash-latest,")
	want := []string{"gemma4:31b", "gemini-flash-latest"}
	if len(got) != len(want) {
		t.Fatalf("splitList = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("splitList = %#v", got)
		}
	}
}

func TestReadKnownPlacesInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known-places.json")
	data := []byte(`[{
	  "label_kind": "home",
	  "display_name": "Example Residence",
	  "latitude": 52,
	  "longitude": 4,
	  "valid_from": "2026-01-01T00:00:00Z"
	}]`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	places, err := readKnownPlacesInput(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(places) != 1 || places[0].LabelKind != archive.KnownPlaceKindHome || places[0].DisplayName != "Example Residence" {
		t.Fatalf("known places input = %#v", places)
	}
}
