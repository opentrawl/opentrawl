package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

type OpenCmd struct {
	Ref string `arg:"" help:"Source-prefixed ref or short ref"`
}

func (c *OpenCmd) Run(r *Runtime) error {
	ref := strings.TrimSpace(c.Ref)
	sourceID, _, ok := splitOpenRef(ref)
	if ok {
		source, err := r.selectedSource(sourceID)
		if err != nil {
			return err
		}
		return r.openWithSource(source, ref)
	}
	if strings.Contains(ref, ":") {
		return r.writeError("invalid_ref",
			"Ref is missing a source or path.",
			"refs look like <source>:<path>, for example imsgcrawl:msg/8842")
	}
	return r.openShortRef(ref)
}

func (r *Runtime) openWithSource(source Source, ref string) error {
	if !r.root.JSON {
		return runCrawlerCommandPassThroughWithTimeout(r.ctx, source.Path, crawlerCommandTimeout, r.stdout, r.stderr, "open", ref)
	}
	data, err := runCrawlerJSONWithArgs(r.ctx, source.Path, "open", ref)
	if err != nil {
		return r.openFailed(ref, source.ID)
	}
	var payload any
	if err := decodeContractJSON(data, &payload); err != nil {
		return r.openFailed(ref, source.ID)
	}
	_, err = r.stdout.Write(data)
	return err
}

func splitOpenRef(ref string) (string, string, bool) {
	source, path, found := strings.Cut(ref, ":")
	if !found {
		return "", "", false
	}
	source = strings.TrimSpace(source)
	path = strings.TrimSpace(path)
	if source == "" || path == "" {
		return "", "", false
	}
	return source, path, true
}

func (r *Runtime) openFailed(ref, source string) error {
	return r.writeError("open_failed",
		fmt.Sprintf("Could not open ref %q.", ref),
		fmt.Sprintf("run: trawl doctor %s", source))
}

const (
	shortRefMinLength = 5
	shortRefMaxLength = 52
	shortRefAlphabet  = "23456789abcdefghjkmnpqrstuvwxyz"
)

type shortRefMatch struct {
	Source Source
	Ref    string
}

type shortRefObject struct {
	Ref     string `json:"ref,omitempty"`
	FullRef string `json:"full_ref,omitempty"`
}

func (r *Runtime) openShortRef(alias string) error {
	if !validShortRefAlias(alias) {
		return r.writeError("invalid_short_ref",
			fmt.Sprintf("Short ref %q is not valid.", alias),
			"short refs use 5 or more lowercase characters from 2-9 and abcdefghjkmnpqrstuvwxyz")
	}
	sources := shortRefSources(discoverCrawlers(r.ctx, r.appsDir))
	matches := make([]shortRefMatch, 0)
	seenRefs := map[string]bool{}
	for _, source := range sources {
		refs, err := resolveSourceShortRef(r.ctx, source, alias)
		if err != nil {
			return r.writeError("short_ref_resolution_failed",
				fmt.Sprintf("Could not resolve short ref %q.", alias),
				fmt.Sprintf("run: trawl doctor %s", source.ID))
		}
		for _, ref := range refs {
			if seenRefs[ref] {
				continue
			}
			seenRefs[ref] = true
			matches = append(matches, shortRefMatch{Source: source, Ref: ref})
		}
	}
	switch len(matches) {
	case 0:
		return r.writeError("unknown_short_ref",
			fmt.Sprintf("Short ref %q was not found.", alias),
			"use a full ref from trawl search --json")
	case 1:
		return r.openWithSource(matches[0].Source, matches[0].Ref)
	default:
		return r.writeError("ambiguous_short_ref",
			fmt.Sprintf("Short ref %q matched more than one item.", alias),
			"rerun the search or use the full ref")
	}
}

func validShortRefAlias(alias string) bool {
	if len(alias) < shortRefMinLength || len(alias) > shortRefMaxLength {
		return false
	}
	for _, char := range alias {
		if !strings.ContainsRune(shortRefAlphabet, char) {
			return false
		}
	}
	return true
}

func shortRefSources(sources []Source) []Source {
	out := make([]Source, 0, len(sources))
	for _, source := range sources {
		if source.MetadataErr == nil && hasCapability(source, "short_refs") {
			out = append(out, source)
		}
	}
	return out
}

func resolveSourceShortRef(ctx context.Context, source Source, alias string) ([]string, error) {
	data, err := runCrawlerJSONWithArgs(ctx, source.Path, "short-ref", alias)
	if err != nil {
		return nil, err
	}
	return decodeShortRefRefs(data)
}

func decodeShortRefRefs(data []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		var refs []string
		if err := decodeContractJSON(trimmed, &refs); err != nil {
			return nil, err
		}
		return uniqueRefs(refs), nil
	}
	var raw struct {
		Ref      string           `json:"ref,omitempty"`
		FullRef  string           `json:"full_ref,omitempty"`
		Refs     []string         `json:"refs,omitempty"`
		FullRefs []string         `json:"full_refs,omitempty"`
		Matches  []shortRefObject `json:"matches,omitempty"`
		Results  []shortRefObject `json:"results,omitempty"`
	}
	if err := decodeContractJSON(data, &raw); err != nil {
		return nil, err
	}
	refs := append([]string(nil), raw.Refs...)
	refs = append(refs, raw.FullRefs...)
	refs = append(refs, raw.Ref, raw.FullRef)
	for _, match := range raw.Matches {
		refs = append(refs, firstNonEmpty(match.Ref, match.FullRef))
	}
	for _, result := range raw.Results {
		refs = append(refs, firstNonEmpty(result.Ref, result.FullRef))
	}
	return uniqueRefs(refs), nil
}

func uniqueRefs(refs []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}
