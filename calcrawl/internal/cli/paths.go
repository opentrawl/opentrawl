package cli

import (
	"os"
	"path/filepath"
)

func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".calcrawl"
	}
	return filepath.Join(home, ".calcrawl")
}
