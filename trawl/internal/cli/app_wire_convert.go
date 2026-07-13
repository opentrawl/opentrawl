package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

const appStatusTimeout = 2 * time.Second

func (r *Runtime) appStatusResponse(ctx context.Context, sources []Source) *federationv1.StatusResponse {
	ctx = trawlkit.WithInternalAppRequest(ctx)
	ctx, cancel := context.WithTimeout(ctx, appStatusTimeout)
	defer cancel()
	return federation.Status(ctx, r.federationStatusSources(sources))
}

func (r *Runtime) appSearchResponse(ctx context.Context, sources []Source, query string) *federationv1.SearchResponse {
	return federation.Search(
		ctx,
		r.federationSearchSources(sources),
		trawlkit.Query{Text: query, Limit: appSearchLimit, BoundedTotals: true},
		federationv1.SearchOrder_SEARCH_ORDER_RECENCY,
		uint32(appSearchLimit),
	)
}

func (r *Runtime) appOpenResponse(ctx context.Context, sourceID, ref string) *openv1.OpenResponse {
	return federation.Open(ctx, r.federationOpenSources(discoverCrawlers(ctx)), sourceID, ref)
}

func appSyncResponse(sources []Source, results []SyncResult) *appv1.SyncResponse {
	response := &appv1.SyncResponse{}
	complete, partial := 0, 0
	for index, result := range results {
		source := sources[index]
		item := &appv1.SyncSourceResult{AppId: source.ID, Surface: sourceHumanName(source), Outcome: appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE}
		switch {
		case syncResultFailed(result):
			item.Outcome, item.Failure = appv1.OperationOutcome_OPERATION_OUTCOME_FAILED, appSyncFailure(source, result)
			response.Failures = append(response.Failures, item.Failure)
		case strings.EqualFold(result.State, "partial"):
			item.Outcome, item.Failure = appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL, appSyncFailure(source, result)
			response.Failures = append(response.Failures, item.Failure)
			partial++
		default: complete++
		}
		response.Sources = append(response.Sources, item)
	}
	if partial > 0 || complete > 0 && len(response.Failures) > partial { response.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	} else if len(response.Failures) > 0 { response.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	} else { response.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE }
	return response
}

func appSyncFailure(source Source, result SyncResult) *appv1.SourceFailure {
	code := appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	if result.Error != nil { code = appSyncFailureCode(result.Error.Code) }
	remedy := fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source))
	if result.Error != nil && result.Error.Remedy != "" { remedy = result.Error.Remedy }
	return &appv1.SourceFailure{AppId: source.ID, Surface: sourceHumanName(source), Code: code, Message: firstNonEmpty(result.Message, "The crawler did not complete sync."), Remedy: remedy}
}

func appSyncFailureCode(code string) appv1.FailureCode {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "timeout", "deadline_exceeded": return appv1.FailureCode_FAILURE_CODE_TIMEOUT
	case "permission_denied", "permission": return appv1.FailureCode_FAILURE_CODE_PERMISSION
	case "authentication_required", "authentication": return appv1.FailureCode_FAILURE_CODE_AUTHENTICATION
	case "invalid_ref", "invalid_input": return appv1.FailureCode_FAILURE_CODE_INVALID_INPUT
	case "not_found", "source_not_found", "unknown_short_ref": return appv1.FailureCode_FAILURE_CODE_NOT_FOUND
	case "internal", "command_failed", "sync_failed": return appv1.FailureCode_FAILURE_CODE_INTERNAL
	default: return appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	}
}
