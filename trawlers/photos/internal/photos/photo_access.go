package photos

import (
	"errors"
	"fmt"
)

type PhotoLibraryAccessError struct {
	Status string
}

func (e *PhotoLibraryAccessError) Error() string {
	switch e.Status {
	case "not_determined":
		return "Photos access has not been granted to Photoscrawl Fetch; open Photoscrawl Fetch in Applications, approve the macOS prompt, then retry"
	case "denied":
		return "Photos access is denied for Photoscrawl Fetch; enable Photoscrawl Fetch in System Settings > Privacy & Security > Photos, then retry"
	case "restricted":
		return "Photos access is restricted by macOS for Photoscrawl Fetch"
	default:
		return fmt.Sprintf("Photos access is %s for Photoscrawl Fetch", e.Status)
	}
}

func IsPhotoLibraryAccessError(err error) bool {
	var accessErr *PhotoLibraryAccessError
	return errors.As(err, &accessErr)
}
