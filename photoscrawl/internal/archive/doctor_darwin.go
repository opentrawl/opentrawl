//go:build darwin

package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	crawlconfig "github.com/openclaw/crawlkit/config"
)

const libraryPathRemedy = "set library_path in ~/.opentrawl/photoscrawl/config.toml"

func sourceStoreChecks(libraryPath string) []DoctorCheck {
	path, err := photosLibraryPath(libraryPath)
	if err != nil {
		check := DoctorCheck{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot resolve Photos library path",
			Remedy:  libraryPathRemedy,
		}
		return []DoctorCheck{check, checkWithID(check, "full_disk_access")}
	}
	return []DoctorCheck{
		sourceStoreCheck(path),
		fullDiskAccessCheck(path),
	}
}

func DefaultPhotosLibraryPath() (string, error) {
	return photosLibraryPath("")
}

func photosLibraryPath(libraryPath string) (string, error) {
	path := crawlconfig.ExpandHome(strings.TrimSpace(libraryPath))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	}
	return filepath.Abs(path)
}

func sourceStoreCheck(libraryPath string) DoctorCheck {
	info, err := os.Stat(libraryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DoctorCheck{
				ID:      "source_store",
				State:   "missing",
				Message: "Photos library not found",
				Remedy:  libraryPathRemedy,
			}
		}
		if os.IsPermission(err) {
			return DoctorCheck{
				ID:      "source_store",
				State:   "fail",
				Message: "cannot access Photos library",
				Remedy:  fullDiskAccessRemedy,
			}
		}
		return DoctorCheck{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot inspect Photos library",
			Remedy:  libraryPathRemedy,
		}
	}
	if !info.IsDir() {
		return DoctorCheck{
			ID:      "source_store",
			State:   "fail",
			Message: "Photos library path is not a directory",
			Remedy:  libraryPathRemedy,
		}
	}
	return DoctorCheck{ID: "source_store", State: "ok"}
}

func fullDiskAccessCheck(libraryPath string) DoctorCheck {
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	file, err := os.Open(dbPath)
	if err == nil {
		_ = file.Close()
		return DoctorCheck{ID: "full_disk_access", State: "ok"}
	}
	if os.IsNotExist(err) {
		return DoctorCheck{
			ID:      "full_disk_access",
			State:   "missing",
			Message: "Photos database not found",
			Remedy:  libraryPathRemedy,
		}
	}
	if os.IsPermission(err) || errors.Is(err, os.ErrPermission) {
		return DoctorCheck{
			ID:      "full_disk_access",
			State:   "fail",
			Message: "cannot read the Photos database",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return DoctorCheck{
		ID:      "full_disk_access",
		State:   "fail",
		Message: "cannot inspect the Photos database",
		Remedy:  libraryPathRemedy,
	}
}

func checkWithID(check DoctorCheck, id string) DoctorCheck {
	check.ID = id
	return check
}
