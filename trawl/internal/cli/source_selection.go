package cli

import (
	"fmt"
	"strings"
)

func (r *Runtime) selectedSourceArgs(names []string) ([]Source, error) {
	return r.selectSources(discoverCrawlers(r.ctx), names)
}

func (r *Runtime) selectSources(installed []Source, names []string) ([]Source, error) {
	if len(names) == 0 {
		return installed, nil
	}
	selected := make([]Source, 0, len(names))
	for _, name := range names {
		source, ok := findSource(installed, name)
		if !ok {
			return nil, r.writeSourceNotFound(name)
		}
		selected = append(selected, source)
	}
	return selected, nil
}

func (r *Runtime) writeSourceNotFound(source string) error {
	return r.writeError("source_not_found",
		fmt.Sprintf("Source %q was not found.", source),
		"run trawl status")
}

func splitSourceCSV(sourceCSV string) []string {
	parts := strings.Split(sourceCSV, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// findSource matches an id, compiled crawler name, declared human alias
// (birdcrawl's "twitter"), the human surface name, or a legacy binary name —
// so "imessage", "x", "twitter" and "gmail" all resolve the way people type
// them.
func findSource(sources []Source, name string) (Source, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range sources {
		if candidate.ID == want || candidate.Binary == want {
			return candidate, true
		}
		if strings.ToLower(strings.TrimSpace(candidate.Surface)) == want {
			return candidate, true
		}
		if matchesAlias(candidate.Aliases, want) || matchesAlias(legacyRoutingAliases(candidate.ID), want) {
			return candidate, true
		}
		if alias := sourceAlias(candidate.DisplayName); alias != "" && alias == want {
			return candidate, true
		}
	}
	return Source{}, false
}

func matchesAlias(aliases []string, want string) bool {
	for _, alias := range aliases {
		if strings.ToLower(strings.TrimSpace(alias)) == want {
			return true
		}
	}
	return false
}

func sourceAlias(displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if open := strings.LastIndex(displayName, "("); open >= 0 && strings.HasSuffix(displayName, ")") {
		return strings.ToLower(strings.TrimSpace(displayName[open+1 : len(displayName)-1]))
	}
	return strings.ToLower(strings.ReplaceAll(displayName, " ", ""))
}

func sourceHumanName(source Source) string {
	return firstNonEmpty(source.DisplayName, source.Surface, source.ID)
}

func sourceCommandToken(source Source) string {
	return firstNonEmpty(source.ID, source.Surface, source.Binary)
}

// surfaceNames maps source ids to the human surface name (Gmail,
// iMessage) so data cells never show module names.
func surfaceNames(sources []Source) map[string]string {
	out := make(map[string]string, len(sources))
	for _, source := range sources {
		if name := strings.TrimSpace(sourceHumanName(source)); name != "" {
			out[source.ID] = name
		}
	}
	return out
}
