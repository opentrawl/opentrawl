package calcrawl

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

func archiveErr(err error) error {
	return commandErr(1, "archive", err, "run trawl sync calendar")
}

func sourceErr(err error) error {
	return commandErr(1, "source_store", err, fullDiskAccessRemedy)
}

func commandErr(code int, kind string, err error, remedy string) error {
	if err == nil {
		err = errors.New(kind)
	}
	message := err.Error()
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = log.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return commandError{code: code, name: kind, message: message, remedy: remedy, err: wrapped}
}
