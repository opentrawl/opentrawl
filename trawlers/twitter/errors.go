package birdcrawl

import (
	"context"
	"errors"

	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
)

// usageRemedy is the one next-step hint for every caller mistake. It rides the
// error body's remedy field, kept out of the message (rules §2.4).
const usageRemedy = "Run 'trawl twitter --help'."

// cliError carries a command failure's exit code and the crawlkit error body
// (crawlkit/output). One shape: WriteJSONErrorIfNeeded renders it as
// {"error": {...}} in JSON mode; in text mode main prints Error().
type cliError struct {
	code    int
	name    string
	message string
	remedy  string
	fields  map[string]any
	err     error
}

func (e *cliError) Error() string {
	if e.remedy == "" {
		return e.message
	}
	return e.message + ". " + e.remedy
}

func (e *cliError) Unwrap() error { return e.err }

func (e *cliError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e *cliError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    e.name,
		Message: e.message,
		Remedy:  e.remedy,
		Fields:  e.fields,
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 1
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.code != 0 {
		return codeErr.code
	}
	return 1
}

// usageErr marks a caller mistake: exit 2, and rejected (not logged as crawler
// health) via isUsageError.
func usageErr(err error) error {
	return &cliError{code: 2, name: "usage", message: err.Error(), remedy: usageRemedy, err: err}
}

func isUsageError(err error) bool {
	var codeErr *cliError
	return errors.As(err, &codeErr) && codeErr.name == "usage"
}

// contractError is a command failure with a machine code, human message and
// remedy. WriteJSONErrorIfNeeded renders it as the {"error": {...}} envelope.
func (r *runtime) contractError(code, message, remedy string) error {
	return &cliError{
		code:    1,
		name:    code,
		message: message,
		remedy:  remedy,
		err:     cklog.WorldMustChange{Err: errors.New(message), Message: message, Remedy: remedy},
	}
}

// loggableError keeps the health log clean: it records a command failure's
// short machine message, never the rendered human text.
func loggableError(err error) error {
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.message != "" {
		return errors.New(codeErr.message)
	}
	return err
}
