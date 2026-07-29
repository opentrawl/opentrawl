package output

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

func StandardWriters() (stdout io.Writer, stderr io.Writer) {
	return os.Stdout, os.Stderr
}

type UsageError struct {
	Err error
}

func (e UsageError) Error() string {
	if e.Err == nil {
		return "usage error"
	}
	return e.Err.Error()
}

func (e UsageError) Unwrap() error {
	return e.Err
}

type ErrorDescription struct {
	Code     string
	Message  string
	LockPath string
}

type ErrorDescriptionProvider interface {
	ErrorDescription() ErrorDescription
}

func ErrorDescriptionFor(err error) ErrorDescription {
	description := ErrorDescription{
		Code:    "command_failed",
		Message: "command failed",
	}
	if err == nil {
		return description
	}
	var descriptionProvider ErrorDescriptionProvider
	if errors.As(err, &descriptionProvider) {
		description = descriptionProvider.ErrorDescription()
	} else {
		description.Message = err.Error()
		if IsUsage(err) {
			description.Code = "usage"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			description.Code = "deadline_exceeded"
			description.Message = "command timed out"
		}
	}
	description.Code = firstNonEmpty(description.Code, "command_failed")
	description.Message = firstNonEmpty(description.Message, err.Error(), "command failed")
	return description
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func IsUsage(err error) bool {
	var usage UsageError
	return errors.As(err, &usage)
}
