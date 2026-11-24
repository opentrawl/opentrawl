package photocard

import (
	"context"
	"errors"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const cardModelCallRetryDelay = 10 * time.Minute

// EvictedPictureRefetcher fetches a photo's picture again right before the
// card model reads it when the picture cache has deleted that JPEG.
type EvictedPictureRefetcher func(ctx context.Context, applePhotoUUID string) (
	pictureJPEGFilePath string,
	pictureSHA256 string,
	pictureLongEdgePixelLimitUsed *int32,
	cleanup func(),
	failureMessage string,
	err error,
)

// WritePhotoCardStep makes one model call per photo, and one more when the
// first answer does not follow the asked format.
type WritePhotoCardStep struct {
	CardModelClient       CardModelClient
	PictureCache          *picturecache.PictureCache
	RefetchEvictedPicture EvictedPictureRefetcher
}

func (WritePhotoCardStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD
}

func (step WritePhotoCardStep) RunPhotosStepOnOneInput(ctx context.Context, writePhotoCardRequest *photosstep.WritePhotoCardRequest) (*photo.PhotoCard, error) {
	modelInput := writePhotoCardRequest.GetPhotoCardModelInput()
	photoCard := &photo.PhotoCard{
		ApplePhotoUuid:                modelInput.GetApplePhotoUuid(),
		PhotoCardModelInputTextUsed:   modelInput.GetPhotoCardModelInputText(),
		PhotoCardModelInputSha256Used: modelInput.GetPhotoCardModelInputSha256(),
		PictureSha256Used:             writePhotoCardRequest.GetPictureSha256(),
		PictureLongEdgePixelLimitUsed: writePhotoCardRequest.PictureLongEdgePixelLimitUsed,
		CardModelProvider:             step.CardModelClient.CardModelProvider(),
		CardModelName:                 step.CardModelClient.CardModelName(),
		CardModelEffort:               step.CardModelClient.CardModelEffort(),
	}
	pictureJPEGFilePath, pictureIsCached := step.PictureCache.CachedPictureFilePath(writePhotoCardRequest.GetPictureSha256())
	if !pictureIsCached && step.RefetchEvictedPicture != nil {
		refetchedPath, refetchedSHA256, refetchedPixelLimit, cleanup, failureMessage, err := step.RefetchEvictedPicture(ctx, modelInput.GetApplePhotoUuid())
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return nil, err
		}
		if failureMessage != "" {
			return failedPhotoCard(photoCard, failureMessage), nil
		}
		pictureJPEGFilePath = refetchedPath
		pictureIsCached = pictureJPEGFilePath != ""
		if refetchedSHA256 != "" {
			photoCard.PictureSha256Used = refetchedSHA256
		}
		if refetchedPixelLimit != nil {
			photoCard.PictureLongEdgePixelLimitUsed = refetchedPixelLimit
		}
	}
	if !pictureIsCached {
		return failedPhotoCard(photoCard, "The picture is not in the picture cache."), nil
	}
	for attempt := 1; ; attempt++ {
		modelAnswer, err := step.CardModelClient.AnswerPhotoCardModelInput(ctx, modelInput.GetPhotoCardModelInputText(), pictureJPEGFilePath)
		var callFailed *CardModelCallFailedError
		if errors.As(err, &callFailed) {
			return failedPhotoCard(photoCard, callFailed.CardModelCallFailureMessage), nil
		}
		if err != nil {
			return nil, err
		}
		photoCard.RawModelAnswer = modelAnswer.RawModelAnswer
		photoCard.InputTokens = addTokenCounts(photoCard.InputTokens, modelAnswer.InputTokens)
		photoCard.OutputTokens = addTokenCounts(photoCard.OutputTokens, modelAnswer.OutputTokens)
		photoCard.ReasoningTokens = addTokenCounts(photoCard.ReasoningTokens, modelAnswer.ReasoningTokens)
		answer, err := parsePhotoCardAnswer(modelAnswer.RawModelAnswer)
		if err != nil {
			if attempt < 2 {
				continue
			}
			photoCard.PhotoCardOutcome = photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_ANSWER_INVALID
			photoCard.PhotoCardFailureMessage = "Twice the answer did not follow the asked format: " + err.Error()
			return photoCard, nil
		}
		photoCard.PhotoCardOutcome = photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN
		photoCard.OneLineSummary = answer.oneLineSummary
		photoCard.Description = answer.description
		photoCard.VisibleTextGroups = answer.visibleTextGroups
		photoCard.PlaceShownName = answer.placeShownName
		photoCard.PlaceShownReasoning = answer.placeShownReasoning
		return photoCard, nil
	}
}

func failedPhotoCard(photoCard *photo.PhotoCard, failureMessage string) *photo.PhotoCard {
	photoCard.PhotoCardOutcome = photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED
	photoCard.PhotoCardFailureMessage = failureMessage
	photoCard.RetryNotBeforeTimeUtc = timestamppb.New(time.Now().Add(cardModelCallRetryDelay))
	return photoCard
}

func addTokenCounts(total, more *int64) *int64 {
	if more == nil {
		return total
	}
	if total == nil {
		return new(*more)
	}
	return new(*total + *more)
}
