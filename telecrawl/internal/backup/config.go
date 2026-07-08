package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ckconfig "github.com/openclaw/crawlkit/config"
)

const (
	defaultRemote = "https://github.com/steipete/backup-telecrawl.git"
)

type Config struct {
	Repo       string   `toml:"repo" json:"repo"`
	Remote     string   `toml:"remote" json:"remote"`
	Identity   string   `toml:"identity" json:"identity"`
	Recipients []string `toml:"recipients" json:"recipients"`
}

type Options struct {
	ConfigPath string
	Repo       string
	Remote     string
	Identity   string
	Recipients []string
	Push       bool
	Ref        string
	Tag        string
	Limit      int
}

func DefaultConfig() Config {
	return Config{
		Repo:     "~/Projects/backup-telecrawl",
		Remote:   defaultRemote,
		Identity: "~/.opentrawl/telecrawl/age.key",
	}
}

func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".opentrawl", "telecrawl", "config.toml")
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	cfg := DefaultConfig()
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return cfg, nil
	}
	var root struct {
		Backup Config `toml:"backup"`
	}
	root.Backup = cfg
	if err := ckconfig.LoadTOML(path, &root); err != nil {
		return Config{}, fmt.Errorf("read backup config: %w", err)
	}
	return root.Backup, nil
}

func SaveConfig(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultConfigPath()
	}
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return ckconfig.WriteTOML(path, struct {
		Backup Config `toml:"backup"`
	}{Backup: cfg}, 0o600)
}

func ResolveOptions(opts Options) (Config, error) {
	cfg, err := LoadConfig(opts.ConfigPath)
	if err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(opts.Repo) != "" {
		cfg.Repo = opts.Repo
	}
	if strings.TrimSpace(opts.Remote) != "" {
		cfg.Remote = opts.Remote
	}
	if strings.TrimSpace(opts.Identity) != "" {
		cfg.Identity = opts.Identity
	}
	if len(opts.Recipients) > 0 {
		cfg.Recipients = opts.Recipients
	}
	cfg.Repo = expandHome(cfg.Repo)
	cfg.Identity = expandHome(cfg.Identity)
	return cfg, nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if after, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, after)
		}
	}
	return path
}
