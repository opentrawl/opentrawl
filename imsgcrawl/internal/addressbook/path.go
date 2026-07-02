package addressbook

import (
	"os"
	"path/filepath"
	"sort"
)

func DefaultStorePaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	root := filepath.Join(home, "Library", "Application Support", "AddressBook")
	paths := make([]string, 0, 2)
	main := filepath.Join(root, "AddressBook-v22.abcddb")
	if regularFile(main) {
		paths = append(paths, main)
	}
	sourcePaths, err := filepath.Glob(filepath.Join(root, "Sources", "*", "AddressBook-v22.abcddb"))
	if err == nil {
		sort.Strings(sourcePaths)
		for _, path := range sourcePaths {
			if regularFile(path) {
				paths = append(paths, path)
			}
		}
	}
	return dedupePaths(paths)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func dedupePaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
