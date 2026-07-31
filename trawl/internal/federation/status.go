package federation

import (
	"context"
	"strings"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

type statusRunResult struct {
	statusResult *federationv1.TrawlerStatusResult
	failure      *federationv1.TrawlerOperationFailure
	skip         *federationv1.TrawlerSkippedFromOperation
}

func Status(ctx context.Context, trawlers []StatusTrawler) *federationv1.FederatedTrawlerStatusOperation {
	results := make([]statusRunResult, len(trawlers))
	type completedStatus struct {
		index  int
		result statusRunResult
	}
	completed := make(chan completedStatus, len(trawlers))
	for index := range trawlers {
		go func(index int) {
			completed <- completedStatus{index: index, result: runStatusTrawler(ctx, trawlers[index])}
		}(index)
	}
	remaining := len(trawlers)
	for remaining > 0 {
		select {
		case completedStatus := <-completed:
			results[completedStatus.index] = completedStatus.result
			remaining--
		case <-ctx.Done():
			for index := range trawlers {
				if results[index].statusResult == nil && results[index].failure == nil && results[index].skip == nil {
					results[index].failure = FailureForError(
						trawlers[index].Manifest,
						federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
						ctx.Err(),
					)
				}
			}
			remaining = 0
		}
	}
	operation := &federationv1.FederatedTrawlerStatusOperation{}
	successfulTrawlerCount := 0
	for _, result := range results {
		switch {
		case result.skip != nil:
			operation.TrawlersSkippedFromOperation = append(operation.TrawlersSkippedFromOperation, result.skip)
		case result.failure != nil:
			operation.OperationFailures = append(operation.OperationFailures, result.failure)
		case result.statusResult != nil:
			operation.TrawlerStatusResults = append(operation.TrawlerStatusResults, result.statusResult)
			successfulTrawlerCount++
		}
	}
	operation.Outcome = aggregateOutcome(
		successfulTrawlerCount,
		len(operation.OperationFailures),
		len(operation.TrawlersSkippedFromOperation),
	)
	return operation
}

func runStatusTrawler(ctx context.Context, trawler StatusTrawler) (result statusRunResult) {
	if strings.TrimSpace(trawler.SkipReason) != "" {
		result.skip = skippedTrawler(trawler.Manifest, trawler.SkipReason)
		return result
	}
	if trawler.Run == nil {
		result.failure = operationFailure(
			trawler.Manifest,
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
			"callback is nil",
			federationv1.FailureCode_FAILURE_CODE_INTERNAL,
		)
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = statusRunResult{failure: panicFailure(
				trawler.Manifest,
				federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
				recovered,
			)}
		}
	}()
	statusResponse, failure := trawler.Run(ctx)
	if failure != nil {
		result.failure = callbackFailure(ctx, trawler.Manifest, failure)
		return result
	}
	if ctx.Err() != nil {
		result.failure = FailureForError(
			trawler.Manifest,
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
			ctx.Err(),
		)
		return result
	}
	if statusResponse == nil || statusResponse.GetTrawlerArchiveStatus() == nil {
		result.failure = operationFailure(
			trawler.Manifest,
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
			"trawler returned no typed status",
			federationv1.FailureCode_FAILURE_CODE_INTERNAL,
		)
		return result
	}
	result.statusResult = &federationv1.TrawlerStatusResult{
		RegisteredTrawler:            trawler.Manifest.GetRegisteredTrawler(),
		RegisteredTrawlerCommandName: trawler.Manifest.GetRegisteredTrawlerCommandName(),
		RegisteredTrawlerDisplayName: trawlerDisplayName(trawler.Manifest),
		TrawlerStatusResponse:        statusResponse,
	}
	return result
}
