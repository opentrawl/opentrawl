package cli

import (
	"fmt"

	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/wacrawl/internal/store"
)

type errorEnvelope struct {
	Error contractError `json:"error"`
}

type contractError struct {
	Code       string                `json:"code"`
	Message    string                `json:"message"`
	Remedy     string                `json:"remedy"`
	Candidates []store.WhoCandidate  `json:"candidates,omitempty"`
	DidYouMean *[]store.WhoCandidate `json:"did_you_mean,omitempty"`
	Hint       string                `json:"hint,omitempty"`
}

type contractFailure struct {
	contractError
}

func (e *contractFailure) Error() string {
	return e.Message
}

func (a *app) failContract(contractErr contractError) error {
	return a.failContractWithExit(contractErr, 1)
}

func (a *app) failContractWithExit(contractErr contractError, exitCode int) error {
	failure := &contractFailure{contractError: contractErr}
	err := commandErr(contractErr.Code, contractErr.Message, contractErr.Remedy, exitCode, contractErr.fields(), cklog.WorldMustChange{Err: failure, Message: contractErr.Message, Remedy: contractErr.Remedy})
	if a.json {
		return err
	}
	// Human mode prints here, once; the error returns marked rendered
	// so main's fallback does not print it a second time.
	_ = a.printContractError(contractErr)
	return ckoutput.Rendered(err)
}

func (e contractError) fields() map[string]any {
	fields := map[string]any{}
	if len(e.Candidates) > 0 {
		fields["candidates"] = e.Candidates
	}
	if e.DidYouMean != nil {
		fields["did_you_mean"] = *e.DidYouMean
	}
	if e.Hint != "" {
		fields["hint"] = e.Hint
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func (a *app) printContractError(contractErr contractError) error {
	if contractErr.Code == "ambiguous_who" {
		if _, err := fmt.Fprintf(a.stderr, "%s.\n\n", contractErr.Message); err != nil {
			return err
		}
		if err := writeWhoCandidateTable(a.stderr, contractErr.Candidates, terminalColumns()); err != nil {
			return err
		}
		_, err := fmt.Fprintf(a.stderr, "\n%s\n", contractErr.Remedy)
		return err
	}
	if contractErr.Code == "unknown_who" {
		if _, err := fmt.Fprintf(a.stderr, "%s.\n", contractErr.Message); err != nil {
			return err
		}
		if contractErr.DidYouMean != nil && len(*contractErr.DidYouMean) > 0 {
			if _, err := fmt.Fprintln(a.stderr, "\nDid you mean:"); err != nil {
				return err
			}
			if err := writeWhoCandidateTable(a.stderr, *contractErr.DidYouMean, terminalColumns()); err != nil {
				return err
			}
		}
		if contractErr.Hint != "" {
			if _, err := fmt.Fprintf(a.stderr, "%s.\n", contractErr.Hint); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(a.stderr, "%s.\n", contractErr.Remedy)
		return err
	}
	// Message and remedy on separate lines: the hint never rides the
	// data line (design bar), and the message stays the frozen string.
	_, err := fmt.Fprintf(a.stderr, "%s.\n%s.\n", contractErr.Message, contractErr.Remedy)
	return err
}
