package whatsapp

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

func commandErr(code int, name, message string) error {
	err := errors.New(message)
	return commandError{code: code, name: name, message: message, err: err}
}

func archiveErr(err error) error {
	if err == nil {
		return nil
	}
	return commandErr(1, "archive", err.Error())
}

func usageErr(err error) error {
	return output.UsageError{Err: err}
}
