package federation

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

func Open(
	ctx context.Context,
	sources []OpenSource,
	selectedRegisteredTrawlerManifestIdentity string,
	providerLocalRecordIdentifierAcceptedByRegisteredTrawler string,
	requestedAnchorID string,
) *openv1.OpenResponse {
	requestedAnchorID = strings.TrimSpace(requestedAnchorID)
	response := &openv1.OpenResponse{RequestedRecordAnchorIdentifier: requestedAnchorID}
	source, found := findOpenSource(sources, selectedRegisteredTrawlerManifestIdentity)
	if !found {
		response.Failure = &federationv1.TrawlerOperationFailure{
			RegisteredTrawlerManifestIdentity: strings.TrimSpace(selectedRegisteredTrawlerManifestIdentity),
			FailureCode:                       federationv1.FailureCode_FAILURE_CODE_NOT_FOUND,
			FailureMessage:                    fmt.Sprintf("Trawler %q was not found.", strings.TrimSpace(selectedRegisteredTrawlerManifestIdentity)),
		}
		response.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
		return response
	}
	providerLocalRecordIdentifierAcceptedByRegisteredTrawler = strings.TrimSpace(providerLocalRecordIdentifierAcceptedByRegisteredTrawler)
	if providerLocalRecordIdentifierAcceptedByRegisteredTrawler == "" {
		response.Failure = operationFailure(source.Manifest, "open", "a local record identifier is required", federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT)
	} else if providerLocalRecordIdentifierIsOutsideSelectedTrawlerNamespace(source.Manifest, providerLocalRecordIdentifierAcceptedByRegisteredTrawler) {
		response.Failure = operationFailure(source.Manifest, "open", "local record identifier is outside the selected trawler namespace", federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT)
	} else if strings.TrimSpace(source.SkipReason) != "" {
		response.Failure = &federationv1.TrawlerOperationFailure{
			RegisteredTrawlerManifestIdentity: sourceID(source.Manifest),
			RegisteredTrawlerDisplayName:      sourceSurface(source.Manifest),
			FailureCode:                       federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
			FailureMessage:                    source.SkipReason,
		}
	} else if source.Run == nil {
		response.Failure = operationFailure(source.Manifest, "open", "callback is nil", federationv1.FailureCode_FAILURE_CODE_INTERNAL)
	} else if err := ctx.Err(); err != nil {
		response.Failure = FailureForError(source.Manifest, "open", err)
	} else {
		record, failure := runOpen(ctx, source, providerLocalRecordIdentifierAcceptedByRegisteredTrawler, requestedAnchorID)
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
		case record.GetRegisteredTrawlerManifestIdentity() != sourceID(source.Manifest):
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

func providerLocalRecordIdentifierIsOutsideSelectedTrawlerNamespace(
	manifest *federationv1.RegisteredTrawlerManifest,
	providerLocalRecordIdentifier string,
) bool {
	prefix, path, qualified := strings.Cut(providerLocalRecordIdentifier, ":")
	if !qualified {
		return false
	}
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(path) == "" {
		return true
	}
	return prefix != sourceID(manifest)
}

func runOpen(
	ctx context.Context,
	source OpenSource,
	providerLocalRecordIdentifierAcceptedByRegisteredTrawler string,
	requestedAnchorID string,
) (record *openv1.OpenRecord, failure *federationv1.TrawlerOperationFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			record = nil
			failure = panicFailure(source.Manifest, "open", recovered)
		}
	}()
	return source.Run(ctx, providerLocalRecordIdentifierAcceptedByRegisteredTrawler, requestedAnchorID)
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
