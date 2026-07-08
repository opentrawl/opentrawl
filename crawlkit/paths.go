package crawlkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/crawlkit/config"
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
	root := strings.TrimSpace(stateRoot)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return sourcePaths{}, err
		}
		root = filepath.Join(home, ".opentrawl")
	}
	root = config.ExpandHome(root)
	base := filepath.Join(root, sourceID)
	return sourcePaths{
		StateRoot: root,
		CrawlerID: sourceID,
		Base:      base,
		Paths: Paths{
			Archive: filepath.Join(base, sourceID+".db"),
			Config:  filepath.Join(base, "config.toml"),
			Logs:    filepath.Join(base, "logs"),
		},
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
