package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
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
			"refs look like <source>:<path>, for example imessage:msg/8842")
	}
	return r.openShortRef(ref)
}

func (r *Runtime) openWithSource(source Source, ref string) error {
	if source.MetadataErr != nil {
		return r.openFailed(ref, source)
	}
	opener, ok := source.Crawler.(trawlkit.Opener)
	if !ok {
		return r.openFailed(ref, source)
	}
	started := r.logSourceStart(source, "open")
	err := r.withSourceRequest(source, "open", sourceStoreRead, outputFormat(r.root.JSON), r.stdout, func(ctx context.Context, req *trawlkit.Request) error {
		return opener.Open(ctx, req, ref)
	})
	r.logSourceDone(source, "open", started, err)
	if err != nil {
		return r.openFailed(ref, source)
	}
	return nil
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

func (r *Runtime) openFailed(ref string, source Source) error {
	return r.writeError("open_failed",
		fmt.Sprintf("%s could not open ref %q.", sourceHumanName(source), ref),
		fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source)))
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

// shortRefFailure is a source that errored for a reason unrelated to
// the short-refs.md contract (not unknown_short_ref, not
// ambiguous_short_ref) — a crawler crash, a bad short_refs
// implementation, a timeout. It never aborts the fan-out on its own;
// the other sources still get asked. It only surfaces if every source
// in the fan-out fails this way, so the caller learns exactly what
// broke instead of a misattributed "trawl doctor <first source>".
type shortRefFailure struct {
	SourceID    string
	DisplayName string
	Err         error
}

var errAmbiguousShortRef = errors.New("ambiguous short ref")

func (r *Runtime) openShortRef(alias string) error {
	if !validShortRefAlias(alias) {
		return r.writeError("invalid_short_ref",
			fmt.Sprintf("Short ref %q is not valid.", alias),
			"short refs use 5 or more lowercase characters from 2-9 and abcdefghjkmnpqrstuvwxyz")
	}
	sources := shortRefSources(discoverCrawlers(r.ctx))
	matches := make([]shortRefMatch, 0)
	seenRefs := map[string]bool{}
	var failures []shortRefFailure
	for _, source := range sources {
		refs, err := r.resolveSourceShortRef(source, alias)
		if err != nil {
			if errors.Is(err, errAmbiguousShortRef) {
				return r.writeError("ambiguous_short_ref",
					fmt.Sprintf("Short ref %q matched more than one item.", alias),
					"rerun the search or use the full ref")
			}
			// Unrelated failure: note it and keep asking the remaining
			// sources. A source erroring must never be mistaken for
			// that source saying "not found".
			r.logInfo("short_ref_source_failed", strings.Join([]string{
				sourceField(source),
				"alias=" + logQuote(alias),
				"error=" + logQuote(err.Error()),
			}, " "))
			failures = append(failures, shortRefFailure{SourceID: source.ID, DisplayName: sourceHumanName(source), Err: err})
			continue
		}
		for _, ref := range refs {
			if seenRefs[ref] {
				continue
			}
			seenRefs[ref] = true
			matches = append(matches, shortRefMatch{Source: source, Ref: ref})
		}
	}
	switch {
	case len(matches) == 1:
		return r.openWithSource(matches[0].Source, matches[0].Ref)
	case len(matches) > 1:
		return r.writeError("ambiguous_short_ref",
			fmt.Sprintf("Short ref %q matched more than one item.", alias),
			"rerun the search or use the full ref")
	case len(sources) > 0 && len(failures) == len(sources):
		// Every source failed for an unrelated reason: no source ever
		// actually answered resolved/unknown/ambiguous, so "not found"
		// would be dishonest. Report exactly what broke.
		return r.shortRefResolutionFailed(alias, failures)
	default:
		return r.writeError("unknown_short_ref",
			fmt.Sprintf("Short ref %q was not found.", alias),
			"use a full ref from trawl search --json")
	}
}

func (r *Runtime) shortRefResolutionFailed(alias string, failures []shortRefFailure) error {
	reasons := make([]string, 0, len(failures))
	for _, failure := range failures {
		reasons = append(reasons, fmt.Sprintf("%s (%s)", firstNonEmpty(failure.DisplayName, failure.SourceID), failure.Err))
	}
	return r.writeError("short_ref_resolution_failed",
		fmt.Sprintf("Could not resolve short ref %q. Every source failed: %s.", alias, strings.Join(reasons, ", ")),
		"run trawl doctor")
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

func (r *Runtime) resolveSourceShortRef(source Source, alias string) ([]string, error) {
	opener, ok := source.Crawler.(trawlkit.Opener)
	if !ok {
		return nil, fmt.Errorf("source does not support open")
	}
	var data bytes.Buffer
	err := r.withSourceRequest(source, "open", sourceStoreRead, outputFormat(true), &data, func(ctx context.Context, req *trawlkit.Request) error {
		return opener.Open(ctx, req, alias)
	})
	if err != nil {
		body := sourceErrorBody(err)
		switch body.Code {
		case "unknown_short_ref":
			return nil, nil
		case "ambiguous_short_ref":
			return nil, errAmbiguousShortRef
		}
		return nil, fmt.Errorf("%s: %s", body.Code, body.Message)
	}
	if envelope, ok := shortRefErrorEnvelope(data.Bytes()); ok {
		switch envelope.Error.Code {
		case "unknown_short_ref":
			return nil, nil
		case "ambiguous_short_ref":
			return nil, errAmbiguousShortRef
		}
		return nil, fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	ref, err := decodeShortRefOpenRef(data.Bytes())
	if err != nil {
		return nil, err
	}
	return []string{ref}, nil
}

func shortRefErrorEnvelope(data []byte) (ErrorEnvelope, bool) {
	var envelope ErrorEnvelope
	if err := decodeContractJSON(data, &envelope); err != nil {
		return ErrorEnvelope{}, false
	}
	envelope.Error.Code = strings.TrimSpace(envelope.Error.Code)
	if envelope.Error.Code == "" {
		return ErrorEnvelope{}, false
	}
	return envelope, true
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
