package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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

func (e usageError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{Code: "usage", Message: e.Error(), Remedy: "run help for the command"}
}

type partialError struct {
	err error
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
	return "This source is not ready yet."
}

func (e MissingArchiveError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "unavailable",
		Message: e.Error(),
	}
}

func (e partialError) Error() string {
	if e.err == nil {
		return "partial failure"
	}
	return e.err.Error()
}

func (e partialError) Unwrap() error {
	return e.err
}

func (e partialError) ExitCode() int {
	return 3
}

type whoAmbiguityError struct {
	message    string
	query      string
	who        string
	candidates []whomatch.Candidate
	code       int
}

func (e whoAmbiguityError) Error() string {
	if strings.TrimSpace(e.message) != "" {
		return strings.TrimSpace(e.message)
	}
	if e.code == 5 {
		return "--who did not match a person"
	}
	return "--who matched more than one person"
}

func (e whoAmbiguityError) ExitCode() int {
	if e.code == 5 {
		return 5
	}
	return 4
}

func (e whoAmbiguityError) ErrorBody() output.ErrorBody {
	if e.code == 5 {
		fields := map[string]any{"hint": "Search without --who to check whether matching items exist."}
		if len(e.candidates) > 0 {
			fields["did_you_mean"] = whoCandidateOutputs(e.candidates)
		}
		return output.ErrorBody{
			Code:    "unknown_who",
			Message: e.Error(),
			Remedy:  "Run who <name>, or search without --who to check whether matching items exist.",
			Fields:  fields,
		}
	}
	return output.ErrorBody{
		Code:    "ambiguous_who",
		Message: e.Error(),
		Remedy:  "Retry with one identifier from candidates.",
		Fields:  map[string]any{"candidates": whoCandidateOutputs(e.candidates)},
	}
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

func errorBodyFor(err error) output.ErrorBody {
	return output.ErrorBodyFor(err)
}

func renderError(w writer, format output.Format, err error) {
	if err == nil {
		return
	}
	if format == output.JSON {
		_ = output.WriteError(w, errorBodyFor(err))
		return
	}
	body := errorBodyFor(err)
	if writeWhoResolutionErrorText(w, err, body) {
		return
	}
	_, _ = fmt.Fprintf(w, "Error: %s\n", body.Message)
	if strings.TrimSpace(body.Remedy) != "" {
		_, _ = fmt.Fprintf(w, "\n%s\n", body.Remedy)
	}
}

type writer interface {
	Write([]byte) (int, error)
}
