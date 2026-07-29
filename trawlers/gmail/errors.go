package gmail

import (
	"errors"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type commandError struct {
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
	return 1
}

func (e commandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    e.name,
		Message: e.message,
	}
}

func commandErr(kind, message string, err error) error {
	if err == nil {
		err = errors.New(message)
	}
	return commandError{name: kind, message: message, err: err}
}

func archiveErr(err error) error {
	return commandErr("archive_missing", "archive database is not ready", err)
}
