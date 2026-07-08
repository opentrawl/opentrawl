package repo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	crawlconfig "github.com/openclaw/crawlkit/config"
)

const (
	DefaultConfigEnv = "CONTACTS_CONFIG"
	RepoEnv          = "CONTACTS_REPO"
	DefaultRemote    = ""
)

type Config struct {
	Version  int       `toml:"version" json:"version"`
	RepoPath string    `toml:"repo_path" json:"repo_path"`
	Git      GitConfig `toml:"git" json:"git"`
	Repair   Repair    `toml:"repair" json:"repair"`
	Google   Google    `toml:"google" json:"google"`
}

type GitConfig struct {
	Remote string `toml:"remote" json:"remote"`
	Branch string `toml:"branch" json:"branch"`
}

type Repair struct {
	BackupBeforeRepair bool `toml:"backup_before_repair" json:"backup_before_repair"`
}

type Google struct {
	DefaultAccount string `toml:"default_account" json:"default_account"`
}

var appConfig = crawlconfig.App{Name: "contacts", ConfigEnv: DefaultConfigEnv, BaseDir: "~/.opentrawl/contacts"}

func DefaultConfig() Config {
	paths, err := appConfig.DefaultPaths()
	if err != nil {
		home, _ := os.UserHomeDir()
		paths.ShareDir = filepath.Join(home, ".opentrawl", "contacts", "share")
	}
	return Config{
		Version:  1,
		RepoPath: paths.ShareDir,
		Git: GitConfig{
			Remote: DefaultRemote,
			Branch: "main",
		},
		Repair: Repair{
			BackupBeforeRepair: true,
		},
	}
}

func ResolveConfigPath(flagPath string) string {
	path, err := appConfig.ResolveConfigPath(flagPath)
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".opentrawl", "contacts", "config.toml")
	}
	return path
}

func DefaultLogDir() string {
	paths, err := appConfig.DefaultPaths()
	if err != nil {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".opentrawl", "contacts", "logs")
	}
	return paths.LogDir
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if _, err := os.Stat(crawlconfig.ExpandHome(path)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.Normalize()
			return cfg, nil
		}
		return Config{}, err
	}
	if err := crawlconfig.LoadTOML(path, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Normalize()
	return cfg, nil
}

func WriteConfig(path string, cfg Config) error {
	cfg.Normalize()
	return crawlconfig.WriteTOML(path, cfg, 0o600)
}

func (c *Config) Normalize() {
	def := DefaultConfig()
	if c.Version == 0 {
		c.Version = 1
	}
	if strings.TrimSpace(c.RepoPath) == "" {
		c.RepoPath = def.RepoPath
	}
	c.RepoPath = crawlconfig.ExpandHome(c.RepoPath)
	if strings.TrimSpace(c.Git.Remote) == "" {
		c.Git.Remote = def.Git.Remote
	}
	if strings.TrimSpace(c.Git.Branch) == "" {
		c.Git.Branch = "main"
	}
}

func ResolveRepoPath(flagRepo string, cfg Config) (string, error) {
	switch {
	case strings.TrimSpace(flagRepo) != "":
		return crawlconfig.ExpandHome(flagRepo), nil
	case strings.TrimSpace(os.Getenv(RepoEnv)) != "":
		return crawlconfig.ExpandHome(os.Getenv(RepoEnv)), nil
	case strings.TrimSpace(cfg.RepoPath) != "":
		return crawlconfig.ExpandHome(cfg.RepoPath), nil
	default:
		return "", errors.New("contacts repo not configured; run trawl contacts init DIR, set repo_path, or set CONTACTS_REPO")
	}
}
