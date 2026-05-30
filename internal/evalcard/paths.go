package evalcard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func normalizeOllamaGenerateURL(raw string) string {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return DefaultOllamaGenerateURL
	}
	if strings.HasSuffix(value, "/api/generate") {
		return value
	}
	if strings.HasSuffix(value, "/api") {
		return value + "/generate"
	}
	return value + "/api/generate"
}

func defaultedOutputDir(value string, now func() time.Time) (string, error) {
	if strings.TrimSpace(value) != "" {
		return filepath.Abs(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".photoscrawl", "evals", now().UTC().Format("2006-01-02-150405")+"-photo-card"), nil
}

func defaultedCacheDir(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return filepath.Abs(value)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".photoscrawl", "cache", "originals"), nil
}

func rejectRepoPath(path string) error {
	root, ok := findGitRoot()
	if !ok {
		return nil
	}
	absPath, err := comparablePath(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
		return fmt.Errorf("%s is inside repo %s", absPath, root)
	}
	return nil
}

func comparablePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	probe := absPath
	for {
		if resolved, err := filepath.EvalSymlinks(probe); err == nil {
			rel, err := filepath.Rel(probe, absPath)
			if err != nil || rel == "." {
				return resolved, nil
			}
			return filepath.Join(resolved, rel), nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return absPath, nil
		}
		probe = parent
	}
}

func findGitRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				return resolved, true
			}
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func safeName(value string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", " ", "_", "\\", "_")
	value = replacer.Replace(strings.TrimSpace(value))
	if value == "" {
		return "default"
	}
	return value
}
