package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

type App struct {
	Name         string
	ConfigEnv    string
	BaseDir      string
	PlatformDirs bool
}

type Paths struct {
	BaseDir    string `toml:"base_dir" json:"base_dir"`
	ConfigPath string `toml:"config_path" json:"config_path"`
	DBPath     string `toml:"db_path" json:"db_path"`
	CacheDir   string `toml:"cache_dir" json:"cache_dir"`
	LogDir     string `toml:"log_dir" json:"log_dir"`
	ShareDir   string `toml:"share_dir" json:"share_dir"`
}

type RuntimeConfig struct {
	Version  int    `toml:"version" json:"version"`
	DBPath   string `toml:"db_path" json:"db_path"`
	CacheDir string `toml:"cache_dir" json:"cache_dir"`
	LogDir   string `toml:"log_dir" json:"log_dir"`
	ShareDir string `toml:"share_dir" json:"share_dir"`
}

type TokenDiagnostic struct {
	Env     string `json:"env"`
	Present bool   `json:"present"`
	Source  string `json:"source,omitempty"`
}

var xdgMu sync.Mutex

func (a App) Normalize() (App, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return App{}, errors.New("app name is required")
	}
	if a.ConfigEnv == "" {
		a.ConfigEnv = strings.ToUpper(strings.ReplaceAll(a.Name, "-", "_")) + "_CONFIG"
	}
	if a.BaseDir == "" && !a.PlatformDirs {
		home, err := os.UserHomeDir()
		if err != nil {
			return App{}, err
		}
		a.BaseDir = filepath.Join(home, ".config", a.Name)
	}
	return a, nil
}

func (a App) DefaultPaths() (Paths, error) {
	app, err := a.Normalize()
	if err != nil {
		return Paths{}, err
	}
	paths, err := app.defaultPaths()
	if err != nil {
		return Paths{}, err
	}
	return paths, nil
}

func (app App) defaultPaths() (Paths, error) {
	if app.PlatformDirs && strings.TrimSpace(app.BaseDir) == "" {
		return platformPaths(app.Name)
	}
	base := ExpandHome(app.BaseDir)
	return Paths{
		BaseDir:    base,
		ConfigPath: filepath.Join(base, "config.toml"),
		DBPath:     filepath.Join(base, app.Name+".db"),
		CacheDir:   filepath.Join(base, "cache"),
		LogDir:     filepath.Join(base, "logs"),
		ShareDir:   filepath.Join(base, "share"),
	}, nil
}

func (a App) ResolveConfigPath(flagPath string) (string, error) {
	app, err := a.Normalize()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(flagPath) != "" {
		return ExpandHome(flagPath), nil
	}
	if envPath := strings.TrimSpace(os.Getenv(app.ConfigEnv)); envPath != "" {
		return ExpandHome(envPath), nil
	}
	paths, err := app.defaultPaths()
	if err != nil {
		return "", err
	}
	return paths.ConfigPath, nil
}

func (a App) DefaultRuntimeConfig() (RuntimeConfig, error) {
	paths, err := a.DefaultPaths()
	if err != nil {
		return RuntimeConfig{}, err
	}
	return RuntimeConfig{
		Version:  1,
		DBPath:   paths.DBPath,
		CacheDir: paths.CacheDir,
		LogDir:   paths.LogDir,
		ShareDir: paths.ShareDir,
	}, nil
}

func ApplyRuntimeDefaults(cfg *RuntimeConfig, defaults RuntimeConfig) {
	if cfg.Version == 0 {
		cfg.Version = defaults.Version
	}
	if cfg.DBPath == "" {
		cfg.DBPath = defaults.DBPath
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = defaults.CacheDir
	}
	if cfg.LogDir == "" {
		cfg.LogDir = defaults.LogDir
	}
	if cfg.ShareDir == "" {
		cfg.ShareDir = defaults.ShareDir
	}
	cfg.DBPath = ExpandHome(cfg.DBPath)
	cfg.CacheDir = ExpandHome(cfg.CacheDir)
	cfg.LogDir = ExpandHome(cfg.LogDir)
	cfg.ShareDir = ExpandHome(cfg.ShareDir)
}

