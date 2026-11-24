package telegram

import (
	"fmt"
	"os"
	"path/filepath"

	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
)

func writeTelegramConfig(path string, cfg TelegramCrawlerConfiguration) error {
	path = ckconfig.ExpandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create Telegram config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".telegram-config-*")
	if err != nil {
		return fmt.Errorf("create Telegram config: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := ckconfig.WriteTOML(temporaryPath, &cfg, 0o600); err != nil {
		return err
	}
	written, err := os.Open(temporaryPath)
	if err != nil {
		return err
	}
	if err := written.Sync(); err != nil {
		_ = written.Close()
		return err
	}
	if err := written.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace Telegram config: %w", err)
	}
	return nil
}
