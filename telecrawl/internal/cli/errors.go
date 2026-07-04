package cli

import (
	"errors"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
)

func usageErr(err error) error {
	return &cliError{code: 2, err: err, event: "usage_error"}
}

func (r *runtime) contractError(code, message, remedy string) error {
	body := contractErrorBody{Code: code, Message: message, Remedy: remedy}
	err := newRemediedError(message, remedy)
	if r.json {
		if printErr := r.print(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: 1, err: err, quiet: true, event: code}
	}
	return &cliError{code: 1, err: err, event: code}
}

func (r *runtime) contractBodyError(code int, body contractErrorBody, human string) error {
	if r.json {
		if printErr := r.print(errorEnvelope{Error: body}); printErr != nil {
			return printErr
		}
		return &cliError{code: code, err: errors.New(body.Message), quiet: true, event: body.Code}
	}
	return &cliError{code: code, err: errors.New(human), event: body.Code}
}

type remediedError struct {
	message string
	remedy  string
}

func newRemediedError(message, remedy string) error {
	return remediedError{message: strings.TrimSpace(message), remedy: strings.TrimSpace(remedy)}
}

func (e remediedError) Error() string {
	switch {
	case e.message != "" && e.remedy != "":
		return e.message + ". " + e.remedy
	case e.message != "":
		return e.message
	default:
		return e.remedy
	}
}

func (e remediedError) Unwrap() error {
	return cklog.WorldMustChange{Err: errors.New(e.message), Message: e.message, Remedy: e.remedy}
}

func isUsageError(err error) bool {
	var cli *cliError
	return errors.As(err, &cli) && cli.event == "usage_error"
}
