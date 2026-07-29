package calendar

import (
	"errors"

	"github.com/opentrawl/opentrawl/trawlkit/output"
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

func (e commandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    e.name,
		Message: e.message,
	}
}

func archiveErr(err error) error {
	return commandErr(1, "archive", err)
}

func sourceErr(err error) error {
	return commandErr(1, "source_store", err)
}

func commandErr(code int, kind string, err error) error {
	if err == nil {
		err = errors.New(kind)
	}
	message := err.Error()
	return commandError{code: code, name: kind, message: message, err: err}
}
