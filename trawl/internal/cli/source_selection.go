package cli

import (
	"fmt"
	"strings"
)

func (r *Runtime) selectedSourcesCSV(sourceCSV string) ([]Source, error) {
	sourceCSV = strings.TrimSpace(sourceCSV)
	if sourceCSV == "" {
		return r.selectedSourceArgs(nil)
	}
	return r.selectedSourceArgs(splitSourceCSV(sourceCSV))
}

func (r *Runtime) selectedSourceArgs(names []string) ([]Source, error) {
	sources := discoverCrawlers(r.ctx, r.appsDir)
	if len(names) == 0 {
		return sources, nil
	}
	selected := make([]Source, 0, len(names))
	for _, name := range names {
		source, ok := findSource(sources, name)
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
		"install the crawler on PATH or add a drop-in manifest in ~/.trawl/apps")
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

// findSource matches an id, a binary name, or the human name of the
// surface — "imessage" and "gmail" work the way people say them.
func findSource(sources []Source, name string) (Source, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range sources {
		if candidate.ID == want || candidate.Binary == want {
			return candidate, true
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
