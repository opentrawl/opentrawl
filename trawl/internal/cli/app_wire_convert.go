package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func appStatusMessage(source Source, status StatusEnvelope, now time.Time) *appv1.SourceStatus {
	counts := make([]*appv1.Count, 0, len(status.Counts))
	for _, count := range status.Counts {
		counts = append(counts, &appv1.Count{Id: count.ID, Display: formatCount(count)})
	}
	return &appv1.SourceStatus{
		AppId: status.AppID, Surface: status.Surface, State: status.State,
		Summary: status.Summary, Counts: counts,
		LastSyncedDisplay: freshnessText(status, now), ArchiveBytes: appArchiveBytes(source),
	}
}

func appArchiveBytes(source Source) int64 {
	paths, err := resolveSourcePaths(source)
	if err != nil {
		return 0
	}
	info, err := os.Stat(paths.paths.Archive)
	if err != nil {
		return 0
	}
	return info.Size()
}

func appSearchMessage(row SearchRow) *appv1.SearchHit {
	return &appv1.SearchHit{
		OpenRef: row.Ref, AppId: row.Source, Title: appSearchTitle(row),
		Snippet: row.Snippet, WhenDisplay: appSearchDate(row),
	}
}

func appSearchTitle(row SearchRow) string {
	if title := render.HumanIdentity(normalizeSelf(row.Where)); title != "" {
		return title
	}
	return render.HumanIdentity(normalizeSelf(row.Who))
}

func appSearchDate(row SearchRow) string {
	if !row.timeOK {
		return ""
	}
	if row.AllDay {
		return row.parsedTime.Format("2006-01-02")
	}
	return render.ShortLocalTime(row.parsedTime)
}

func appStatusResponse(results []appStatusResult, now time.Time) *appv1.StatusResponse {
	response := &appv1.StatusResponse{}
	for _, result := range results {
		if result.Err != nil || statusFailed(result.Status) {
			response.Failures = append(response.Failures, appStatusFailure(result.Source, result.Err))
			continue
		}
		response.Sources = append(response.Sources, appStatusMessage(result.Source, result.Status, now))
	}
	response.Outcome = appOutcome(len(response.Sources), len(response.Failures))
	return response
}

func appSearchResponse(results []searchSourceResult, merged mergedSearchResult) *appv1.SearchResponse {
	response := &appv1.SearchResponse{
		ResultLimit: appSearchLimit,
		Truncated:   merged.Truncated,
	}
	for _, row := range merged.Rows {
		response.Hits = append(response.Hits, appSearchMessage(row))
	}
	for _, result := range results {
		if result.Err != nil {
			response.Failures = append(response.Failures, appSourceFailure(result.Source, "search", result.Err))
		}
	}
	response.Outcome = appOutcome(appSearchSuccesses(results), len(response.Failures))
	return response
}

func appSearchSuccesses(results []searchSourceResult) int {
	successes := 0
	for _, result := range results {
		if result.Err == nil && !result.Skipped {
			successes++
		}
	}
	return successes
}

func appInvalidSourceSearchResponse(sourceID string) *appv1.SearchResponse {
	return &appv1.SearchResponse{
		Outcome:     appv1.OperationOutcome_OPERATION_OUTCOME_FAILED,
		ResultLimit: appSearchLimit,
		Failures: []*appv1.SourceFailure{{
			AppId:   sourceID,
			Code:    appv1.FailureCode_FAILURE_CODE_NOT_FOUND,
			Message: fmt.Sprintf("Source %q was not found.", sourceID),
			Remedy:  "run trawl status",
		}},
	}
}

func appSyncResponse(sources []Source, results []SyncResult) *appv1.SyncResponse {
	response := &appv1.SyncResponse{}
	complete := 0
	partial := 0
	for index, result := range results {
		source := sources[index]
		sourceResult := &appv1.SyncSourceResult{
			AppId:   source.ID,
			Surface: sourceHumanName(source),
			Outcome: appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE,
		}
		switch {
		case syncResultFailed(result):
			failure := appSyncFailure(source, result)
			sourceResult.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_FAILED
			sourceResult.Failure = failure
			response.Failures = append(response.Failures, failure)
		case strings.EqualFold(result.State, "partial"):
			failure := appSyncFailure(source, result)
			sourceResult.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
			sourceResult.Failure = failure
			response.Failures = append(response.Failures, failure)
			partial++
		default:
			complete++
		}
		response.Sources = append(response.Sources, sourceResult)
	}
	response.Outcome = appSyncOutcome(complete, partial, len(response.Failures)-partial)
	return response
}

func appOpenResponse(source Source, ref string, output []byte, err error) *appv1.OpenResponse {
	response := &appv1.OpenResponse{AppId: source.ID, OpenRef: ref}
	if err == nil {
		response.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
		response.Output = output
		return response
	}
	response.Outcome = appv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	response.Failure = appSourceFailure(source, "open", err)
	return response
}

