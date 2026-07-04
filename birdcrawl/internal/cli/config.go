package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/tomlfile"
)

const (
	configEnv                     = "BIRDCRAWL_CONFIG"
	defaultMonthlyBudgetUSDMicros = int64(10_000_000)
)

type birdConfig struct {
	Path                string
	Handle              string
	UserID              string
	MonthlyBudgetMicros int64
	file                *tomlfile.File
}

func defaultConfigPath() string {
	return filepath.Join(defaultBaseDir(), "config.toml")
}

func configPathForDB(dbPath string) string {
	if env := strings.TrimSpace(os.Getenv(configEnv)); env != "" {
		return expandHome(env)
	}
	if strings.TrimSpace(dbPath) == "" {
		return defaultConfigPath()
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return defaultConfigPath()
	}
	return filepath.Join(dir, "config.toml")
}

func loadBirdConfig(path string) (birdConfig, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultConfigPath()
	}
	file, err := tomlfile.Read(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return birdConfig{}, err
		}
		file = tomlfile.Empty()
	}
	cfg := birdConfig{
		Path:                path,
		Handle:              strings.TrimPrefix(strings.TrimSpace(file.Get("handle")), "@"),
		UserID:              strings.TrimSpace(file.Get("user_id")),
		MonthlyBudgetMicros: defaultMonthlyBudgetUSDMicros,
		file:                file,
	}
	if raw := strings.TrimSpace(file.Get("monthly_budget_usd")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return birdConfig{}, err
		}
		cfg.MonthlyBudgetMicros = int64(value * 1_000_000)
	}
	return cfg, nil
}

func (c *birdConfig) SaveIdentity(userID, handle string) error {
	changed := false
	if c.UserID == "" && strings.TrimSpace(userID) != "" {
		c.UserID = strings.TrimSpace(userID)
		c.file.Set("user_id", c.UserID)
		changed = true
	}
	if c.Handle == "" && strings.TrimSpace(handle) != "" {
		c.Handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
		c.file.Set("handle", c.Handle)
		changed = true
	}
	if !changed {
		return nil
	}
	return c.file.WriteAtomic(c.Path, 0o600)
}

func (c birdConfig) MonthlyBudgetUSD() float64 {
	return float64(c.MonthlyBudgetMicros) / 1_000_000
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
