package photos

import (
	"context"
	"flag"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
)

type photosStepRunCommandFlags struct {
	onlyApplePhotoUUID         string
	limit                      int
	runWithoutStoringOutput    bool
	retryPermanentlyFailedWork bool
	remakeStaleCards           bool
}

func (c *Crawler) registerPhotosStepRunCommandFlags(flagSet *flag.FlagSet) {
	c.runCommandFlags = photosStepRunCommandFlags{limit: photosarchive.NoWorkListLimit}
	flagSet.StringVar(&c.runCommandFlags.onlyApplePhotoUUID, "photo", "", "Run the step on this one `PHOTO` link or Apple photo UUID")
	flagSet.IntVar(&c.runCommandFlags.limit, "limit", photosarchive.NoWorkListLimit, "Run the step on at most `N` pending items, newest first")
	flagSet.BoolVar(&c.runCommandFlags.runWithoutStoringOutput, "no-write", false, "Run the step and store nothing")
	flagSet.BoolVar(&c.runCommandFlags.retryPermanentlyFailedWork, "retry-failed", false, "Run again on items whose failure has no retry time")
	flagSet.BoolVar(&c.runCommandFlags.remakeStaleCards, "recard", false, "Remake cards whose card model input has changed")
}

func (c *Crawler) runPhotosStepCommand(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 1 {
		return nil, output.UsageError{Err: render.PhotosRunNeedsStep()}
	}
	photosStepName, ok := photospipeline.ParsePhotosStepCommandName(req.TrawlerCommandPositionalArguments[0])
	if !ok {
		return nil, output.UsageError{Err: render.PhotosStepNameIsUnknown(req.TrawlerCommandPositionalArguments[0])}
	}
	if !photospipeline.PhotosStepIsBuilt(photosStepName) {
		return nil, render.PhotosStepsNotBuilt([]photosstep.PhotosStepName{photosStepName})
	}

	runFlags := c.runCommandFlags
	if runFlags.onlyApplePhotoUUID != "" {
		resolvedUUID, err := c.resolvePhotoArgument(ctx, req, runFlags.onlyApplePhotoUUID)
		if err != nil {
			return nil, err
		}
		runFlags.onlyApplePhotoUUID = resolvedUUID
	}
	if runFlags.onlyApplePhotoUUID != "" && runFlags.limit != photosarchive.NoWorkListLimit {
		return nil, output.UsageError{Err: render.OptionsCannotBeUsedTogether("photo", "limit")}
	}
	if runFlags.limit != photosarchive.NoWorkListLimit && runFlags.limit < 1 {
		return nil, output.UsageError{Err: render.PhotosRunLimitMustBePositive()}
	}
	if runFlags.remakeStaleCards && photosStepName != photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD {
		return nil, output.UsageError{Err: render.PhotosRecardNeedsCardStep()}
	}
	if photosStepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD && runFlags.limit == photosarchive.NoWorkListLimit && runFlags.onlyApplePhotoUUID == "" {
		return nil, output.UsageError{Err: render.PhotosCardRunNeedsSelection()}
	}
	applePhotosLibraryPath, err := c.applePhotosLibraryPath()
	if err != nil {
		return nil, err
	}
	var photosArchive *photosarchive.PhotosArchive
	if runFlags.runWithoutStoringOutput {
		if photosStepName != photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY {
			var archiveExists bool
			photosArchive, archiveExists, err = photosarchive.UseExistingPhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
			if err == nil && !archiveExists {
				err = trawlkit.NewMissingArchiveError(req.TrawlerArchivePaths.TrawlerArchivePath)
			}
		}
	} else {
		photosArchive, err = photosarchive.UsePhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
	}
	if err != nil {
		return nil, err
	}
	stepResponse := &photosstep.PhotosStepRunResponse{PhotosStepName: photosStepName}
	photosPipeline := photospipeline.NewPhotosPipeline(photosArchive, applePhotosLibraryPath)
	stepRunResult, err := photosPipeline.RunPhotosStep(ctx, photosStepName, photospipeline.PhotosStepRunOptions{
		WorkSelection: photospipeline.PhotosStepWorkSelection{
			OnlyApplePhotoUUID:          runFlags.onlyApplePhotoUUID,
			Limit:                       runFlags.limit,
			IncludeStaleCards:           runFlags.remakeStaleCards,
			RetryPermanentlyFailedWork:  runFlags.retryPermanentlyFailedWork,
			SelectionTime:               time.Now(),
			ReadWithoutCreatingSnapshot: runFlags.runWithoutStoringOutput,
		},
		StoreStepOutputs: !runFlags.runWithoutStoringOutput,
		ObservePhotosStepItem: func(stepInput, stepOutput proto.Message) {
			stepResponse.StepItems = append(stepResponse.StepItems, photosStepRunItem(stepInput, stepOutput))
		},
		PhotosRunLog: req.TrawlerCommandLog,
	})
	if err != nil {
		return nil, err
	}
	stepResponse.CompletedItemCount = uint64(stepRunResult.ItemCount)
	stepResponse.StopReason = stepRunResult.StopReason
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_PhotosStepRunResponse{PhotosStepRunResponse: stepResponse},
	}, nil
}

func photosStepRunItem(stepInput, stepOutput proto.Message) *photosstep.PhotosStepRunItem {
	item := &photosstep.PhotosStepRunItem{}
	switch input := stepInput.(type) {
	case *photosstep.ReadAppleLibraryRequest:
		item.StepInput = &photosstep.PhotosStepRunItem_ReadAppleLibraryRequest{ReadAppleLibraryRequest: input}
	case *photosstep.FetchPictureRequest:
		item.StepInput = &photosstep.PhotosStepRunItem_FetchPictureRequest{FetchPictureRequest: input}
	case *photosstep.LookUpPlacesNearSpotRequest:
		item.StepInput = &photosstep.PhotosStepRunItem_LookUpPlacesNearSpotRequest{LookUpPlacesNearSpotRequest: input}
	case *photosstep.WriteCardModelInputRequest:
		item.StepInput = &photosstep.PhotosStepRunItem_WriteCardModelInputRequest{WriteCardModelInputRequest: input}
	case *photosstep.WritePhotoCardRequest:
		item.StepInput = &photosstep.PhotosStepRunItem_WritePhotoCardRequest{WritePhotoCardRequest: input}
	}
	if stepOutput == nil || !stepOutput.ProtoReflect().IsValid() {
		return item
	}
	switch output := stepOutput.(type) {
	case *photosstep.AppleLibrarySnapshot:
		item.StepOutput = &photosstep.PhotosStepRunItem_AppleLibrarySnapshot{AppleLibrarySnapshot: output}
	case *photo.PictureFetchResult:
		item.StepOutput = &photosstep.PhotosStepRunItem_PictureFetchResult{PictureFetchResult: output}
	case *photo.NearbyPlacesLookupResult:
		item.StepOutput = &photosstep.PhotosStepRunItem_NearbyPlacesLookupResult{NearbyPlacesLookupResult: output}
	case *photo.PhotoCardModelInput:
		item.StepOutput = &photosstep.PhotosStepRunItem_PhotoCardModelInput{PhotoCardModelInput: output}
	case *photo.PhotoCard:
		item.StepOutput = &photosstep.PhotosStepRunItem_PhotoCard{PhotoCard: output}
	}
	return item
}
