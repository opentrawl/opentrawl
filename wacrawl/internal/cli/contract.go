package cli

import (
	"errors"
	"fmt"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/wacrawl/internal/store"
)

// newContractError builds a command failure that renders as the one
// {"error": {...}} envelope (crawlkit/output) in JSON mode via cliError's
// ErrorBody, and as human (a plain message and remedy, or a who table) in text
// mode. fields carries structured extras (candidates, did_you_mean, hint); the
// wrapped WorldMustChange keeps the log line the short machine message, never
// the rendered table (rules §2.6).
func newContractError(code, message, remedy string, exitCode int, fields map[string]any, human string) error {
	return &cliError{
		code:    exitCode,
		name:    code,
		message: message,
		remedy:  remedy,
		fields:  fields,
		human:   human,
		err:     cklog.WorldMustChange{Err: errors.New(message), Message: message, Remedy: remedy},
	}
}

// contractError is the common exit-1 contract failure: no structured fields,
// a plain message-and-remedy text rendering.
func contractError(code, message, remedy string) error {
	return newContractError(code, message, remedy, 1, nil, genericContractText(message, remedy))
}

// genericContractText is the text-mode rendering for a contract error with no
// table: the message and remedy on their own lines (rules §2.4). main adds the
// trailing newline.
func genericContractText(message, remedy string) string {
	return fmt.Sprintf("%s.\n%s.", message, remedy)
}

func ambiguousWhoText(message, remedy string, candidates []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s.\n\n", message)
	_ = writeWhoTable(&out, candidates)
	fmt.Fprintf(&out, "\n%s", remedy)
	return out.String()
}

func unknownWhoText(message, remedy, hint string, didYouMean []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s.\n", message)
	if len(didYouMean) > 0 {
		out.WriteString("\nDid you mean:\n")
		_ = writeWhoTable(&out, didYouMean)
	}
	if hint != "" {
		fmt.Fprintf(&out, "%s.\n", hint)
	}
	fmt.Fprintf(&out, "%s.", remedy)
	return out.String()
}
