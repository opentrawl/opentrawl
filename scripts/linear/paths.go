package main

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/config"
)

const linearLogFileName = "linear.log"

func linearPaths() (config.Paths, error) {
	return config.App{Name: "linear", BaseDir: "~/.opentrawl/linear"}.DefaultPaths()
}

func linearTokenPath() (string, error) {
	paths, err := linearPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.BaseDir, "token.json"), nil
}

func linearLogPath() (string, error) {
	paths, err := linearPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.BaseDir, linearLogFileName), nil
}
