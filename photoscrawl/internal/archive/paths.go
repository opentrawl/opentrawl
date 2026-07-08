package archive

import (
	"path/filepath"

	crawlconfig "github.com/openclaw/crawlkit/config"
)

var runtimeApp = crawlconfig.App{Name: "photoscrawl", BaseDir: "~/.opentrawl/photoscrawl"}

type Paths struct {
	ConfigPath string
	DataDir    string
	Database   string
	CacheDir   string
	LogDir     string
	ShareDir   string
}

func DefaultPaths() (Paths, error) {
	defaults, err := runtimeApp.DefaultPaths()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ConfigPath: defaults.ConfigPath,
		DataDir:    defaults.BaseDir,
		Database:   defaults.DBPath,
		CacheDir:   defaults.CacheDir,
		LogDir:     defaults.LogDir,
		ShareDir:   defaults.ShareDir,
	}, nil
}

func (p Paths) EvalRootDir() string {
	return filepath.Join(p.DataDir, "evals")
}

func (p Paths) OriginalsCacheDir() string {
	return filepath.Join(p.CacheDir, "originals")
}

func (p Paths) PlaceContextCacheDir() string {
	return filepath.Join(p.CacheDir, "place-context")
}

func (p Paths) PlaceBackfillDir() string {
	return filepath.Join(p.DataDir, "backfills", "place-context-full", "apple-ingest")
}
