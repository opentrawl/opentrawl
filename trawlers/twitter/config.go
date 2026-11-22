package twitter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/twitter/internal/tomlfile"
	"github.com/opentrawl/opentrawl/twitter/internal/xapi"
	"github.com/opentrawl/opentrawl/twitter/internal/xweb"
)

const defaultMonthlyBudgetUSDMicros = int64(10_000_000)

// TwitterSyncSource identifies which live transport updates the Twitter archive.
// OpenTrawl never falls back silently between free web-session cookies and paid
// official X API calls.
type TwitterSyncSource string

const (
	TwitterSyncSourceUnset       TwitterSyncSource = ""
	TwitterSyncSourceWebSession  TwitterSyncSource = "web_session"
	TwitterSyncSourceOfficialAPI TwitterSyncSource = "official_api"
)

var ErrAmbiguousTwitterSyncSource = errors.New("both X web session and official API credentials exist; set source = \"web_session\" or source = \"official_api\" in config.toml")

type TwitterCrawlerConfiguration struct {
	Source           string `toml:"source"`
	Handle           string `toml:"handle"`
	UserID           string `toml:"user_id"`
	MonthlyBudgetUSD string `toml:"monthly_budget_usd"`
}

func (c TwitterCrawlerConfiguration) Validate() error {
	if _, err := parseTwitterSyncSource(c.Source); err != nil {
		return err
	}
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

func parseTwitterSyncSource(raw string) (TwitterSyncSource, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return TwitterSyncSourceUnset, nil
	case string(TwitterSyncSourceWebSession):
		return TwitterSyncSourceWebSession, nil
	case string(TwitterSyncSourceOfficialAPI):
		return TwitterSyncSourceOfficialAPI, nil
	default:
		return TwitterSyncSourceUnset, fmt.Errorf("source must be %q or %q", TwitterSyncSourceWebSession, TwitterSyncSourceOfficialAPI)
	}
}

type birdConfig struct {
	Path                string
	Source              TwitterSyncSource
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
	source, err := parseTwitterSyncSource(file.Get("source"))
	if err != nil {
		return birdConfig{}, err
	}
	cfg := birdConfig{
		Path:                path,
		Source:              source,
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

func (c *birdConfig) SaveSourceAndIdentity(source TwitterSyncSource, userID, handle string) error {
	if source != TwitterSyncSourceUnset {
		c.Source = source
		c.file.Set("source", string(source))
	}
	if trimmed := strings.TrimSpace(userID); trimmed != "" {
		c.UserID = trimmed
		c.file.Set("user_id", c.UserID)
	}
	if trimmed := strings.TrimPrefix(strings.TrimSpace(handle), "@"); trimmed != "" {
		c.Handle = trimmed
		c.file.Set("handle", c.Handle)
	}
	return c.file.WriteAtomic(c.Path, 0o600)
}

func (c birdConfig) MonthlyBudgetUSD() float64 {
	return float64(c.MonthlyBudgetMicros) / 1_000_000
}

func (c birdConfig) SessionPath() string {
	return xweb.DefaultWebSessionPath(c.Path)
}

func (c birdConfig) OfficialAPICredentialsPath() string {
	if strings.TrimSpace(c.Path) != "" {
		return filepath.Join(filepath.Dir(c.Path), "credentials.toml")
	}
	return xapi.DefaultCredentialsPath()
}

// ResolveSyncSource picks the configured live sync source without silently
// crossing between free web-session cookies and paid official X API calls.
func (c birdConfig) ResolveSyncSource() (TwitterSyncSource, error) {
	if c.Source != TwitterSyncSourceUnset {
		return c.Source, nil
	}
	hasSession := xweb.WebSessionPresent(c.SessionPath())
	hasAPI := xapi.CredentialsPresent(c.OfficialAPICredentialsPath())
	switch {
	case hasSession && hasAPI:
		return TwitterSyncSourceUnset, ErrAmbiguousTwitterSyncSource
	case hasSession:
		return TwitterSyncSourceWebSession, nil
	case hasAPI:
		return TwitterSyncSourceOfficialAPI, nil
	default:
		return TwitterSyncSourceUnset, nil
	}
}
