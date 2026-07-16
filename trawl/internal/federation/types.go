package federation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"google.golang.org/protobuf/proto"
)

type StatusSource struct {
	Manifest   control.Manifest
	Run        func(context.Context) (*control.Status, *federationv1.SourceFailure)
	SkipReason string
}

type SearchSource struct {
	Manifest   control.Manifest
	Run        func(context.Context, trawlkit.Query) (trawlkit.SearchResult, *federationv1.SourceFailure)
	SkipReason string
}

type OpenSource struct {
	Manifest   control.Manifest
	Run        func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.SourceFailure)
	SkipReason string
}

func FailureForError(manifest control.Manifest, operation string, err error) *federationv1.SourceFailure {
	if err == nil {
		err = errors.New(strings.TrimSpace(operation) + " failed")
	}
	body := ckoutput.ErrorBody{}
	var provider ckoutput.ErrorBodyProvider
	if errors.As(err, &provider) {
		body = provider.ErrorBody()
	}
	defaultBody := ckoutput.ErrorBodyFor(err)
	if strings.TrimSpace(body.Code) == "" {
		body.Code = defaultBody.Code
	}
	if strings.TrimSpace(body.Message) == "" {
		body.Message = defaultBody.Message
	}
	code := failureCode(body.Code)
	if errors.Is(err, context.Canceled) {
		code = federationv1.FailureCode_FAILURE_CODE_CANCELLED
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
	} else if errors.Is(err, os.ErrPermission) {
		code = federationv1.FailureCode_FAILURE_CODE_PERMISSION
	}
	message := body.Message
	if strings.TrimSpace(message) == "" {
		message = err.Error()
	}
	remedy := body.Remedy
	if strings.TrimSpace(remedy) == "" {
		remedy = "Retry with -v to see the log location."
	}
	return &federationv1.SourceFailure{SourceId: sourceID(manifest), Surface: sourceSurface(manifest), Code: code, Message: message, Remedy: remedy}
}

func failureCode(value string) federationv1.FailureCode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "deadline_exceeded", "timeout":
		return federationv1.FailureCode_FAILURE_CODE_TIMEOUT
	case "permission", "permission_denied":
		return federationv1.FailureCode_FAILURE_CODE_PERMISSION
	case "authentication", "authentication_required":
		return federationv1.FailureCode_FAILURE_CODE_AUTHENTICATION
	case "invalid_ref", "invalid_input", "usage", "ambiguous_short_ref":
		return federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT
	case "not_found", "source_not_found", "unknown_short_ref":
		return federationv1.FailureCode_FAILURE_CODE_NOT_FOUND
	case "unavailable":
		return federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	default:
		return federationv1.FailureCode_FAILURE_CODE_INTERNAL
	}
}

func sourceID(manifest control.Manifest) string {
	return strings.TrimSpace(manifest.ID)
}

func sourceSurface(manifest control.Manifest) string {
	return strings.TrimSpace(manifest.DisplayName)
}

func stampedFailure(manifest control.Manifest, failure *federationv1.SourceFailure) *federationv1.SourceFailure {
	if failure == nil {
		failure = &federationv1.SourceFailure{
			Code:    federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
			Message: "The source did not complete the operation.",
		}
	} else {
		failure = proto.Clone(failure).(*federationv1.SourceFailure)
	}
	failure.SourceId = sourceID(manifest)
	failure.Surface = sourceSurface(manifest)
	return failure
}

func callbackFailure(ctx context.Context, manifest control.Manifest, failure *federationv1.SourceFailure) *federationv1.SourceFailure {
	failure = stampedFailure(manifest, failure)
	switch ctx.Err() {
	case context.Canceled:
		failure.Code = federationv1.FailureCode_FAILURE_CODE_CANCELLED
	case context.DeadlineExceeded:
		failure.Code = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
	}
	return failure
}

func operationFailure(manifest control.Manifest, operation, message string, code federationv1.FailureCode) *federationv1.SourceFailure {
	return &federationv1.SourceFailure{
		SourceId: sourceID(manifest),
		Surface:  sourceSurface(manifest),
		Code:     code,
		Message:  operation + " failed: " + message,
		Remedy:   "Retry with -v to see the log location.",
	}
}

func projectionFailure(manifest control.Manifest, operation string, err error) *federationv1.SourceFailure {
	return operationFailure(manifest, operation, err.Error(), federationv1.FailureCode_FAILURE_CODE_INTERNAL)
}

func panicFailure(manifest control.Manifest, operation string, recovered any) *federationv1.SourceFailure {
	return operationFailure(manifest, operation, fmt.Sprintf("source panicked: %v", recovered), federationv1.FailureCode_FAILURE_CODE_INTERNAL)
}

func skippedSource(manifest control.Manifest, reason string) *federationv1.SkippedSource {
	return &federationv1.SkippedSource{
		SourceId: sourceID(manifest),
		Surface:  sourceSurface(manifest),
		Reason:   strings.TrimSpace(reason),
	}
}

func aggregateOutcome(successes, failures, skips int) federationv1.OperationOutcome {
	if successes > 0 && failures == 0 && skips == 0 {
		return federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	}
	if successes > 0 || failures == 0 && skips > 0 {
		return federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	}
	return federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
}

func cloneSearchHit(hit *federationv1.SearchHit) *federationv1.SearchHit {
	return proto.Clone(hit).(*federationv1.SearchHit)
}
