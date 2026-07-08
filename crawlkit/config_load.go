package crawlkit

import (
	"fmt"
	"reflect"

	"github.com/openclaw/crawlkit/config"
)

func loadConfig(info Info, stateRoot string) error {
	if info.Config == nil {
		return nil
	}
	rv := reflect.ValueOf(info.Config)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return ConfigFieldError{Field: "config", Fix: "pass a pointer to the crawler config struct"}
	}
	paths, err := resolveSourcePaths(stateRoot, info)
	if err != nil {
		return err
	}
	exists, err := pathExists(paths.Config)
	if err != nil {
		return fmt.Errorf("stat config: %w", err)
	}
	if exists {
		if err := config.LoadTOML(paths.Config, info.Config); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
	}
	if validator, ok := info.Config.(ConfigValidator); ok {
		if err := validator.Validate(); err != nil {
			return err
		}
	}
	return nil
}