func EnsureRuntimeDirs(cfg RuntimeConfig) error {
	for _, path := range []string{filepath.Dir(cfg.DBPath), cfg.CacheDir, cfg.LogDir, cfg.ShareDir} {
		if strings.TrimSpace(path) == "" || path == "." {
			continue
		}
		if err := os.MkdirAll(ExpandHome(path), 0o755); err != nil {
			return fmt.Errorf("create runtime dir %s: %w", path, err)
		}
	}
	return nil
}

func platformPaths(name string) (Paths, error) {
	xdgMu.Lock()
	defer xdgMu.Unlock()

	configHome, dataHome, cacheHome, stateHome, err := platformHomes()
	if err != nil {
		return Paths{}, err
	}
	dataDir := filepath.Join(dataHome, name)
	configDir := filepath.Join(configHome, name)
	cacheDir := filepath.Join(cacheHome, name)
	stateDir := filepath.Join(stateHome, name)
	return Paths{
		BaseDir:    dataDir,
		ConfigPath: filepath.Join(configDir, "config.toml"),
		DBPath:     filepath.Join(dataDir, name+".db"),
		CacheDir:   cacheDir,
		LogDir:     filepath.Join(stateDir, "logs"),
		ShareDir:   filepath.Join(dataDir, "share"),
	}, nil
}

func platformHomes() (configHome, dataHome, cacheHome, stateHome string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve user home for platform dirs: %w", err)
	}
	if !filepath.IsAbs(home) {
		return "", "", "", "", fmt.Errorf("resolve user home for platform dirs: %q is not absolute", home)
	}
	switch runtime.GOOS {
	case "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support")
		configHome = absoluteEnv("XDG_CONFIG_HOME", appSupport)
		dataHome = absoluteEnv("XDG_DATA_HOME", appSupport)
		cacheHome = absoluteEnv("XDG_CACHE_HOME", filepath.Join(home, "Library", "Caches"))
		stateHome = absoluteEnv("XDG_STATE_HOME", appSupport)
	case "windows":
		localAppData := absoluteEnv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		configHome = absoluteEnv("XDG_CONFIG_HOME", localAppData)
		dataHome = absoluteEnv("XDG_DATA_HOME", localAppData)
		cacheHome = absoluteEnv("XDG_CACHE_HOME", filepath.Join(localAppData, "cache"))
		stateHome = absoluteEnv("XDG_STATE_HOME", localAppData)
	default:
		configHome = absoluteEnv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		dataHome = absoluteEnv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		cacheHome = absoluteEnv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		stateHome = absoluteEnv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	}
	return configHome, dataHome, cacheHome, stateHome, nil
}

func absoluteEnv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" || !filepath.IsAbs(value) {
		return fallback
	}
	return value
}

func LoadTOML(path string, dst any) error {
	data, err := os.ReadFile(ExpandHome(path))
	if err != nil {
		return err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := toml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse toml %s: %w", path, err)
	}
	return nil
}

func WriteTOML(path string, src any, perm os.FileMode) error {
	resolved := ExpandHome(path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := toml.Marshal(src)
	if err != nil {
		return fmt.Errorf("marshal toml: %w", err)
	}
	if perm == 0 {
		perm = 0o600
	}
	return os.WriteFile(resolved, data, perm)
}

func TokenDiagnosticForEnv(env string) TokenDiagnostic {
	env = strings.TrimSpace(env)
	if env == "" {
		return TokenDiagnostic{}
	}
	_, present := os.LookupEnv(env)
	source := ""
	if present {
		source = "env"
	}
	return TokenDiagnostic{Env: env, Present: present, Source: source}
}

func ExpandHome(path string) string {
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
