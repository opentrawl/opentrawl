//go:build !darwin

package archive

import (
	"os"
	"path/filepath"
)

func DefaultPhotosLibraryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, "Pictures", "Photos Library.photoslibrary"))
}
