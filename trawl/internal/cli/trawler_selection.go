package cli

import (
	"fmt"
	"strings"
)

func (r *Runtime) selectedTrawlerArguments(names []string) ([]InstalledTrawler, error) {
	return r.selectInstalledTrawlers(discoverInstalledTrawlers(r.ctx), names)
}

func (r *Runtime) selectInstalledTrawlers(installed []InstalledTrawler, names []string) ([]InstalledTrawler, error) {
	if len(names) == 0 {
		return installed, nil
	}
	selected := make([]InstalledTrawler, 0, len(names))
	for _, name := range names {
		source, ok := findInstalledTrawler(installed, name)
		if !ok {
			return nil, r.writeTrawlerNotFound(name)
		}
		selected = append(selected, source)
	}
	return selected, nil
}

func (r *Runtime) writeTrawlerNotFound(trawlerName string) error {
	return usageErr{fmt.Errorf("Unknown trawler %q.", trawlerName)}
}

func splitTrawlerCSV(trawlerCSV string) []string {
	parts := strings.Split(trawlerCSV, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// findInstalledTrawler matches an identity, command name, alias, or display name.
func findInstalledTrawler(installedTrawlers []InstalledTrawler, name string) (InstalledTrawler, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range installedTrawlers {
		if candidate.RegisteredTrawlerManifestIdentity == want || candidate.RegisteredTrawlerCommandName == want {
			return candidate, true
		}
		if strings.ToLower(strings.TrimSpace(candidate.RegisteredTrawlerCommandName)) == want {
			return candidate, true
		}
		if matchesAlias(candidate.RegisteredTrawlerAliases, want) {
			return candidate, true
		}
		if alias := trawlerDisplayNameAlias(candidate.RegisteredTrawlerDisplayName); alias != "" && alias == want {
			return candidate, true
		}
	}
	return InstalledTrawler{}, false
}

func matchesAlias(aliases []string, want string) bool {
	for _, alias := range aliases {
		if strings.ToLower(strings.TrimSpace(alias)) == want {
			return true
		}
	}
	return false
}

func trawlerDisplayNameAlias(displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if open := strings.LastIndex(displayName, "("); open >= 0 && strings.HasSuffix(displayName, ")") {
		return strings.ToLower(strings.TrimSpace(displayName[open+1 : len(displayName)-1]))
	}
	return strings.ToLower(strings.ReplaceAll(displayName, " ", ""))
}

func trawlerHumanName(source InstalledTrawler) string {
	return firstNonEmpty(source.RegisteredTrawlerDisplayName, source.RegisteredTrawlerCommandName, source.RegisteredTrawlerManifestIdentity)
}

func trawlerCommandToken(source InstalledTrawler) string {
	return firstNonEmpty(source.RegisteredTrawlerCommandName, source.RegisteredTrawlerManifestIdentity)
}

// trawlerDisplayNamesByIdentity maps trawler identities to display names.
func trawlerDisplayNamesByIdentity(installedTrawlers []InstalledTrawler) map[string]string {
	out := make(map[string]string, len(installedTrawlers))
	for _, source := range installedTrawlers {
		if name := strings.TrimSpace(trawlerHumanName(source)); name != "" {
			out[source.RegisteredTrawlerManifestIdentity] = name
		}
	}
	return out
}
