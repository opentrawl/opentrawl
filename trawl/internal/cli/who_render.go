package cli

// The who resolution error surfaces: what a reader (or agent) sees
// when --who matched nobody, matched too many people, or some sources
// could not answer.

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type whoResolutionErrorEnvelope struct {
	Error            ErrorBody      `json:"error"`
	TotalCandidates  int            `json:"total_candidates,omitempty"`
	Candidates       []WhoCandidate `json:"candidates,omitempty"`
	TotalDidYouMean  int            `json:"total_did_you_mean,omitempty"`
	DidYouMean       []WhoCandidate `json:"did_you_mean,omitempty"`
	SourcesConsulted []string       `json:"sources_consulted"`
	SkippedSources   []string       `json:"skipped_sources,omitempty"`
	Hint             string         `json:"hint,omitempty"`
}

func reportSkippedWhoSources(w io.Writer, skipped []string) {
	for _, source := range skipped {
		_, _ = fmt.Fprintf(w, "%s cannot filter by person yet\n", source)
	}
}

func renderWhoResolutionLine(w io.Writer, input string, candidate WhoCandidate, surfaces map[string]string) error {
	_, err := fmt.Fprintf(w, "%s → %s (%s)\n", input, candidate.Who, whoSources(candidate.Sources, surfaces))
	return err
}

func (r *Runtime) writeAmbiguousWho(query, input string, resolution federatedWhoResolution, skipped []string, surfaces map[string]string) error {
	if r.root.JSON {
		_ = writeJSON(r.stdout, whoResolutionErrorEnvelope{
			Error: ErrorBody{
				Code:    "ambiguous_who",
				Message: fmt.Sprintf("Who %q matched more than one person.", input),
				Remedy:  "retry with one identifier from candidates",
			},
			TotalCandidates:  len(resolution.Candidates),
			Candidates:       capWhoCandidates(resolution.Candidates, jsonWhoCandidateLimit),
			SourcesConsulted: resolution.SourcesConsulted,
			SkippedSources:   skipped,
		})
		return exitErr{code: 4}
	}
	reportSkippedWhoSources(r.stderr, skipped)
	_, _ = fmt.Fprintf(r.stderr, "Who %q matched more than one person. Search was not run.\n", input)
	_ = renderWhoTable(r.stderr, resolution.Candidates, surfaces)
	_, _ = fmt.Fprintf(r.stderr, "Retry example: %s\n", searchRetryExample(query, resolution.Candidates[0]))
	return exitErr{code: 4}
}

func (r *Runtime) writeUnknownWho(query, input string, resolution federatedWhoResolution, skipped []string, surfaces map[string]string) error {
	hint := searchWithoutWhoHint(query)
	if r.root.JSON {
		_ = writeJSON(r.stdout, whoResolutionErrorEnvelope{
			Error: ErrorBody{
				Code:    "unknown_who",
				Message: fmt.Sprintf("No person matched %q.", input),
				Remedy:  "retry with a suggestion or search without --who",
			},
			TotalDidYouMean:  len(resolution.DidYouMean),
			DidYouMean:       capWhoCandidates(resolution.DidYouMean, jsonWhoCandidateLimit),
			SourcesConsulted: resolution.SourcesConsulted,
			SkippedSources:   skipped,
			Hint:             hint,
		})
		return exitErr{code: 5}
	}
	reportSkippedWhoSources(r.stderr, skipped)
	_, _ = fmt.Fprintf(r.stderr, "No person matched %q. Search was not run.\n", input)
	if len(resolution.DidYouMean) > 0 {
		_, _ = fmt.Fprintln(r.stderr, "Did you mean:")
		_ = renderWhoTable(r.stderr, resolution.DidYouMean, surfaces)
	}
	_, _ = fmt.Fprintf(r.stderr, "Hint: %s\n", hint)
	return exitErr{code: 5}
}

func searchRetryExample(query string, candidate WhoCandidate) string {
	parts := []string{"trawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteExampleArg(query))
	}
	parts = append(parts, "--who", quoteExampleArg(whoFilterValue(candidate)))
	return strings.Join(parts, " ")
}

func searchWithoutWhoHint(query string) string {
	if strings.TrimSpace(query) == "" {
		return "search again without --who to list matching items"
	}
	return "run " + strings.Join([]string{"trawl", "search", quoteExampleArg(query)}, " ") + " without --who"
}

func quoteExampleArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return strconv.Quote(value)
	}
	return value
}
