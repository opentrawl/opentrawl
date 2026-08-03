package photos

import (
	"errors"
	"strings"
)

func (identifier PhotosLibraryDatabaseUUID) Validate() error {
	value := strings.TrimSpace(string(identifier))
	if value == "" {
		return errors.New("Photos library database UUID is required")
	}
	return nil
}

func SourceLibraryID(identifier PhotosLibraryDatabaseUUID) (string, error) {
	if err := identifier.Validate(); err != nil {
		return "", err
	}
	canonicalIdentifier := strings.ToUpper(strings.TrimSpace(string(identifier)))
	return "source_library:" + canonicalIdentifier, nil
}
