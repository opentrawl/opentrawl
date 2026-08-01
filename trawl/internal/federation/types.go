package federation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/proto"
)

type StatusTrawler struct {
	Manifest   *federation.RegisteredTrawlerManifest
	Run        func(context.Context) (*status.TrawlerStatusResponse, *federation.TrawlerOperationFailure)
	SkipReason string
}

type SearchTrawler struct {
	Manifest   *federation.RegisteredTrawlerManifest
	Run        func(context.Context, trawlkit.Query) (*search.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federation.TrawlerOperationFailure)
	SkipReason string
}

type OpenTrawler struct {
	Manifest *federation.RegisteredTrawlerManifest
	Run      func(
		context.Context,
		*trawlkit.LocalTrawlerShortReference,
		*trawlkit.RecordAnchorIdentifier,
	) (*open.OpenRecord, *federation.TrawlerOperationFailure)
	SkipReason string
}

func FailureForError(
	manifest *federation.RegisteredTrawlerManifest,
	sharedTrawlerOperation federation.SharedTrawlerOperation,
	err error,
) *federation.TrawlerOperationFailure {
	if err == nil {
		if sharedTrawlerOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			err = errors.New("the shared trawler operation is not specified")
		} else {
			err = errors.New("the trawler operation failed")
		}
	}
	description := ckoutput.ErrorDescription{}
	var provider ckoutput.ErrorDescriptionProvider
	if errors.As(err, &provider) {
		description = provider.ErrorDescription()
	}
	defaultDescription := trawlkit.TrawlerOperationErrorDescription(err)
	if strings.TrimSpace(description.Code) == "" {
		description.Code = defaultDescription.Code
	}
	if strings.TrimSpace(description.Message) == "" {
		description.Message = defaultDescription.Message
	}
	code := failureCode(description.Code)
	if errors.Is(err, context.Canceled) {
		code = federation.FailureCode_FAILURE_CODE_CANCELLED
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = federation.FailureCode_FAILURE_CODE_TIMEOUT
	} else if errors.Is(err, os.ErrPermission) {
		code = federation.FailureCode_FAILURE_CODE_PERMISSION
	}
	message := description.Message
	if strings.TrimSpace(message) == "" {
		message = err.Error()
	}
	return &federation.TrawlerOperationFailure{
		FailedTrawler:                manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		FailureCode:                  code,
		FailureMessage:               message,
	}
}

func failureCode(value string) federation.FailureCode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "deadline_exceeded", "timeout":
		return federation.FailureCode_FAILURE_CODE_TIMEOUT
	case "permission", "permission_denied":
		return federation.FailureCode_FAILURE_CODE_PERMISSION
	case "authentication", "authentication_required":
		return federation.FailureCode_FAILURE_CODE_AUTHENTICATION
	case "invalid_ref", "invalid_input", "usage", "ambiguous_short_ref":
		return federation.FailureCode_FAILURE_CODE_INVALID_INPUT
	case "not_found", "source_not_found", "unknown_short_ref":
		return federation.FailureCode_FAILURE_CODE_NOT_FOUND
	case "unavailable", "archive", "archive_missing", "archive_unreadable":
		return federation.FailureCode_FAILURE_CODE_UNAVAILABLE
	default:
		return federation.FailureCode_FAILURE_CODE_INTERNAL
	}
}

func registeredTrawlerIdentityText(manifest *federation.RegisteredTrawlerManifest) string {
	return trawlkit.RegisteredTrawlerIdentityText(manifest.GetRegisteredTrawler())
}

func trawlerDisplayName(manifest *federation.RegisteredTrawlerManifest) string {
	for _, candidate := range []string{
		manifest.GetRegisteredTrawlerDisplayName(),
		manifest.GetRegisteredTrawlerCommandName(),
		registeredTrawlerIdentityText(manifest),
	} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

func stampedFailure(manifest *federation.RegisteredTrawlerManifest, failure *federation.TrawlerOperationFailure) *federation.TrawlerOperationFailure {
	if failure == nil {
		failure = &federation.TrawlerOperationFailure{
			FailureCode:    federation.FailureCode_FAILURE_CODE_UNAVAILABLE,
			FailureMessage: "The trawler did not complete the operation.",
		}
	} else {
		failure = proto.Clone(failure).(*federation.TrawlerOperationFailure)
	}
	failure.FailedTrawler = manifest.GetRegisteredTrawler()
	failure.RegisteredTrawlerDisplayName = trawlerDisplayName(manifest)
	return failure
}

func callbackFailure(ctx context.Context, manifest *federation.RegisteredTrawlerManifest, failure *federation.TrawlerOperationFailure) *federation.TrawlerOperationFailure {
	failure = stampedFailure(manifest, failure)
	switch ctx.Err() {
	case context.Canceled:
		failure.FailureCode = federation.FailureCode_FAILURE_CODE_CANCELLED
	case context.DeadlineExceeded:
		failure.FailureCode = federation.FailureCode_FAILURE_CODE_TIMEOUT
	}
	return failure
}

func operationFailure(
	manifest *federation.RegisteredTrawlerManifest,
	sharedTrawlerOperation federation.SharedTrawlerOperation,
	message string,
	code federation.FailureCode,
) *federation.TrawlerOperationFailure {
	if sharedTrawlerOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		message = "The shared trawler operation is not specified."
	}
	return &federation.TrawlerOperationFailure{
		FailedTrawler:                manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		FailureCode:                  code,
		FailureMessage:               message,
	}
}

func projectionFailure(
	manifest *federation.RegisteredTrawlerManifest,
	sharedTrawlerOperation federation.SharedTrawlerOperation,
	err error,
) *federation.TrawlerOperationFailure {
	return operationFailure(manifest, sharedTrawlerOperation, err.Error(), federation.FailureCode_FAILURE_CODE_INTERNAL)
}

func panicFailure(
	manifest *federation.RegisteredTrawlerManifest,
	sharedTrawlerOperation federation.SharedTrawlerOperation,
	recovered any,
) *federation.TrawlerOperationFailure {
	return operationFailure(
		manifest,
		sharedTrawlerOperation,
		fmt.Sprintf("trawler panicked: %v", recovered),
		federation.FailureCode_FAILURE_CODE_INTERNAL,
	)
}

func skippedTrawler(manifest *federation.RegisteredTrawlerManifest, reason string) *federation.TrawlerSkippedFromOperation {
	return &federation.TrawlerSkippedFromOperation{
		SkippedTrawler:               manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		SkipReason:                   strings.TrimSpace(reason),
	}
}

func aggregateOutcome(successes, failures, skips int) federation.OperationOutcome {
	if successes > 0 && failures == 0 && skips == 0 {
		return federation.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	}
	if successes > 0 || failures == 0 && skips > 0 {
		return federation.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	}
	return federation.OperationOutcome_OPERATION_OUTCOME_FAILED
}
