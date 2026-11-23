package beeper

import (
	"errors"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type beeperCommandError struct {
	code    int
	name    string
	message string
	err     error
}

func (e beeperCommandError) Error() string {
	return e.message
}

func (e beeperCommandError) Unwrap() error {
	return e.err
}

func (e beeperCommandError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e beeperCommandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    e.name,
		Message: e.message,
	}
}

func commandFailure(code int, name string, err error) error {
	if err == nil {
		err = errors.New(name)
	}
	return beeperCommandError{code: code, name: name, message: err.Error(), err: err}
}

func archiveFailure(err error) error {
	if err == nil {
		return nil
	}
	return commandFailure(1, "archive", err)
}

func usageFailure(err error) error {
	return output.UsageError{Err: err}
}
