package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".calcrawl"
	}
	return filepath.Join(home, ".calcrawl")
}

func defaultLogDir() string {
	return filepath.Join(defaultBaseDir(), "logs")
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
