package federation

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
)

func Open(
	ctx context.Context,
	trawlers []OpenTrawler,
	selectedTrawler *trawlkit.RegisteredTrawlerIdentity,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	requestedRecordAnchor *trawlkit.RecordAnchorIdentifier,
) *open.OpenResponse {
	response := &open.OpenResponse{RequestedRecordAnchor: requestedRecordAnchor}
	selectedTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(selectedTrawler)
	trawler, found := findOpenTrawler(trawlers, selectedTrawler)
	if !found {
		response.Failure = &federation.TrawlerOperationFailure{
			FailedTrawler:  selectedTrawler,
			FailureCode:    federation.FailureCode_FAILURE_CODE_NOT_FOUND,
			FailureMessage: fmt.Sprintf("Trawler %q was not found.", selectedTrawlerIdentity),
		}
		response.Outcome = federation.OperationOutcome_OPERATION_OUTCOME_FAILED
		return response
	}
	if trawlkit.LocalTrawlerShortReferenceText(localShortReference) == "" {
		response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "a local short reference is required", federation.FailureCode_FAILURE_CODE_INVALID_INPUT)
	} else if strings.TrimSpace(trawler.SkipReason) != "" {
		response.Failure = &federation.TrawlerOperationFailure{
			FailedTrawler:                trawler.Manifest.GetRegisteredTrawler(),
			RegisteredTrawlerDisplayName: trawlerDisplayName(trawler.Manifest),
			FailureCode:                  federation.FailureCode_FAILURE_CODE_UNAVAILABLE,
			FailureMessage:               trawler.SkipReason,
		}
	} else if trawler.Run == nil {
		response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "callback is nil", federation.FailureCode_FAILURE_CODE_INTERNAL)
	} else if err := ctx.Err(); err != nil {
		response.Failure = FailureForError(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, err)
	} else {
		record, failure := runOpen(ctx, trawler, localShortReference, requestedRecordAnchor)
		switch {
		case failure != nil:
			response.Failure = callbackFailure(ctx, trawler.Manifest, failure)
		case ctx.Err() != nil:
			response.Failure = FailureForError(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, ctx.Err())
		case record == nil:
			response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "trawler returned no record", federation.FailureCode_FAILURE_CODE_INTERNAL)
		case trawlkit.RecordAnchorIdentifierText(requestedRecordAnchor) != "" &&
			openrecord.ValidateRequestedAnchor(record, requestedRecordAnchor) != nil:
			response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "record does not contain the requested anchor", federation.FailureCode_FAILURE_CODE_INTERNAL)
		case trawlkit.RecordAnchorIdentifierText(requestedRecordAnchor) == "" && openrecord.Validate(record) != nil:
			response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "record is invalid", federation.FailureCode_FAILURE_CODE_INTERNAL)
		case trawlkit.RegisteredTrawlerIdentityText(record.GetRecordTrawler()) != registeredTrawlerIdentityText(trawler.Manifest):
			response.Failure = operationFailure(trawler.Manifest, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, "record trawler does not match selected trawler", federation.FailureCode_FAILURE_CODE_INTERNAL)
		default:
			response.Record = record
			response.Outcome = federation.OperationOutcome_OPERATION_OUTCOME_COMPLETE
			return response
		}
	}
	response.Outcome = federation.OperationOutcome_OPERATION_OUTCOME_FAILED
	return response
}

func runOpen(
	ctx context.Context,
	trawler OpenTrawler,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	requestedRecordAnchor *trawlkit.RecordAnchorIdentifier,
) (record *open.OpenRecord, failure *federation.TrawlerOperationFailure) {
	defer func() {
		if recovered := recover(); recovered != nil {
			record = nil
			failure = panicFailure(
				trawler.Manifest,
				federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
				recovered,
			)
		}
	}()
	return trawler.Run(ctx, localShortReference, requestedRecordAnchor)
}

func findOpenTrawler(
	trawlers []OpenTrawler,
	selectedTrawler *trawlkit.RegisteredTrawlerIdentity,
) (OpenTrawler, bool) {
	selectedTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(selectedTrawler)
	for _, trawler := range trawlers {
		if registeredTrawlerIdentityText(trawler.Manifest) == selectedTrawlerIdentity {
			return trawler, true
		}
	}
	return OpenTrawler{}, false
}
