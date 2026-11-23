package beeper

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperapi"
)

const (
	defaultRequestTimeoutSeconds = 15
	maxRequestTimeoutSeconds     = 120
)

var defaultExcludedBeeperNetworks = []string{
	"WhatsApp",
	"Telegram",
	"iMessage",
	"SMS",
}

// BeeperTrawlerConfiguration holds optional user settings from config.toml.
type BeeperTrawlerConfiguration struct {
	APIBaseURL             string   `toml:"api_base_url,omitempty"`
	TokenFile              string   `toml:"token_file,omitempty"`
	IncludeNetworks        []string `toml:"include_networks,omitempty"`
	ExcludeNetworks        []string `toml:"exclude_networks,omitempty"`
	RequestTimeoutSeconds  int      `toml:"request_timeout_seconds,omitempty"`
	MaxMessagePagesPerChat int      `toml:"max_message_pages_per_chat,omitempty"`
}

func defaultBeeperTrawlerConfiguration() BeeperTrawlerConfiguration {
	return BeeperTrawlerConfiguration{
		APIBaseURL:            beeperapi.DefaultBeeperAPIBaseURL,
		RequestTimeoutSeconds: defaultRequestTimeoutSeconds,
	}
}

// Validate checks that configuration values are well-formed.
func (cfg BeeperTrawlerConfiguration) Validate() error {
	trimmedURL := strings.TrimSpace(cfg.APIBaseURL)
	if trimmedURL != "" {
		parsed, err := url.Parse(trimmedURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || strings.TrimSpace(parsed.Host) == "" {
			return fmt.Errorf("invalid api_base_url %q: must be an http or https URL", cfg.APIBaseURL)
		}
	}
	if cfg.RequestTimeoutSeconds < 0 || cfg.RequestTimeoutSeconds > maxRequestTimeoutSeconds {
		return fmt.Errorf("request_timeout_seconds must be between 1 and %d", maxRequestTimeoutSeconds)
	}
	if cfg.MaxMessagePagesPerChat < 0 {
		return fmt.Errorf("max_message_pages_per_chat must not be negative")
	}
	return nil
}

// EffectiveAPIBaseURL returns the configured API base URL or the default loopback URL.
func (cfg BeeperTrawlerConfiguration) EffectiveAPIBaseURL() string {
	if trimmed := strings.TrimSpace(cfg.APIBaseURL); trimmed != "" {
		return strings.TrimRight(trimmed, "/")
	}
	return beeperapi.DefaultBeeperAPIBaseURL
}

// EffectiveRequestTimeout returns the per-request HTTP timeout.
func (cfg BeeperTrawlerConfiguration) EffectiveRequestTimeout() time.Duration {
	if cfg.RequestTimeoutSeconds > 0 {
		return time.Duration(cfg.RequestTimeoutSeconds) * time.Second
	}
	return beeperapi.DefaultBeeperRequestTimeout
}

// BeeperNetworkFilter decides whether a Beeper chat's network should be archived.
type BeeperNetworkFilter struct {
	includeSet map[string]struct{}
	excludeSet map[string]struct{}
}

// BuildBeeperNetworkFilter combines config settings and per-invocation CLI overrides.
//
// When neither include nor exclude networks are specified, Beeper skips networks
// that OpenTrawl already crawls natively (WhatsApp, Telegram, iMessage, SMS).
func BuildBeeperNetworkFilter(
	cfg BeeperTrawlerConfiguration,
	cliIncludeCSV string,
	cliExcludeCSV string,
) BeeperNetworkFilter {
	includes := append([]string(nil), cfg.IncludeNetworks...)
	excludes := append([]string(nil), cfg.ExcludeNetworks...)

	if parsed := splitNetworkList(cliIncludeCSV); len(parsed) > 0 {
		includes = parsed
	}
	if parsed := splitNetworkList(cliExcludeCSV); len(parsed) > 0 {
		excludes = parsed
	}
	if len(includes) == 0 && len(excludes) == 0 {
		excludes = append([]string(nil), defaultExcludedBeeperNetworks...)
	}

	return BeeperNetworkFilter{
		includeSet: normalizedNetworkSet(includes),
		excludeSet: normalizedNetworkSet(excludes),
	}
}

// Allows reports whether a Beeper chat with the given network display name or
// account ID should be archived.
func (filter BeeperNetworkFilter) Allows(networkName string) bool {
	normalized := normalizeBeeperNetworkKey(networkName)
	if len(filter.excludeSet) > 0 {
		if _, excluded := filter.excludeSet[normalized]; excluded {
			return false
		}
		if normalized == "imessagesms" {
			if _, excluded := filter.excludeSet["imessage"]; excluded {
				return false
			}
		}
	}
	if len(filter.includeSet) == 0 {
		return true
	}
	_, included := filter.includeSet[normalized]
	return included
}

func splitNetworkList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizedNetworkSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if key := normalizeBeeperNetworkKey(name); key != "" {
			set[key] = struct{}{}
		}
	}
	return set
}

func normalizeBeeperNetworkKey(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func expandHomeRelativePath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return trimmed
		}
		if trimmed == "~" {
			return filepath.Clean(home)
		}
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}
	return filepath.Clean(trimmed)
}
