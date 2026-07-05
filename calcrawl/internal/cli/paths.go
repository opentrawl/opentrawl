package cli

import (
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

func defaultLogDir() string {
	return archive.DefaultPaths().LogDir
}

func logPathParts(logDir string) (string, string) {
	baseDir := filepath.Dir(logDir)
	stateRoot := filepath.Dir(baseDir)
	crawlerID := filepath.Base(baseDir)
	if strings.TrimSpace(crawlerID) == "" || crawlerID == "." || crawlerID == string(filepath.Separator) {
		return baseDir, "calcrawl"
	}
	return stateRoot, crawlerID
}
