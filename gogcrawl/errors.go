package gogcrawl

import (
	"errors"
	"strings"

	"github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/output"
)

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

func commandErr(kind, message, remedy string, err error) error {
	return commandErrCode(1, kind, message, remedy, err)
}

func commandErrCode(code int, kind, message, remedy string, err error) error {
	if err == nil {
		err = errors.New(message)
	}
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = log.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return commandError{code: code, name: kind, message: message, remedy: remedy, err: wrapped}
}

func archiveErr(err error) error {
	return commandErr("archive_missing", "archive database is not ready", "run gogcrawl sync", err)
}
