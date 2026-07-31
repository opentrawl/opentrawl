package trawlkit

import (
	"errors"
	"fmt"
	"strings"
)

func selectTrawler(args []string, trawlers []Trawler) (Trawler, []string, error) {
	if len(trawlers) == 0 {
		return nil, nil, usageError{err: errors.New("no trawlers are registered")}
	}
	if len(trawlers) == 1 {
		trawler := trawlers[0]
		if len(args) > 0 && matchesTrawler(trawler.RegisteredTrawlerDeclaration(), args[0]) {
			return trawler, args[1:], nil
		}
		return trawler, args, nil
	}
	if len(args) == 0 {
		return nil, nil, usageError{err: errors.New("trawler is required")}
	}
	var matches []Trawler
	for _, trawler := range trawlers {
		if matchesTrawler(trawler.RegisteredTrawlerDeclaration(), args[0]) {
			matches = append(matches, trawler)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil, usageError{err: fmt.Errorf("unknown trawler %q", args[0])}
	case 1:
		return matches[0], args[1:], nil
	default:
		return nil, nil, usageError{err: fmt.Errorf("ambiguous trawler %q matches %s", args[0], trawlerManifestIdentities(matches))}
	}
}

func matchesTrawler(registeredTrawlerDeclaration RegisteredTrawlerDeclaration, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	if token == RegisteredTrawlerIdentityText(registeredTrawlerDeclaration.RegisteredTrawler) ||
		token == registeredTrawlerDeclaration.RegisteredTrawlerCommandName {
		return true
	}
	for _, alias := range registeredTrawlerDeclaration.RegisteredTrawlerAliases {
		if token == strings.TrimSpace(alias) {
			return true
		}
	}
	return false
}

func trawlerManifestIdentities(trawlers []Trawler) string {
	identities := make([]string, 0, len(trawlers))
	for _, trawler := range trawlers {
		registeredTrawlerDeclaration := trawler.RegisteredTrawlerDeclaration()
		identities = append(
			identities,
			firstText(
				RegisteredTrawlerIdentityText(registeredTrawlerDeclaration.RegisteredTrawler),
				registeredTrawlerDeclaration.RegisteredTrawlerCommandName,
			),
		)
	}
	return strings.Join(identities, ", ")
}
