package gogcrawl

import (
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type commandError struct {
	name    string
	message string
	remedy  string
	err     error
}

func (e commandError) Error() string {
	if strings.TrimSpace(e.remedy) == "" {
		return e.message
	}
	return e.message + ". " + e.remedy
}

func (e commandError) Unwrap() error {
	return e.err
}

func (e commandError) ExitCode() int {
	return 1
}

func (e commandError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    e.name,
		Message: e.message,
		Remedy:  e.remedy,
	}
}

func commandErr(kind, message, remedy string, err error) error {
	if err == nil {
		err = errors.New(message)
	}
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = log.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return commandError{name: kind, message: message, remedy: remedy, err: wrapped}
}

func archiveErr(err error) error {
	return commandErr("archive_missing", "archive database is not ready", "run trawl sync gmail", err)
}
