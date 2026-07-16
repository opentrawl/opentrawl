package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

type OpenCmd struct {
	Ref string `arg:"" help:"Source-prefixed ref or short ref"`
}

func (c *OpenCmd) Run(r *Runtime) error {
	trimmed := strings.TrimSpace(c.Ref)
	sourceID, _, ok := splitOpenRef(trimmed)
	sources := discoverCrawlers(r.ctx)
	adapters := r.federationOpenSources(sources)
	if ok {
		source, found := findSource(sources, sourceID)
		if !found {
			return r.renderOpenResponse(r.canonicalOpen(adapters, sourceID, c.Ref, c.Ref))
		}
		return r.renderOpenResponse(r.canonicalOpen(adapters, source.ID, c.Ref, c.Ref))
	}
	if strings.Contains(trimmed, ":") {
		sourceID, _, _ := strings.Cut(trimmed, ":")
		if source, found := findSource(sources, sourceID); found {
			sourceID = source.ID
		}
		return r.renderOpenResponse(r.canonicalOpen(adapters, sourceID, c.Ref, c.Ref))
	}
	return r.openShortRef(sources, adapters, trimmed, c.Ref)
}

func (r *Runtime) renderOpenResponse(response *openv1.OpenResponse) error {
	if r.root.JSON {
		if err := writeCanonicalJSON(r.stdout, response); err != nil {
			return err
		}
		return outcomeExit(response.GetOutcome())
	}
	if response.GetFailure() != nil {
		failure := response.GetFailure()
		_, _ = fmt.Fprintf(r.stderr, "%s\n", strings.TrimSpace(failure.GetMessage()))
		if remedy := normalFailureRemedy(strings.TrimSpace(failure.GetRemedy()), "", "open"); remedy != "" {
			_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", remedy)
		}
		return exitErr{code: 1}
	}
	if response.GetRecord() == nil {
		return fmt.Errorf("open response has no record")
	}
	presentation := response.GetRecord().GetPresentation()
	if r.canonicalObserver != nil {
		r.canonicalObserver.observePresentation(presentation)
	}
	if err := renderPresentation(r.stdout, presentation); err != nil {
		return err
	}
	return outcomeExit(response.GetOutcome())
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
// broke instead of a failure misattributed to the first source.
type shortRefFailure struct {
	SourceID    string
	DisplayName string
	Err         error
}

var errAmbiguousShortRef = errors.New("ambiguous short ref")

func (r *Runtime) openShortRef(discovered []Source, adapters []federation.OpenSource, alias, requestedRef string) error {
	if !validShortRefAlias(alias) {
		if r.root.JSON {
			return r.renderOpenResponse(shortRefOpenFailure(requestedRef, federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT, fmt.Sprintf("Short ref %q is not valid.", alias), "short refs use 5 or more lowercase characters from 2-9 and abcdefghjkmnpqrstuvwxyz"))
		}
		return r.writeError("invalid_short_ref",
			fmt.Sprintf("Short ref %q is not valid.", alias),
			"short refs use 5 or more lowercase characters from 2-9 and abcdefghjkmnpqrstuvwxyz")
	}
	sources := shortRefSources(discovered)
	matches := make([]shortRefMatch, 0)
	seenRefs := map[string]bool{}
	var failures []shortRefFailure
	for _, source := range sources {
		refs, err := r.resolveSourceShortRef(adapters, source, requestedRef)
		if err != nil {
			if errors.Is(err, errAmbiguousShortRef) {
				if r.root.JSON {
					return r.renderOpenResponse(shortRefOpenFailure(requestedRef, federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT, fmt.Sprintf("Short ref %q matched more than one item.", alias), "rerun the search or use the full ref"))
				}
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
		return r.renderOpenResponse(r.canonicalOpen(adapters, matches[0].Source.ID, matches[0].Ref, requestedRef))
	case len(matches) > 1:
		if r.root.JSON {
			return r.renderOpenResponse(shortRefOpenFailure(requestedRef, federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT, fmt.Sprintf("Short ref %q matched more than one item.", alias), "rerun the search or use the full ref"))
		}
		return r.writeError("ambiguous_short_ref",
			fmt.Sprintf("Short ref %q matched more than one item.", alias),
			"rerun the search or use the full ref")
	case len(sources) > 0 && len(failures) == len(sources):
		// Every source failed for an unrelated reason: no source ever
		// actually answered resolved/unknown/ambiguous, so "not found"
		// would be dishonest. Report exactly what broke.
		return r.shortRefResolutionFailed(alias, requestedRef, failures)
	default:
		if r.root.JSON {
			return r.renderOpenResponse(shortRefOpenFailure(requestedRef, federationv1.FailureCode_FAILURE_CODE_NOT_FOUND, fmt.Sprintf("Short ref %q was not found.", alias), "use a full ref from trawl search"))
		}
		return r.writeError("unknown_short_ref",
			fmt.Sprintf("Short ref %q was not found.", alias),
			"use a full ref from trawl search")
	}
}

func shortRefOpenFailure(alias string, code federationv1.FailureCode, message, remedy string) *openv1.OpenResponse {
	return &openv1.OpenResponse{RequestedRef: alias, Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED, Failure: &federationv1.SourceFailure{Code: code, Message: message, Remedy: remedy}}
}

func (r *Runtime) shortRefResolutionFailed(alias, requestedRef string, failures []shortRefFailure) error {
	reasons := make([]string, 0, len(failures))
	for _, failure := range failures {
		reasons = append(reasons, fmt.Sprintf("%s (%s)", firstNonEmpty(failure.DisplayName, failure.SourceID), failure.Err))
	}
	message := fmt.Sprintf("Could not resolve short ref %q. Every source failed: %s.", alias, strings.Join(reasons, ", "))
	if r.root.JSON {
		return r.renderOpenResponse(shortRefOpenFailure(requestedRef, federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE, message, "retry with -v to see the log location"))
	}
	return r.writeError("short_ref_resolution_failed", message, "retry with -v to see the log location")
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

func (r *Runtime) resolveSourceShortRef(adapters []federation.OpenSource, source Source, requestedRef string) ([]string, error) {
	response := r.canonicalOpen(adapters, source.ID, requestedRef, requestedRef)
	if response.GetRecord() != nil {
		return []string{response.GetRecord().GetOpenRef()}, nil
	}
	failure := failureForOpenResponse(response)
	if isNotFoundFailure(failure) {
		return nil, nil
	}
	if isAmbiguousShortRefFailure(failure) {
		return nil, errAmbiguousShortRef
	}
	return nil, fmt.Errorf("%s: %s", failure.GetCode(), failure.GetMessage())
}
