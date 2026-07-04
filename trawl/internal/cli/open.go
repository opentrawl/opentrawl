package cli

import (
	"context"
	"errors"
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

var errAmbiguousShortRef = errors.New("ambiguous short ref")

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
			if errors.Is(err, errAmbiguousShortRef) {
				return r.writeError("ambiguous_short_ref",
					fmt.Sprintf("Short ref %q matched more than one item.", alias),
					"rerun the search or use the full ref")
			}
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
	data, err := runCrawlerJSONWithArgs(ctx, source.Path, "open", alias)
	if err != nil {
		switch shortRefErrorCode(data) {
		case "unknown_short_ref":
			return []string{}, nil
		case "ambiguous_short_ref":
			return nil, errAmbiguousShortRef
		}
		return nil, err
	}
	ref, err := decodeShortRefOpenRef(data)
	if err != nil {
		return nil, err
	}
	return []string{ref}, nil
}

func shortRefErrorCode(data []byte) string {
	var envelope ErrorEnvelope
	if err := decodeContractJSON(data, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Error.Code)
}

func decodeShortRefOpenRef(data []byte) (string, error) {
	var raw struct {
		Ref string `json:"ref"`
	}
	if err := decodeContractJSON(data, &raw); err != nil {
		return "", err
	}
	ref := strings.TrimSpace(raw.Ref)
	if ref == "" {
		return "", errors.New("open ref is missing")
	}
	return ref, nil
}
