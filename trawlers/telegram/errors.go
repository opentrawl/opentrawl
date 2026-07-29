package telegram

import (
	"errors"

	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

type commandError struct {
	code    int
	name    string
	message string
	err     error
}

func (e commandError) Error() string {
	return e.message
}

func (e commandError) Unwrap() error {
	return e.err
}

func (e commandError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e commandError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    e.name,
		Message: e.message,
	}
}

func usageErr(err error) error {
	return ckoutput.UsageError{Err: err}
}

func archiveErr(err error) error {
	return commandErr(1, "archive", err)
}

func commandErr(code int, kind string, err error) error {
	if err == nil {
		err = errors.New(kind)
	}
	message := err.Error()
	return commandError{code: code, name: kind, message: message, err: err}
}

func (r *runtime) contractError(code, message string) error {
	return commandErr(1, code, errors.New(message))
}
