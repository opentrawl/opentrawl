package federation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"google.golang.org/protobuf/proto"
)

type StatusTrawler struct {
	Manifest   *federationv1.RegisteredTrawlerManifest
	Run        func(context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

type SearchTrawler struct {
	Manifest   *federationv1.RegisteredTrawlerManifest
	Run        func(context.Context, trawlkit.Query) (*searchv1.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

type OpenTrawler struct {
	Manifest *federationv1.RegisteredTrawlerManifest
	Run      func(
		context.Context,
		*trawlkit.LocalTrawlerShortReference,
		*trawlkit.RecordAnchorIdentifier,
	) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

func FailureForError(
	manifest *federationv1.RegisteredTrawlerManifest,
	sharedTrawlerOperation federationv1.SharedTrawlerOperation,
	err error,
) *federationv1.TrawlerOperationFailure {
	if err == nil {
		if sharedTrawlerOperation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
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
		code = federationv1.FailureCode_FAILURE_CODE_CANCELLED
	} else if errors.Is(err, context.DeadlineExceeded) {
		code = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
	} else if errors.Is(err, os.ErrPermission) {
		code = federationv1.FailureCode_FAILURE_CODE_PERMISSION
	}
	message := description.Message
	if strings.TrimSpace(message) == "" {
		message = err.Error()
	}
	return &federationv1.TrawlerOperationFailure{
		FailedTrawler:                manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		FailureCode:                  code,
		FailureMessage:               message,
	}
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
	case "unavailable", "archive", "archive_unreadable":
		return federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	default:
		return federationv1.FailureCode_FAILURE_CODE_INTERNAL
	}
}

func registeredTrawlerIdentityText(manifest *federationv1.RegisteredTrawlerManifest) string {
	return trawlkit.RegisteredTrawlerIdentityText(manifest.GetRegisteredTrawler())
}

func trawlerDisplayName(manifest *federationv1.RegisteredTrawlerManifest) string {
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

func stampedFailure(manifest *federationv1.RegisteredTrawlerManifest, failure *federationv1.TrawlerOperationFailure) *federationv1.TrawlerOperationFailure {
	if failure == nil {
		failure = &federationv1.TrawlerOperationFailure{
			FailureCode:    federationv1.FailureCode_FAILURE_CODE_UNAVAILABLE,
			FailureMessage: "The trawler did not complete the operation.",
		}
	} else {
		failure = proto.Clone(failure).(*federationv1.TrawlerOperationFailure)
	}
	failure.FailedTrawler = manifest.GetRegisteredTrawler()
	failure.RegisteredTrawlerDisplayName = trawlerDisplayName(manifest)
	return failure
}

func callbackFailure(ctx context.Context, manifest *federationv1.RegisteredTrawlerManifest, failure *federationv1.TrawlerOperationFailure) *federationv1.TrawlerOperationFailure {
	failure = stampedFailure(manifest, failure)
	switch ctx.Err() {
	case context.Canceled:
		failure.FailureCode = federationv1.FailureCode_FAILURE_CODE_CANCELLED
	case context.DeadlineExceeded:
		failure.FailureCode = federationv1.FailureCode_FAILURE_CODE_TIMEOUT
	}
	return failure
}

func operationFailure(
	manifest *federationv1.RegisteredTrawlerManifest,
	sharedTrawlerOperation federationv1.SharedTrawlerOperation,
	message string,
	code federationv1.FailureCode,
) *federationv1.TrawlerOperationFailure {
	if sharedTrawlerOperation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		message = "The shared trawler operation is not specified."
	}
	return &federationv1.TrawlerOperationFailure{
		FailedTrawler:                manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		FailureCode:                  code,
		FailureMessage:               message,
	}
}

func projectionFailure(
	manifest *federationv1.RegisteredTrawlerManifest,
	sharedTrawlerOperation federationv1.SharedTrawlerOperation,
	err error,
) *federationv1.TrawlerOperationFailure {
	return operationFailure(manifest, sharedTrawlerOperation, err.Error(), federationv1.FailureCode_FAILURE_CODE_INTERNAL)
}

func panicFailure(
	manifest *federationv1.RegisteredTrawlerManifest,
	sharedTrawlerOperation federationv1.SharedTrawlerOperation,
	recovered any,
) *federationv1.TrawlerOperationFailure {
	return operationFailure(
		manifest,
		sharedTrawlerOperation,
		fmt.Sprintf("trawler panicked: %v", recovered),
		federationv1.FailureCode_FAILURE_CODE_INTERNAL,
	)
}

func skippedTrawler(manifest *federationv1.RegisteredTrawlerManifest, reason string) *federationv1.TrawlerSkippedFromOperation {
	return &federationv1.TrawlerSkippedFromOperation{
		SkippedTrawler:               manifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(manifest),
		SkipReason:                   strings.TrimSpace(reason),
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
