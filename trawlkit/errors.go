package trawlkit

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type exitCoder interface {
	ExitCode() int
}

type usageError struct {
	err error
}

func (e usageError) Error() string {
	if e.err == nil {
		return "usage error"
	}
	return e.err.Error()
}

func (e usageError) Unwrap() error {
	return e.err
}

func (e usageError) ExitCode() int {
	return 2
}

func (e usageError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{Code: "usage", Message: e.Error()}
}

// MissingArchiveError keeps the absent archive path for diagnostics without
// exposing it through any human or federated error surface.
type MissingArchiveError struct {
	path string
}

func NewMissingArchiveError(path string) MissingArchiveError {
	return MissingArchiveError{path: path}
}

func (e MissingArchiveError) Error() string {
	return "The archive is not available."
}

func (e MissingArchiveError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "unavailable",
		Message: e.Error(),
	}
}

func (e MissingArchiveError) InternalErrorLogMessage() string {
	return fmt.Sprintf("archive file does not exist: %q", e.path)
}

func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var coder exitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	if output.IsUsage(err) {
		return 2
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	return 1
}

func TrawlerOperationErrorDescription(err error) output.ErrorDescription {
	if errors.Is(err, ErrUnknownShortRef) {
		return output.ErrorDescription{
			Code:    "not_found",
			Message: "No result has that link.",
		}
	}
	return output.ErrorDescriptionFor(err)
}
