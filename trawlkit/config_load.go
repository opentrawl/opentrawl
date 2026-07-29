package trawlkit

import (
	"fmt"
	"reflect"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

func loadConfig(info RegisteredTrawlerDeclaration, stateRoot string) error {
	if info.TrawlerConfiguration == nil {
		return nil
	}
	rv := reflect.ValueOf(info.TrawlerConfiguration)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ConfigFieldError{Field: "config"}
	}
	paths, err := resolveTrawlerArchivePaths(stateRoot, info)
	if err != nil {
		return err
	}
	exists, err := pathExists(paths.TrawlerConfigurationPath)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if exists {
		if err := config.LoadTOML(paths.TrawlerConfigurationPath, info.TrawlerConfiguration); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
	}
	if validator, ok := info.TrawlerConfiguration.(ConfigValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	return nil
}
