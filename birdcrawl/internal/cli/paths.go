package cli

import (
	"path/filepath"
	"strings"

	"github.com/openclaw/crawlkit/config"
)

// birdcrawlPaths is the one path layout, from crawlkit/config. The base dir
// is the fleet-wide state root, ~/.opentrawl/birdcrawl (TRAWL-99).
func birdcrawlPaths() config.Paths {
	paths, _ := config.App{Name: "birdcrawl", BaseDir: "~/.opentrawl/birdcrawl"}.DefaultPaths()
	return paths
}

func defaultDBPath() string {
	return birdcrawlPaths().DBPath
}

func defaultBaseDir() string {
	return birdcrawlPaths().BaseDir
}

// logStateRoot is where cklog puts its <root>/birdcrawl/logs tree: next to a
// caller-supplied database, or at the fleet root for the default layout so
// logs land in ~/.opentrawl/birdcrawl/logs.
func logStateRoot(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" || dbPath == defaultDBPath() {
		return filepath.Dir(defaultBaseDir())
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return filepath.Dir(defaultBaseDir())
	}
	return dir
}

func defaultLogDir() string {
	return birdcrawlPaths().LogDir
}
