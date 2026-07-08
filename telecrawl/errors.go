package telecrawl

import (
	"errors"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
)

type commandError struct {
	code    int
	name    string
	message string
	remedy  string
	fields  map[string]any
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

func (e commandError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    e.name,
		Message: e.message,
		Remedy:  e.remedy,
		Fields:  e.fields,
	}
}

func usageErr(err error) error {
	return ckoutput.UsageError{Err: err}
}

func archiveErr(err error) error {
	return commandErr(1, "archive", err, "run: trawl telecrawl sync")
}

func commandErr(code int, kind string, err error, remedy string) error {
	return commandErrFields(code, kind, err, remedy, nil)
}

func commandErrFields(code int, kind string, err error, remedy string, fields map[string]any) error {
	if err == nil {
		err = errors.New(kind)
	}
	message := err.Error()
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = cklog.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return commandError{code: code, name: kind, message: message, remedy: remedy, fields: fields, err: wrapped}
}

func (r *runtime) contractError(code, message, remedy string) error {
	return commandErr(1, code, errors.New(message), remedy)
}
