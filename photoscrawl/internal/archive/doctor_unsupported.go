//go:build !darwin

package archive

import (
	"os"
	"path/filepath"
)

func sourceStoreChecks(string) []DoctorCheck {
	return []DoctorCheck{
		{ID: "source_store", State: "unsupported", Message: "not supported on this platform"},
		{ID: "full_disk_access", State: "unsupported", Message: "not supported on this platform"},
	}
}

func DefaultPhotosLibraryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(home, "Pictures", "Photos Library.photoslibrary"))
}
