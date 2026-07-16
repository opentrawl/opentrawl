package wacrawl

import (
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const fullDiskAccessRemedy = "grant Full Disk Access to your terminal or Trawl in System Settings > Privacy & Security > Full Disk Access"

type commandError struct {
	code    int
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
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e commandError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    e.name,
		Message: e.message,
		Remedy:  e.remedy,
	}
}

func commandErr(code int, name, message, remedy string) error {
	err := errors.New(message)
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = log.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return commandError{code: code, name: name, message: message, remedy: remedy, err: wrapped}
}

func archiveErr(err error) error {
	if err == nil {
		return nil
	}
	return commandErr(1, "archive", err.Error(), "run trawl sync whatsapp")
}

func usageErr(err error) error {
	return output.UsageError{Err: err}
}
