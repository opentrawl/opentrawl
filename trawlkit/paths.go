package trawlkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

type sourcePaths struct {
	StateRoot string
	CrawlerID string
	Base      string
	Paths
}

func resolveSourcePaths(stateRoot string, info Info) (sourcePaths, error) {
	sourceID := strings.TrimSpace(info.ID)
	if sourceID == "" {
		return sourcePaths{}, errors.New("source id is required")
	}
	root, err := ResolveStateRoot(stateRoot)
	if err != nil {
		return sourcePaths{}, err
	}
	base := filepath.Join(root, sourceID)
	paths := Paths{
		Archive: filepath.Join(base, sourceID+".db"),
		Config:  filepath.Join(base, "config.toml"),
		Logs:    filepath.Join(base, "logs"),
	}
	if strings.TrimSpace(info.DefaultPaths.Archive) != "" {
		paths.Archive = config.ExpandHome(info.DefaultPaths.Archive)
	}
	if strings.TrimSpace(info.DefaultPaths.Config) != "" {
		paths.Config = config.ExpandHome(info.DefaultPaths.Config)
	}
	if strings.TrimSpace(info.DefaultPaths.Logs) != "" {
		paths.Logs = config.ExpandHome(info.DefaultPaths.Logs)
	}
	return sourcePaths{
		StateRoot: root,
		CrawlerID: sourceID,
		Base:      base,
		Paths:     paths,
	}, nil
}

func pathExists(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}