func appUnknownOpenResponse(sourceID, ref string) *appv1.OpenResponse {
	return &appv1.OpenResponse{
		Outcome: appv1.OperationOutcome_OPERATION_OUTCOME_FAILED,
		AppId:   sourceID,
		OpenRef: ref,
		Failure: &appv1.SourceFailure{
			AppId:   sourceID,
			Code:    appv1.FailureCode_FAILURE_CODE_NOT_FOUND,
			Message: fmt.Sprintf("Source %q was not found.", sourceID),
			Remedy:  "run trawl status",
		},
	}
}

func appInvalidOpenResponse(ref string) *appv1.OpenResponse {
	return &appv1.OpenResponse{
		Outcome: appv1.OperationOutcome_OPERATION_OUTCOME_FAILED,
		OpenRef: ref,
		Failure: &appv1.SourceFailure{
			Code:    appv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
			Message: "Ref is missing a source or path.",
			Remedy:  "refs look like <source>:<path>, for example imessage:msg/8842",
		},
	}
}

func appStatusFailure(source Source, err error) *appv1.SourceFailure {
	return &appv1.SourceFailure{
		AppId:   source.ID,
		Surface: sourceHumanName(source),
		Code:    appFailureCode(err),
		Message: "The crawler did not report its status.",
		Remedy:  fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source)),
	}
}

func appSyncFailure(source Source, result SyncResult) *appv1.SourceFailure {
	code := appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	if result.Error != nil {
		code = appSyncFailureCode(result.Error.Code)
	}
	return &appv1.SourceFailure{
		AppId:   source.ID,
		Surface: sourceHumanName(source),
		Code:    code,
		Message: firstNonEmpty(result.Message, "The crawler did not complete sync."),
		Remedy:  firstNonEmpty(syncFailureRemedy(result), fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source))),
	}
}

func appSyncFailureCode(code string) appv1.FailureCode {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "timeout", "deadline_exceeded":
		return appv1.FailureCode_FAILURE_CODE_TIMEOUT
	case "permission_denied", "permission":
		return appv1.FailureCode_FAILURE_CODE_PERMISSION
	case "authentication_required", "authentication":
		return appv1.FailureCode_FAILURE_CODE_AUTHENTICATION
	case "invalid_ref", "invalid_input":
		return appv1.FailureCode_FAILURE_CODE_INVALID_INPUT
	case "not_found", "source_not_found", "unknown_short_ref":
		return appv1.FailureCode_FAILURE_CODE_NOT_FOUND
	case "internal", "command_failed", "sync_failed":
		return appv1.FailureCode_FAILURE_CODE_INTERNAL
	default:
		return appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	}
}

func syncFailureRemedy(result SyncResult) string {
	if result.Error == nil {
		return ""
	}
	return result.Error.Remedy
}

func appSourceFailure(source Source, verb string, err error) *appv1.SourceFailure {
	return &appv1.SourceFailure{
		AppId:   source.ID,
		Surface: sourceHumanName(source),
		Code:    appFailureCode(err),
		Message: fmt.Sprintf("%s %s failed.", sourceHumanName(source), verb),
		Remedy:  fmt.Sprintf("run trawl doctor %s", sourceCommandToken(source)),
	}
}

func appFailureCode(err error) appv1.FailureCode {
	if err == nil {
		return appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	}
	if isTimeoutError(err) {
		return appv1.FailureCode_FAILURE_CODE_TIMEOUT
	}
	code := strings.ToLower(strings.TrimSpace(sourceErrorBody(err).Code))
	switch code {
	case "deadline_exceeded", "timeout":
		return appv1.FailureCode_FAILURE_CODE_TIMEOUT
	case "permission_denied", "permission":
		return appv1.FailureCode_FAILURE_CODE_PERMISSION
	case "authentication_required", "authentication":
		return appv1.FailureCode_FAILURE_CODE_AUTHENTICATION
	case "invalid_ref", "invalid_input":
		return appv1.FailureCode_FAILURE_CODE_INVALID_INPUT
	case "not_found", "source_not_found", "unknown_short_ref":
		return appv1.FailureCode_FAILURE_CODE_NOT_FOUND
	case "internal", "command_failed", "sync_failed":
		return appv1.FailureCode_FAILURE_CODE_INTERNAL
	default:
		return appv1.FailureCode_FAILURE_CODE_UNAVAILABLE
	}
}

func appOutcome(successes, failures int) appv1.OperationOutcome {
	if failures == 0 {
		return appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	}
	if successes == 0 {
		return appv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	}
	return appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
}

func appSyncOutcome(complete, partial, failed int) appv1.OperationOutcome {
	if partial > 0 || (complete > 0 && failed > 0) {
		return appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	}
	if failed > 0 {
		return appv1.OperationOutcome_OPERATION_OUTCOME_FAILED
	}
	return appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
}
