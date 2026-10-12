package photospipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/applelibrary"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
)

type PhotosStepRunOptions struct {
	WorkSelection PhotosStepWorkSelection
	// StoreStepOutputs is false for --no-write: the step runs and nothing is
	// stored.
	StoreStepOutputs bool
	// ObservePhotosStepItem receives each step input and output, for a person
	// reading one step's exact data.
	ObservePhotosStepItem func(stepInput, stepOutput proto.Message)
	PhotosRunLog          *cklog.Run
}

type PhotosStepRunResult struct {
	PhotosStepName photosstep.PhotosStepName
	ItemCount      int
	StopReason     photosstep.PhotosStepRunStopReason
}

// PhotosPipeline runs the built Photos steps against their archive bindings.
type PhotosPipeline struct {
	photosArchive    *photosarchive.PhotosArchive
	readAppleLibrary *readAppleLibraryBinding
}

func NewPhotosPipeline(photosArchive *photosarchive.PhotosArchive, applePhotosLibraryPath string) *PhotosPipeline {
	return &PhotosPipeline{
		photosArchive:    photosArchive,
		readAppleLibrary: &readAppleLibraryBinding{applePhotosLibraryPath: applePhotosLibraryPath},
	}
}

func (photosPipeline *PhotosPipeline) LastAppleLibrarySnapshotStoreCounts() photosarchive.AppleLibrarySnapshotStoreCounts {
	return photosPipeline.readAppleLibrary.appleLibrarySnapshotStoreCounts
}

// RunPhotosStep runs one step by name. The runner is written out for the five
// steps; it is not a general workflow framework.
func (photosPipeline *PhotosPipeline) RunPhotosStep(
	ctx context.Context,
	photosStepName photosstep.PhotosStepName,
	runOptions PhotosStepRunOptions,
) (PhotosStepRunResult, error) {
	switch photosStepName {
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY:
		return runPhotosStep(ctx, photosPipeline.photosArchive, applelibrary.ReadAppleLibraryStep{}, photosPipeline.readAppleLibrary, runOptions)
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_CARD_MODEL_INPUT,
		photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD:
		return PhotosStepRunResult{
			PhotosStepName: photosStepName,
			StopReason:     photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_STEP_NOT_IMPLEMENTED,
		}, nil
	default:
		return PhotosStepRunResult{}, fmt.Errorf("unknown Photos step %s", photosStepName)
	}
}

// RunPhotosUpdateChain refreshes Apple's facts and reports the later steps
// that are not built. An unbuilt step is not a failed facts refresh.
func (photosPipeline *PhotosPipeline) RunPhotosUpdateChain(ctx context.Context, photosRunLog *cklog.Run) ([]PhotosStepRunResult, error) {
	runOptions := PhotosStepRunOptions{
		WorkSelection:    PhotosStepWorkSelection{Limit: photosarchive.NoWorkListLimit, SelectionTime: time.Now()},
		StoreStepOutputs: true,
		PhotosRunLog:     photosRunLog,
	}
	readAppleLibraryResult, err := photosPipeline.RunPhotosStep(ctx, photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY, runOptions)
	if err != nil {
		return nil, err
	}
	stepRunResults := []PhotosStepRunResult{readAppleLibraryResult}
	for _, photosStepName := range AllPhotosStepNamesInOrder()[1:] {
		stepRunResults = append(stepRunResults, PhotosStepRunResult{
			PhotosStepName: photosStepName,
			StopReason:     photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_STEP_NOT_IMPLEMENTED,
		})
	}
	return stepRunResults, nil
}

func runPhotosStep[StepInput, StepOutput proto.Message](
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	step PhotosStepRunsOnOneInput[StepInput, StepOutput],
	binding PhotosStepArchiveBinding[StepInput, StepOutput],
	runOptions PhotosStepRunOptions,
) (PhotosStepRunResult, error) {
	runResult := PhotosStepRunResult{PhotosStepName: step.PhotosStepName()}
	runStartedTime := time.Now()
	pendingStepInputs, err := binding.SelectPendingPhotosStepInputsNewestFirst(ctx, photosArchive, runOptions.WorkSelection)
	if err != nil {
		return runResult, fmt.Errorf("select pending work of %s: %w", PhotosStepCommandName(runResult.PhotosStepName), err)
	}
	for _, stepInput := range pendingStepInputs {
		if runOptions.WorkSelection.RetryPermanentlyFailedWork && runOptions.StoreStepOutputs {
			if err := photosArchive.ClearPermanentlyFailedPhotosStepOutput(ctx, stepInput); err != nil {
				return runResult, err
			}
		}
		itemStartedTime := time.Now()
		stepOutput, err := step.RunPhotosStepOnOneInput(ctx, stepInput)
		if err != nil {
			writePhotosRunLogLine(runOptions.PhotosRunLog, runResult.PhotosStepName, "step_item_failed",
				fmt.Sprintf("duration_ms=%d error=%q", time.Since(itemStartedTime).Milliseconds(), err.Error()))
			return runResult, fmt.Errorf("run %s: %w", PhotosStepCommandName(runResult.PhotosStepName), err)
		}
		if runOptions.StoreStepOutputs {
			if err := binding.StorePhotosStepOutput(ctx, photosArchive, stepInput, stepOutput); err != nil {
				return runResult, fmt.Errorf("store output of %s: %w", PhotosStepCommandName(runResult.PhotosStepName), err)
			}
		}
		if runOptions.ObservePhotosStepItem != nil {
			runOptions.ObservePhotosStepItem(stepInput, stepOutput)
		}
		runResult.ItemCount++
		writePhotosRunLogLine(runOptions.PhotosRunLog, runResult.PhotosStepName, "step_item_finished",
			fmt.Sprintf("duration_ms=%d stored=%t", time.Since(itemStartedTime).Milliseconds(), runOptions.StoreStepOutputs))
	}
	runResult.StopReason = photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_NOTHING_PENDING
	writePhotosRunLogLine(runOptions.PhotosRunLog, runResult.PhotosStepName, "step_finished",
		fmt.Sprintf("items=%d duration_ms=%d", runResult.ItemCount, time.Since(runStartedTime).Milliseconds()))
	return runResult, recordPhotosStepLastRunWhenItCoveredAllWork(ctx, photosArchive, runOptions, runResult, runStartedTime)
}

// Only a run over all pending work that stored its output is a step's last
// run; a run on one photo or a few is a person trying the step.
func recordPhotosStepLastRunWhenItCoveredAllWork(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	runOptions PhotosStepRunOptions,
	runResult PhotosStepRunResult,
	runStartedTime time.Time,
) error {
	if !runOptions.WorkSelection.selectsAllPendingWork() || !runOptions.StoreStepOutputs {
		return nil
	}
	return photosArchive.RecordPhotosStepLastRun(ctx, photosarchive.PhotosStepLastRun{
		PhotosStepName:      runResult.PhotosStepName,
		LastRunStartedTime:  runStartedTime,
		LastRunFinishedTime: time.Now(),
		LastRunItemCount:    runResult.ItemCount,
		LastRunStopReason:   runResult.StopReason,
	})
}

func writePhotosRunLogLine(photosRunLog *cklog.Run, photosStepName photosstep.PhotosStepName, event, message string) {
	if photosRunLog == nil {
		return
	}
	_ = photosRunLog.Info(event, strings.TrimSpace("step="+PhotosStepCommandName(photosStepName)+" "+message))
}
