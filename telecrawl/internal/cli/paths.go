package cli

import (
	"os"
	"path/filepath"
)

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "telecrawl.db"
	}
	return filepath.Join(home, ".telecrawl", "telecrawl.db")
}

func defaultLogDir() string {
	return filepath.Join(defaultBaseDir(), "logs")
}

func defaultLogPath() string {
	return filepath.Join(defaultLogDir(), telecrawlLogFileName)
}

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".telecrawl"
	}
	return filepath.Join(home, ".telecrawl")
}
