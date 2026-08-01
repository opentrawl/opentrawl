package trawlkit

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

type resolvedTrawlerArchivePaths struct {
	StateRoot         string
	RegisteredTrawler *RegisteredTrawlerIdentity
	Base              string
	TrawlerArchivePaths
}

func resolveTrawlerArchivePaths(stateRoot string, registeredTrawlerDeclaration RegisteredTrawlerDeclaration) (resolvedTrawlerArchivePaths, error) {
	registeredTrawlerIdentityText := RegisteredTrawlerIdentityText(registeredTrawlerDeclaration.RegisteredTrawler)
	if registeredTrawlerIdentityText == "" {
		return resolvedTrawlerArchivePaths{}, errors.New("registered trawler identity is required")
	}
	root, err := ResolveStateRoot(stateRoot)
	if err != nil {
		return resolvedTrawlerArchivePaths{}, err
	}
	base := filepath.Join(root, registeredTrawlerIdentityText)
	paths := TrawlerArchivePaths{
		TrawlerArchivePath:       filepath.Join(base, registeredTrawlerIdentityText+".db"),
		TrawlerConfigurationPath: TrawlerConfigurationFilePath(filepath.Join(base, "config.toml")),
		TrawlerLogDirectoryPath:  filepath.Join(base, "logs"),
	}
	if strings.TrimSpace(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerArchivePath) != "" {
		paths.TrawlerArchivePath = config.ExpandHome(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerArchivePath)
	}
	if strings.TrimSpace(string(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerConfigurationPath)) != "" {
		paths.TrawlerConfigurationPath = TrawlerConfigurationFilePath(config.ExpandHome(
			string(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerConfigurationPath),
		))
	}
	if strings.TrimSpace(registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerLogDirectoryPath) != "" {
		paths.TrawlerLogDirectoryPath = config.ExpandHome(
			registeredTrawlerDeclaration.DefaultTrawlerArchivePaths.TrawlerLogDirectoryPath,
		)
	}
	return resolvedTrawlerArchivePaths{
		StateRoot:           root,
		RegisteredTrawler:   registeredTrawlerDeclaration.RegisteredTrawler,
		Base:                base,
		TrawlerArchivePaths: paths,
	}, nil
}
