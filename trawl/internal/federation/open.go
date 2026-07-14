package federation

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

func Open(ctx context.Context, sources []OpenSource, selectedSourceID, requestedRef, requestedAnchorID string) *openv1.OpenResponse {
	requestedAnchorID = strings.TrimSpace(requestedAnchorID)
	response := &openv1.OpenResponse{RequestedRef: requestedRef, RequestedAnchorId: requestedAnchorID}
	source, found := findOpenSource(sources, selectedSourceID)
	if !found {
		response.Failure = &federationv1.SourceFailure{SourceId: strings.TrimSpace(selectedSourceID), Code: federationv1.FailureCode_FAILURE_CODE_NOT_FOUND, Message: fmt.Sprintf("Source %q was not found.", strings.TrimSpace(selectedSourceID)), Remedy: "trawl status"}
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
		return response
	}
	trimmedRef := strings.TrimSpace(requestedRef)
	if trimmedRef == "" {
		response.Failure = operationFailure(source.Manifest, "open", "a ref is required", federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT)
	} else if invalidOpenRef(source.Manifest, trimmedRef) {
		response.Failure = operationFailure(source.Manifest, "open", "ref is outside the selected source namespace", federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT)
	} else if strings.TrimSpace(source.SkipReason) != "" {
		response.Failure = &federationv1.SourceFailure{
			SourceId: sourceID(source.Manifest),
			Surface:  sourceSurface(source.Manifest),
			Code:     federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
			Message:  source.SkipReason,
			Remedy:   "trawl doctor " + sourceID(source.Manifest),
		}
	} else if source.Run == nil {
		response.Failure = operationFailure(source.Manifest, "open", "callback is nil", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
	} else if err := ctx.Err(); err != nil {
		response.Failure = FailureForError(source.Manifest, "open", err)
	} else {
		record, failure := runOpen(ctx, source, trimmedRef, requestedAnchorID)
		switch {
		case failure != nil:
			response.Failure = callbackFailure(ctx, source.Manifest, failure)
		case ctx.Err() != nil:
			response.Failure = FailureForError(source.Manifest, "open", ctx.Err())
		case record == nil:
			response.Failure = operationFailure(source.Manifest, "open", "source returned no record", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		case requestedAnchorID != "" && openrecord.ValidateRequestedAnchor(record, requestedAnchorID) != nil:
			response.Failure = operationFailure(source.Manifest, "open", "record does not contain the requested anchor", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		case requestedAnchorID == "" && openrecord.Validate(record) != nil:
			response.Failure = operationFailure(source.Manifest, "open", "record is invalid", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		case record.SourceId != sourceID(source.Manifest):
			response.Failure = operationFailure(source.Manifest, "open", "record source does not match selected source", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
		default:
			response.Record = record
			response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
			return response
		}
	}
	response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	return response
}

func invalidOpenRef(manifest control.Manifest, ref string) bool {
	prefix, path, qualified := strings.Cut(ref, ":")
	if !qualified {
		return false
	}
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(path) == "" {
		return true
	}
	return prefix != sourceID(manifest)
}

func runOpen(ctx context.Context, source OpenSource, ref, anchorID string) (record *openv1.OpenRecord, failure *federationv1.SourceFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			record = nil
			failure = panicFailure(source.Manifest, "open", recovered)
		}
	}()
	return source.Run(ctx, ref, anchorID)
}

func findOpenSource(sources []OpenSource, wanted string) (OpenSource, bool) {
	wanted = strings.TrimSpace(wanted)
	for _, source := range sources {
		if sourceID(source.Manifest) == wanted {
			return source, true
		}
	}
	return OpenSource{}, false
}
