package cli

import (
	"fmt"

	cklog "github.com/openclaw/crawlkit/log"
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
	if a.json {
		if err := a.print(errorEnvelope{Error: contractErr}); err != nil {
			return err
		}
	} else {
		_ = a.printContractError(contractErr)
	}
	failure := &contractFailure{contractError: contractErr}
	return &cliError{code: exitCode, err: cklog.WorldMustChange{Err: failure, Message: contractErr.Message, Remedy: contractErr.Remedy}}
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
	_, err := fmt.Fprintf(a.stderr, "%s. %s.\n", contractErr.Message, contractErr.Remedy)
	return err
}
