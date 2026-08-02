package photos

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c Config) Validate() error {
	if path := strings.TrimSpace(c.GeoapifyAPIKeyFilePath); path != "" && !filepath.IsAbs(path) {
		return configError("geoapify_api_key_file", "geoapify_api_key_file must be an absolute path")
	}
	return nil
}

func configError(field, message string) error {
	return trawlkit.ConfigFieldError{Field: field, Err: fmt.Errorf("%s", message)}
}
