package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "birdcrawl.db"
	}
	return filepath.Join(home, ".birdcrawl", "birdcrawl.db")
}

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".birdcrawl"
	}
	return filepath.Join(home, ".birdcrawl")
}

func logStateRoot(dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return defaultBaseDir()
	}
	dir := filepath.Dir(dbPath)
	if dir == "." || dir == "" {
		return defaultBaseDir()
	}
	return dir
}

func defaultLogDir() string {
	return filepath.Join(logStateRoot(defaultDBPath()), "birdcrawl", "logs")
}
