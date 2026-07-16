package cli

import (
	"fmt"
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
	remedy       string
}

func (r *Runtime) reportFailedSourceFailure(source failedSource, verb, detail string) {
	display := firstNonEmpty(source.displayName, source.Source)
	_, _ = fmt.Fprintf(r.stderr, "%s %s failed: %s.\n", display, verb, detail)
	_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", firstNonEmpty(source.remedy, "retry with -v to see the log location"))
}

// failureReason is the stable token JSON and logs share: a source that
// hit the read deadline is a timeout, anything else is an error.
func failureReason(err error) string {
	if isTimeoutError(err) {
		return "timeout"
	}
	return "error"
}

func (r *Runtime) reasonDetail(reason string) string {
	if reason == "timeout" {
		return "timed out after " + r.timeout.String()
	}
	return "the crawler returned an error"
}
