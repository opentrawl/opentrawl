package cli

import (
	"fmt"
	"io"

	"github.com/openclaw/crawlkit/render"
)

// Federated search fans out to every source and merges the survivors.
// A source that drops out — a timeout under fan-out, a non-zero exit,
// unparseable output — is never silently dropped: it is named in the
// JSON envelope, on stderr, and in the exit code, all from one shared
// vocabulary (timeout | error) so the three surfaces never disagree.

// failedSource names a source that dropped out of the fan-out and why.
// It rides at the top of the JSON envelope (before results) so a
// dropped source is never a trailing field on an otherwise normal body.
type failedSource struct {
	Source string `json:"source"`
	Reason string `json:"reason"`

	displayName  string
	commandToken string
}

// searchExit is the deterministic exit contract: the same failure shape
// always yields the same code. 0 = every source answered, 3 = some
// answered and some dropped (partial), 1 = every source dropped.
func searchExit(results []searchSourceResult) error {
	failures := 0
	successes := 0
	for _, result := range results {
		if result.Err != nil || result.Skipped {
			failures++
			continue
		}
		successes++
	}
	if failures == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func searchSuccesses(results []searchSourceResult) int {
	successes := 0
	for _, result := range results {
		if result.Err == nil && !result.Skipped {
			successes++
		}
	}
	return successes
}

func (r *Runtime) reportSearchFailures(results []searchSourceResult) {
	for _, result := range results {
		if result.Skipped {
			_, _ = fmt.Fprintf(r.stderr, "%s cannot filter by person yet\n", sourceHumanName(result.Source))
			continue
		}
		if result.Err == nil {
			continue
		}
		r.reportSourceFailure(result.Source, "search", r.failureDetail(result.Err))
	}
}

// reportSourceFailure is the one stderr shape for a source that
// dropped out of a verb: what failed on one line, the remedy on its
// own line below it.
func (r *Runtime) reportSourceFailure(source Source, verb, detail string) {
	_, _ = fmt.Fprintf(r.stderr, "%s %s failed: %s.\n", sourceHumanName(source), verb, detail)
	_, _ = fmt.Fprintf(r.stderr, "  Remedy: run trawl doctor %s\n", sourceCommandToken(source))
}

func (r *Runtime) reportFailedSourceFailure(source failedSource, verb, detail string) {
	display := firstNonEmpty(source.displayName, source.Source)
	command := firstNonEmpty(source.commandToken, source.Source)
	_, _ = fmt.Fprintf(r.stderr, "%s %s failed: %s.\n", display, verb, detail)
	_, _ = fmt.Fprintf(r.stderr, "  Remedy: run trawl doctor %s\n", command)
}

// failureReason is the stable token JSON and logs share: a source that
// hit the read deadline is a timeout, anything else is an error.
func failureReason(err error) string {
	if isTimeoutError(err) {
		return "timeout"
	}
	return "error"
}

// failureDetail is the human sentence for the same failure — the reason
// a person reads on stderr, never a machine ref or an internal argv. It
// names the real deadline the source was held to.
func (r *Runtime) failureDetail(err error) string {
	return r.reasonDetail(failureReason(err))
}

func (r *Runtime) reasonDetail(reason string) string {
	if reason == "timeout" {
		return "timed out after " + r.timeout.String()
	}
	return "the crawler returned an error"
}

func failedSearchSources(results []searchSourceResult) []failedSource {
	var failures []failedSource
	for _, result := range results {
		if result.Err != nil {
			failures = append(failures, failedSource{
				Source:       result.Source.ID,
				Reason:       failureReason(result.Err),
				displayName:  sourceHumanName(result.Source),
				commandToken: sourceCommandToken(result.Source),
			})
		}
	}
	return failures
}

func skippedSearchSources(results []searchSourceResult) []string {
	var skipped []string
	for _, result := range results {
		if result.Skipped {
			skipped = append(skipped, result.Source.ID)
		}
	}
	return skipped
}

func renderSearchPartialNote(w io.Writer, results []searchSourceResult) error {
	failures := len(failedSearchSources(results))
	skipped := len(skippedSearchSources(results))
	blocked := failures + skipped
	if blocked == 0 || blocked == len(results) {
		return nil
	}
	reason := "unavailable"
	if skipped > 0 && failures == 0 {
		reason = "skipped"
	} else if skipped > 0 {
		reason = "skipped or unavailable"
	}
	_, err := fmt.Fprintf(w, "note: %s of %s sources %s - results are partial (see stderr)\n", render.FormatInteger(int64(blocked)), render.FormatInteger(int64(len(results))), reason)
	return err
}
