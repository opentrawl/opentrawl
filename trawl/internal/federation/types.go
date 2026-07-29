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

type StatusSource struct {
	Manifest   *federationv1.RegisteredTrawlerManifest
	Run        func(context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

type SearchSource struct {
	Manifest   *federationv1.RegisteredTrawlerManifest
	Run        func(context.Context, trawlkit.Query) (*searchv1.TrawlerSearchResponse, map[string]string, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

type OpenSource struct {
	Manifest   *federationv1.RegisteredTrawlerManifest
	Run        func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure)
	SkipReason string
}

func FailureForError(manifest *federationv1.RegisteredTrawlerManifest, operation string, err error) *federationv1.TrawlerOperationFailure {
	if err == nil {
		err = errors.New(strings.TrimSpace(operation) + " failed")
	}
	description := ckoutput.ErrorDescription{}
	var provider ckoutput.ErrorDescriptionProvider
	if errors.As(err, &provider) {
		description = provider.ErrorDescription()
	}
	defaultDescription := ckoutput.ErrorDescriptionFor(err)
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
		RegisteredTrawlerManifestIdentity: sourceID(manifest),
		RegisteredTrawlerDisplayName:      sourceSurface(manifest),
		FailureCode:                       code,
		FailureMessage:                    message,
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

func sourceID(manifest *federationv1.RegisteredTrawlerManifest) string {
	return strings.TrimSpace(manifest.GetRegisteredTrawlerManifestIdentity())
}

func sourceSurface(manifest *federationv1.RegisteredTrawlerManifest) string {
	for _, candidate := range []string{
		manifest.GetRegisteredTrawlerDisplayName(),
		manifest.GetRegisteredTrawlerCommandName(),
		manifest.GetRegisteredTrawlerManifestIdentity(),
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
	failure.RegisteredTrawlerManifestIdentity = sourceID(manifest)
	failure.RegisteredTrawlerDisplayName = sourceSurface(manifest)
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

func operationFailure(manifest *federationv1.RegisteredTrawlerManifest, operation, message string, code federationv1.FailureCode) *federationv1.TrawlerOperationFailure {
	return &federationv1.TrawlerOperationFailure{
		RegisteredTrawlerManifestIdentity: sourceID(manifest),
		RegisteredTrawlerDisplayName:      sourceSurface(manifest),
		FailureCode:                       code,
		FailureMessage:                    operation + " failed: " + message,
	}
}

func projectionFailure(manifest *federationv1.RegisteredTrawlerManifest, operation string, err error) *federationv1.TrawlerOperationFailure {
	return operationFailure(manifest, operation, err.Error(), federationv1.FailureCode_FAILURE_CODE_INTERNAL)
}

func panicFailure(manifest *federationv1.RegisteredTrawlerManifest, operation string, recovered any) *federationv1.TrawlerOperationFailure {
	return operationFailure(manifest, operation, fmt.Sprintf("source panicked: %v", recovered), federationv1.FailureCode_FAILURE_CODE_INTERNAL)
}

func skippedSource(manifest *federationv1.RegisteredTrawlerManifest, reason string) *federationv1.TrawlerSkippedFromOperation {
	return &federationv1.TrawlerSkippedFromOperation{
		RegisteredTrawlerManifestIdentity: sourceID(manifest),
		RegisteredTrawlerDisplayName:      sourceSurface(manifest),
		SkipReason:                        strings.TrimSpace(reason),
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
