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

func (r *Runtime) selectedSource(source string) (Source, error) {
	sources, err := r.selectedSourceArgs([]string{source})
	if err != nil {
		return Source{}, err
	}
	return sources[0], nil
}

func (r *Runtime) writeSourceNotFound(source string) error {
	return r.writeError("source_not_found",
		fmt.Sprintf("Source %q was not found.", source),
		"run: trawl status")
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

// findSource matches an id, compiled crawler name, explicit alias, or the
// human surface name — "imessage" and "gmail" work the way people say them.
func findSource(sources []Source, name string) (Source, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range sources {
		if candidate.ID == want || candidate.Binary == want {
			return candidate, true
		}
		if strings.ToLower(strings.TrimSpace(candidate.Surface)) == want {
			return candidate, true
		}
		for _, alias := range candidate.Aliases {
			if strings.ToLower(strings.TrimSpace(alias)) == want {
				return candidate, true
			}
		}
		if alias := sourceAlias(candidate.DisplayName); alias != "" && alias == want {
			return candidate, true
		}
	}
	return Source{}, false
}

func sourceAlias(displayName string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(displayName), " ", ""))
}

// surfaceNames maps source ids to the human surface name (Gmail,
// iMessage) so data cells never show module names.
func surfaceNames(sources []Source) map[string]string {
	out := make(map[string]string, len(sources))
	for _, source := range sources {
		if name := strings.TrimSpace(source.DisplayName); name != "" {
			out[source.ID] = name
		}
	}
	return out
}
