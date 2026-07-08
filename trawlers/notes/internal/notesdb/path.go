package notesdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const storeFile = "NoteStore.sqlite"

func DefaultStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home directory is unavailable: %w", err)
	}
	if home == "" {
		return "", errors.New("home directory is unavailable")
	}
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.notes", storeFile), nil
}

func StorePathInContainer(dir string) string {
	return filepath.Join(dir, storeFile)
}
