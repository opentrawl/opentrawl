package cli

import (
	"encoding/json"
	"errors"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
)

type cliError struct {
	code  int
	err   error
	quiet bool
	event string
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	return 1
}

func ShouldPrintError(err error) bool {
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return !codeErr.quiet
	}
	return err != nil
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err, event: "usage_error"}
}

func (r *runtime) contractError(code, message, remedy string) error {
	body := contractErrorBody{Code: code, Message: message, Remedy: remedy}
	err := newRemediedError(message, remedy)
	if r.json {
		if printErr := json.NewEncoder(r.stdout).Encode(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: 1, err: err, quiet: true, event: code}
	}
	return &cliError{code: 1, err: err, event: code}
}

type remediedError struct {
	message string
	remedy  string
}

func newRemediedError(message, remedy string) error {
	return remediedError{message: strings.TrimSpace(message), remedy: strings.TrimSpace(remedy)}
}

func (e remediedError) Error() string {
	if e.message != "" && e.remedy != "" {
		return e.message + ". " + e.remedy
	}
	if e.message != "" {
		return e.message
	}
	return e.remedy
}

func (e remediedError) Unwrap() error {
	return cklog.WorldMustChange{Err: errors.New(e.message), Message: e.message, Remedy: e.remedy}
}
