package cli

import (
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

const trawlConfigurationFileName = "config.toml"

type trawlConfiguration struct {
	KeepSourceAppsRunning bool `toml:"keep_source_apps_running"`
}

func (r *Runtime) keepSourceAppsRunningForAutomaticUpdates() bool {
	if r == nil || strings.TrimSpace(r.stateRoot) == "" {
		return false
	}
	configurationPath := filepath.Join(r.stateRoot, "trawl", trawlConfigurationFileName)
	var configuration trawlConfiguration
	if err := config.LoadTOMLFileIfPresent(configurationPath, &configuration); err != nil {
		r.logInfo("trawl_configuration_invalid", "error="+logQuote(err.Error()))
		return false
	}
	return configuration.KeepSourceAppsRunning
}
