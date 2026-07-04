package cli

import "fmt"

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
			_, _ = fmt.Fprintf(r.stderr, "%s cannot filter by person yet\n", result.Source.ID)
			continue
		}
		if result.Err == nil {
			continue
		}
		remedy := fmt.Sprintf("run: trawl doctor %s", result.Source.ID)
		if logPath := sourceLogPath(result.Source); logPath != "" {
			remedy += "; read " + logPath
		}
		_, _ = fmt.Fprintf(r.stderr, "%s search failed: %s. Remedy: %s\n", result.Source.ID, r.failureDetail(result.Err), remedy)
	}
}

// failureReason is the stable token JSON and logs share: a source that
// hit the subprocess deadline is a timeout, anything else is an error
// (non-zero exit, spawn failure, unparseable output).
func failureReason(err error) string {
	if isTimeoutError(err) {
		return "timeout"
	}
	return "error"
}

// failureDetail is the human sentence for the same failure — the reason
// a person reads on stderr, never a machine ref or the child argv. It
// names the real deadline the source was held to.
func (r *Runtime) failureDetail(err error) string {
	if isTimeoutError(err) {
		return "timed out after " + r.timeout.String()
	}
	return "the crawler returned an error"
}

func failedSearchSources(results []searchSourceResult) []failedSource {
	var failures []failedSource
	for _, result := range results {
		if result.Err != nil {
			failures = append(failures, failedSource{
				Source: result.Source.ID,
				Reason: failureReason(result.Err),
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
