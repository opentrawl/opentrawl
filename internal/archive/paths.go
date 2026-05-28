package archive

import (
	"os"
	"path/filepath"
	"strings"
)

const DefaultArchiveRoot = ".photoscrawl"

type Paths struct {
	ArchiveRoot string
	Database    string
}

func PathsFromEnv() Paths {
	root := strings.TrimSpace(os.Getenv("PHOTOSCRAWL_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = "."
		}
		root = filepath.Join(home, DefaultArchiveRoot)
	}
	db := strings.TrimSpace(os.Getenv("PHOTOSCRAWL_DB"))
	if db == "" {
		db = filepath.Join(root, "photos.sqlite")
	}
	return Paths{ArchiveRoot: root, Database: db}
}
