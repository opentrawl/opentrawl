package cli

import (
	"os"
	"path/filepath"
	"strings"
)

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "telecrawl.db"
	}
	return filepath.Join(home, ".telecrawl", "telecrawl.db")
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
	return filepath.Join(logStateRoot(defaultDBPath()), "telecrawl", "logs")
}

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecrawl"
	}
	return filepath.Join(home, ".telecrawl")
}
