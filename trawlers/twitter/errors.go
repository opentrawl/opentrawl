package twitter

import (
	"context"
	"errors"

	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

// cliError carries a command failure's exit code and typed description.
type cliError struct {
	code    int
	name    string
	message string
	err     error
}

func (e *cliError) Error() string {
	return e.message
}

func (e *cliError) Unwrap() error { return e.err }

func (e *cliError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e *cliError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    e.name,
		Message: e.message,
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
	return &cliError{code: 2, name: "usage", message: err.Error(), err: err}
}

// contractError is a command failure with a machine code and human message.
func (r *runtime) contractError(code, message string) error {
	return &cliError{
		code:    1,
		name:    code,
		message: message,
		err:     errors.New(message),
	}
}

// loggableError keeps the health log clean: it records a command failure's
// short machine message, never the rendered human text.
