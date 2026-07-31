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
		trawler, ok := findInstalledTrawler(installed, name)
		if !ok {
			return nil, r.writeTrawlerNotFound(name)
		}
		selected = append(selected, trawler)
	}
	return selected, nil
}

func (r *Runtime) writeTrawlerNotFound(trawlerName string) error {
	return usageErr{humanFacingUsageErrorMessage(fmt.Sprintf("Unknown trawler %q.", trawlerName))}
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
		if installedTrawlerIdentityText(candidate) == want || candidate.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName() == want {
			return candidate, true
		}
		if strings.ToLower(strings.TrimSpace(candidate.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName())) == want {
			return candidate, true
		}
		if matchesAlias(candidate.RegisteredTrawlerManifest.GetRegisteredTrawlerAliases(), want) {
			return candidate, true
		}
		if alias := trawlerDisplayNameAlias(candidate.RegisteredTrawlerManifest.GetRegisteredTrawlerDisplayName()); alias != "" && alias == want {
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

func trawlerHumanName(trawler InstalledTrawler) string {
	return firstNonEmpty(
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerDisplayName(),
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName(),
		installedTrawlerIdentityText(trawler),
	)
}

func trawlerCommandToken(trawler InstalledTrawler) string {
	return strings.TrimSpace(trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName())
}

// trawlerDisplayNamesByIdentity maps trawler identities to display names.
func trawlerDisplayNamesByIdentity(installedTrawlers []InstalledTrawler) map[string]string {
	out := make(map[string]string, len(installedTrawlers))
	for _, trawler := range installedTrawlers {
		if name := strings.TrimSpace(trawlerHumanName(trawler)); name != "" {
			out[installedTrawlerIdentityText(trawler)] = name
		}
	}
	return out
}
