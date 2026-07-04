package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/config"
)

// defaultPaths is the one path layout, from crawlkit/config. The base dir
// stays ~/.telecrawl (the layout shipped before config adoption).
func defaultPaths() config.Paths {
	paths, _ := config.App{Name: "telecrawl", BaseDir: "~/.telecrawl"}.DefaultPaths()
	return paths
}

func defaultDBPath() string {
	return defaultPaths().DBPath
}

func defaultLogDir() string {
	return defaultPaths().LogDir
}

func defaultLogPath() string {
	return filepath.Join(defaultLogDir(), telecrawlLogFileName)
}
