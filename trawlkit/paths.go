package trawlkit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

type resolvedTrawlerArchivePaths struct {
	StateRoot                         string
	RegisteredTrawlerManifestIdentity string
	Base                              string
	TrawlerArchivePaths
}

func resolveTrawlerArchivePaths(stateRoot string, registeredTrawlerDeclaration RegisteredTrawlerDeclaration) (resolvedTrawlerArchivePaths, error) {
	registeredTrawlerManifestIdentity := strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerManifestIdentity)
	if registeredTrawlerManifestIdentity == "" {
		return resolvedTrawlerArchivePaths{}, errors.New("registered trawler manifest identity is required")
	}
	root, err := ResolveStateRoot(stateRoot)
	if err != nil {
		return resolvedTrawlerArchivePaths{}, err
	}
	base := filepath.Join(root, registeredTrawlerManifestIdentity)
	paths := TrawlerArchivePaths{
		TrawlerArchivePath:       filepath.Join(base, registeredTrawlerManifestIdentity+".db"),
		TrawlerConfigurationPath: filepath.Join(base, "config.toml"),
		TrawlerLogDirectoryPath:  filepath.Join(base, "logs"),
	}
	if strings.TrimSpace(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerArchivePath) != "" {
		paths.TrawlerArchivePath = config.ExpandHome(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerArchivePath)
	}
	if strings.TrimSpace(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerConfigurationPath) != "" {
		paths.TrawlerConfigurationPath = config.ExpandHome(
			registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerConfigurationPath,
		)
	}
	if strings.TrimSpace(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerLogDirectoryPath) != "" {
		paths.TrawlerLogDirectoryPath = config.ExpandHome(
			registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerLogDirectoryPath,
		)
	}
	return resolvedTrawlerArchivePaths{
		StateRoot:                         root,
		RegisteredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
		Base:                              base,
		TrawlerArchivePaths:               paths,
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
