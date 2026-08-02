package updatephotos

import (
	"context"
	"errors"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/luna"
)

func photoModelGenerationUsage(result luna.GenerationResult) *archive.PhotoModelGenerationTokenUsage {
	if result.TokenUsage == nil {
		return nil
	}
	return &archive.PhotoModelGenerationTokenUsage{
		InputTokens:           result.TokenUsage.InputTokens,
		CachedInputTokens:     result.TokenUsage.CachedInputTokens,
		OutputTokens:          result.TokenUsage.OutputTokens,
		ReasoningOutputTokens: result.TokenUsage.ReasoningOutputTokens,
		TotalTokens:           result.TokenUsage.TotalTokens,
	}
}

func (worker *photoAssetWorker) deferRejectedPhotoModelResult(ctx context.Context, assetID archive.PhotoAssetID, inputSHA256 []byte, phase archive.PhotoModelGenerationPhase, threadIdentifier, turnIdentifier string, contractViolation error) error {
	failureDetail := contractViolation.Error()
	if err := archive.RejectRetainedPhotoModelGenerationResponse(ctx, worker.runner.options.OpenedArchiveStore, assetID, inputSHA256, phase, threadIdentifier, turnIdentifier, failureDetail, time.Now()); err != nil {
		return errors.Join(contractViolation, err)
	}
	return &AssetDeferredError{Reason: failureDetail}
}
