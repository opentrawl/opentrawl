package photoscrawl

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlkit"
)

type PlaceEvidenceConfig struct {
	Geoapify GeoapifyEvidenceConfig `toml:"geoapify"`
}

type GeoapifyEvidenceConfig struct {
	ProviderIdentity    string   `toml:"provider_identity"`
	ReverseEndpoint     string   `toml:"reverse_endpoint"`
	NearbyEndpoint      string   `toml:"nearby_endpoint"`
	CredentialEnv       string   `toml:"credential_env"`
	CredentialParameter string   `toml:"credential_parameter"`
	NearbyCategories    []string `toml:"nearby_categories"`
	ReverseLimit        int      `toml:"reverse_limit"`
	NearbyLimit         int      `toml:"nearby_limit"`
}

func (c Config) Validate() error {
	if !c.PlaceEvidence.Geoapify.configured() {
		return nil
	}
	return c.PlaceEvidence.Geoapify.validate()
}

func (c Config) ConfiguredGeoapifyEvidence() (GeoapifyEvidenceConfig, error) {
	configured := c.PlaceEvidence.Geoapify
	if !configured.configured() {
		return GeoapifyEvidenceConfig{}, configError(
			"place_evidence.geoapify",
			"add a complete [place_evidence.geoapify] section to the Photos config",
			"Geoapify place evidence is not configured",
		)
	}
	if err := configured.validate(); err != nil {
		return GeoapifyEvidenceConfig{}, err
	}
	return configured, nil
}

func (c GeoapifyEvidenceConfig) configured() bool {
	return strings.TrimSpace(c.ProviderIdentity) != "" ||
		strings.TrimSpace(c.ReverseEndpoint) != "" ||
		strings.TrimSpace(c.NearbyEndpoint) != "" ||
		strings.TrimSpace(c.CredentialEnv) != "" ||
		strings.TrimSpace(c.CredentialParameter) != "" ||
		len(c.NearbyCategories) > 0 || c.ReverseLimit != 0 || c.NearbyLimit != 0
}

func (c GeoapifyEvidenceConfig) validate() error {
	required := []struct {
		field string
		value string
	}{
		{"provider_identity", c.ProviderIdentity},
		{"reverse_endpoint", c.ReverseEndpoint},
		{"nearby_endpoint", c.NearbyEndpoint},
		{"credential_env", c.CredentialEnv},
		{"credential_parameter", c.CredentialParameter},
	}
	for _, value := range required {
		if strings.TrimSpace(value.value) == "" {
			return configError(
				"place_evidence.geoapify."+value.field,
				"set every field in [place_evidence.geoapify] explicitly",
				fmt.Sprintf("%s is required", value.field),
			)
		}
	}
	if !validEnvironmentName(c.CredentialEnv) {
		return configError(
			"place_evidence.geoapify.credential_env",
			"set credential_env to the name of the environment variable supplied by secret management",
			"credential_env must be an environment variable name",
		)
	}
	if !validQueryParameter(c.CredentialParameter) {
		return configError(
			"place_evidence.geoapify.credential_parameter",
			"set credential_parameter to the provider's query parameter name",
			"credential_parameter must be one URL query parameter name",
		)
	}
	if err := validateEvidenceEndpoint("reverse_endpoint", c.ReverseEndpoint); err != nil {
		return err
	}
	if err := validateEvidenceEndpoint("nearby_endpoint", c.NearbyEndpoint); err != nil {
		return err
	}
	if len(c.NearbyCategories) == 0 {
		return configError(
			"place_evidence.geoapify.nearby_categories",
			"set the exact provider categories to probe; there is no default category set",
			"nearby_categories is required",
		)
	}
	for _, category := range c.NearbyCategories {
		category = strings.TrimSpace(category)
		if category == "" || strings.Contains(category, ",") {
			return configError(
				"place_evidence.geoapify.nearby_categories",
				"use non-empty category values without commas",
				"nearby_categories contains an invalid value",
			)
		}
	}
	if c.ReverseLimit <= 0 {
		return configError(
			"place_evidence.geoapify.reverse_limit",
			"set the exact reverse-result limit to probe",
			"reverse_limit must be greater than 0",
		)
	}
	if c.NearbyLimit <= 0 {
		return configError(
			"place_evidence.geoapify.nearby_limit",
			"set the exact nearby-result limit to probe",
			"nearby_limit must be greater than 0",
		)
	}
	return nil
}

func validateEvidenceEndpoint(field, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return configError("place_evidence.geoapify."+field, "set a complete HTTPS endpoint", field+" is invalid")
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return configError("place_evidence.geoapify."+field, "set a complete HTTPS endpoint", field+" must use HTTPS and include a host")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return configError("place_evidence.geoapify."+field, "remove user information and fragments from the endpoint", field+" contains unsafe URL components")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return configError("place_evidence.geoapify."+field, "remove the query string; the evidence request supplies every query value", field+" must not contain a query string")
	}
	return nil
}

func validEnvironmentName(value string) bool {
	for index, r := range strings.TrimSpace(value) {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return strings.TrimSpace(value) != ""
}

func validQueryParameter(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || strings.ContainsRune("&=?#", r) {
			return false
		}
	}
	return true
}

func configError(field, fix, message string) error {
	return trawlkit.ConfigFieldError{
		Field: field,
		Fix:   fix,
		Err:   fmt.Errorf("%s", message),
	}
}
