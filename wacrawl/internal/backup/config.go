package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultRemote = "https://github.com/steipete/backup-wacrawl.git"
)

var renameConfigFile = os.Rename

type Config struct {
	Repo       string   `json:"repo" toml:"repo,omitempty"`
	Remote     string   `json:"remote" toml:"remote,omitempty"`
	Identity   string   `json:"identity" toml:"identity,omitempty"`
	Recipients []string `json:"recipients" toml:"recipients,omitempty"`
}

type Options struct {
	ConfigPath string
	Config     Config
	SaveConfig func(Config) error
	Repo       string
	Remote     string
	Identity   string
	Recipients []string
	Push       bool
	Ref        string
	Tag        string
	Limit      int
	NoMedia    bool
}

func DefaultConfig() Config {
	return Config{
		Repo:     "~/Projects/backup-wacrawl",
		Remote:   defaultRemote,
		Identity: "~/.opentrawl/wacrawl/age.key",
	}
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultConfig(), nil
	}
	cfg := DefaultConfig()
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("read backup config: %w", err)
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("backup config path is required")
	}
	path = expandHome(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(path, data, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	closeTmp := func() error {
		if closed {
			return nil
		}
		closed = true
		return tmp.Close()
	}

	if _, err := tmp.Write(data); err != nil {
		_ = closeTmp()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = closeTmp()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = closeTmp()
		return err
	}
	if err := closeTmp(); err != nil {
		return err
	}
	if err := renameConfigFile(tmpName, path); err != nil {
		return err
	}
	if err := syncConfigDir(dir); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func syncConfigDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- dir is the validated parent of the config path being atomically replaced.
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func ResolveOptions(opts Options) (Config, error) {
	cfg := mergeConfig(DefaultConfig(), opts.Config)
	if strings.TrimSpace(opts.ConfigPath) != "" {
		loaded, err := LoadConfig(opts.ConfigPath)
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
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

func SaveResolvedConfig(opts Options, cfg Config) error {
	if opts.SaveConfig != nil {
		return opts.SaveConfig(cfg)
	}
	return SaveConfig(opts.ConfigPath, cfg)
}

func mergeConfig(base, overlay Config) Config {
	if strings.TrimSpace(overlay.Repo) != "" {
		base.Repo = overlay.Repo
	}
	if strings.TrimSpace(overlay.Remote) != "" {
		base.Remote = overlay.Remote
	}
	if strings.TrimSpace(overlay.Identity) != "" {
		base.Identity = overlay.Identity
	}
	if len(overlay.Recipients) > 0 {
		base.Recipients = append([]string(nil), overlay.Recipients...)
	}
	return base
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
