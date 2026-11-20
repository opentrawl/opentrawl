package mail

import (
	"errors"
	"os"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type mailCommandError struct {
	errorCode    string
	errorMessage string
	cause        error
}

func (e mailCommandError) Error() string {
	return e.errorMessage
}

func (e mailCommandError) Unwrap() error {
	return e.cause
}

func (e mailCommandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{Code: e.errorCode, Message: e.errorMessage}
}

func newMailCommandError(errorCode, errorMessage string, cause error) error {
	if cause == nil {
		cause = errors.New(errorMessage)
	}
	return mailCommandError{errorCode: errorCode, errorMessage: errorMessage, cause: cause}
}

func archiveUnavailableError(cause error) error {
	return newMailCommandError("archive_missing", "archive database is not ready", cause)
}

// appleMailSourceError keeps macOS privacy refusals distinct: without Full
// Disk Access, reading ~/Library/Mail fails with os.ErrPermission.
func appleMailSourceError(cause error) error {
	if errors.Is(cause, os.ErrPermission) {
		return newMailCommandError("permission", "Apple Mail needs Full Disk Access", cause)
	}
	if errors.Is(cause, os.ErrNotExist) {
		return newMailCommandError("unavailable", "Apple Mail has no local store on this Mac", cause)
	}
	return newMailCommandError("source_unreadable", "Apple Mail store could not be read", cause)
}
