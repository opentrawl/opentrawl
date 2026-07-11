package photoscrawl

import (
	"errors"
	"os"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
)

func TestPlaceEvidenceConfigRoundTripAndValidation(t *testing.T) {
	path := t.TempDir() + "/config.toml"
	input := []byte(`library_path = "/tmp/Synthetic.photoslibrary"

[place_evidence.geoapify]
provider_identity = "synthetic-osm"
reverse_endpoint = "https://geo.example.com/configured/reverse"
nearby_endpoint = "https://geo.example.com/configured/nearby"
credential_env = "SYNTHETIC_OSM_KEY"
credential_parameter = "syntheticKey"
nearby_categories = ["natural", "tourism.museum"]
reverse_limit = 2
nearby_limit = 50
`)
	if err := os.WriteFile(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := ckconfig.LoadTOML(path, &config); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	configured, err := config.ConfiguredGeoapifyEvidence()
	if err != nil {
		t.Fatal(err)
	}
	if configured.ProviderIdentity != "synthetic-osm" || configured.CredentialEnv != "SYNTHETIC_OSM_KEY" {
		t.Fatalf("configured evidence = %#v", configured)
	}
	if len(configured.NearbyCategories) != 2 || configured.NearbyLimit != 50 {
		t.Fatalf("configured query = %#v", configured)
	}
}

func TestPlaceEvidenceConfigIsOptionalButPartialConfigurationFails(t *testing.T) {
	if err := (Config{}).Validate(); err != nil {
		t.Fatalf("empty configuration failed: %v", err)
	}
	_, err := (Config{}).ConfiguredGeoapifyEvidence()
	assertConfigField(t, err, "place_evidence.geoapify")

	partial := Config{PlaceEvidence: PlaceEvidenceConfig{Geoapify: GeoapifyEvidenceConfig{
		ProviderIdentity: "synthetic-osm",
	}}}
	assertConfigField(t, partial.Validate(), "place_evidence.geoapify.reverse_endpoint")
}

func TestPlaceEvidenceConfigRejectsUnsafeOrImplicitProviderInputs(t *testing.T) {
	valid := validGeoapifyEvidenceConfig()
	cases := []struct {
		name   string
		change func(*GeoapifyEvidenceConfig)
		field  string
	}{
		{"HTTP endpoint", func(c *GeoapifyEvidenceConfig) { c.ReverseEndpoint = "http://geo.example.com/reverse" }, "place_evidence.geoapify.reverse_endpoint"},
		{"credential in endpoint", func(c *GeoapifyEvidenceConfig) { c.NearbyEndpoint += "?syntheticKey=secret" }, "place_evidence.geoapify.nearby_endpoint"},
		{"non-credential query in endpoint", func(c *GeoapifyEvidenceConfig) { c.ReverseEndpoint += "?language=en" }, "place_evidence.geoapify.reverse_endpoint"},
		{"empty query in endpoint", func(c *GeoapifyEvidenceConfig) { c.NearbyEndpoint += "?" }, "place_evidence.geoapify.nearby_endpoint"},
		{"missing categories", func(c *GeoapifyEvidenceConfig) { c.NearbyCategories = nil }, "place_evidence.geoapify.nearby_categories"},
		{"missing reverse limit", func(c *GeoapifyEvidenceConfig) { c.ReverseLimit = 0 }, "place_evidence.geoapify.reverse_limit"},
		{"invalid credential reference", func(c *GeoapifyEvidenceConfig) { c.CredentialEnv = "NOT AN ENV" }, "place_evidence.geoapify.credential_env"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.NearbyCategories = append([]string(nil), valid.NearbyCategories...)
			test.change(&config)
			assertConfigField(t, config.validate(), test.field)
		})
	}
}

func validGeoapifyEvidenceConfig() GeoapifyEvidenceConfig {
	return GeoapifyEvidenceConfig{
		ProviderIdentity:    "synthetic-osm",
		ReverseEndpoint:     "https://geo.example.com/reverse",
		NearbyEndpoint:      "https://geo.example.com/nearby",
		CredentialEnv:       "SYNTHETIC_OSM_KEY",
		CredentialParameter: "syntheticKey",
		NearbyCategories:    []string{"natural"},
		ReverseLimit:        2,
		NearbyLimit:         50,
	}
}

func assertConfigField(t *testing.T, err error, field string) {
	t.Helper()
	var configErr trawlkit.ConfigFieldError
	if !errors.As(err, &configErr) || configErr.Field != field {
		t.Fatalf("error = %v, want config field %q", err, field)
	}
}
