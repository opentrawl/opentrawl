package birdcrawl

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/tomlfile"
)

const defaultMonthlyBudgetUSDMicros = int64(10_000_000)

type Config struct {
	Handle           string `toml:"handle"`
	UserID           string `toml:"user_id"`
	MonthlyBudgetUSD string `toml:"monthly_budget_usd"`
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.MonthlyBudgetUSD) == "" {
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(c.MonthlyBudgetUSD), 64)
	if err != nil {
		return fmt.Errorf("monthly_budget_usd must be a number")
	}
	if value < 0 {
		return fmt.Errorf("monthly_budget_usd must be at least 0")
	}
	return nil
}

type birdConfig struct {
	Path                string
	Handle              string
	UserID              string
	MonthlyBudgetMicros int64
	file                *tomlfile.File
}

func loadBirdConfig(path string) (birdConfig, error) {
	path = strings.TrimSpace(path)
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
