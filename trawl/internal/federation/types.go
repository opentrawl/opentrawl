package federation

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
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
		Remedy:   "trawl doctor " + sourceID(manifest),
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
