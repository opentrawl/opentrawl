package photospipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/applelibrary"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturefetch"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
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
	photosArchive              *photosarchive.PhotosArchive
	readAppleLibrary           *readAppleLibraryBinding
	fetchPicture               picturefetch.FetchPictureStep
	lookUpPlacesNearSpot       *placelookup.LookUpPlacesNearSpotStep
	runPlaceLookupDuringUpdate bool
	photoCardWriting           PhotoCardWriting
}

// PhotoCardWriting is what write_photo_card needs. CardModelClient is nil when
// the Photos configuration names no card model.
type PhotoCardWriting struct {
	CardModelClient photocard.CardModelClient
	KnownPlacesFile *knownplaces.KnownPlacesFile
	// Fetches a picture again when the cache has deleted it. It always has a
	// picture cache, even in a run that stores nothing in the archive.
	FetchPictureIntoCache picturefetch.FetchPictureStep
}

func NewPhotosPipeline(
	photosArchive *photosarchive.PhotosArchive,
	applePhotosLibraryPath string,
	fetchPicture picturefetch.FetchPictureStep,
	photonBaseURL string,
	runPlaceLookupDuringUpdate bool,
	photoCardWriting PhotoCardWriting,
) *PhotosPipeline {
	return &PhotosPipeline{
		photosArchive:              photosArchive,
		readAppleLibrary:           &readAppleLibraryBinding{applePhotosLibraryPath: applePhotosLibraryPath},
		fetchPicture:               fetchPicture,
		lookUpPlacesNearSpot:       placelookup.NewLookUpPlacesNearSpotStep(photonBaseURL),
		runPlaceLookupDuringUpdate: runPlaceLookupDuringUpdate,
		photoCardWriting:           photoCardWriting,
	}
}

// ErrNoCardModelConfigured stops write_photo_card before it selects work.
var ErrNoCardModelConfigured = errors.New("the Photos configuration names no card model")

func (photosPipeline *PhotosPipeline) LastAppleLibrarySnapshotStoreCounts() photosarchive.AppleLibrarySnapshotStoreCounts {
	return photosPipeline.readAppleLibrary.appleLibrarySnapshotStoreCounts
}

// RunPhotosStep runs one step by name. The runner is written out for the four
// steps; it is not a general workflow framework.
func (photosPipeline *PhotosPipeline) RunPhotosStep(
	ctx context.Context,
	photosStepName photosstep.PhotosStepName,
	runOptions PhotosStepRunOptions,
) (PhotosStepRunResult, error) {
	switch photosStepName {
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY:
		return runPhotosStep(ctx, photosPipeline.photosArchive, applelibrary.ReadAppleLibraryStep{}, photosPipeline.readAppleLibrary, runOptions)
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE:
		return runPhotosStep(ctx, photosPipeline.photosArchive, photosPipeline.fetchPicture,
			fetchPictureBinding{
				pictureCache:              photosPipeline.fetchPicture.PictureCache,
				pictureLongEdgePixelLimit: photosPipeline.fetchPicture.ResolvedPictureLongEdgePixelLimit(),
			}, runOptions)
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT:
		return runPhotosStep(ctx, photosPipeline.photosArchive, photosPipeline.lookUpPlacesNearSpot, lookUpPlacesNearSpotBinding{}, runOptions)
	case photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD:
		return photosPipeline.runWritePhotoCard(ctx, runOptions)
	default:
		return PhotosStepRunResult{}, fmt.Errorf("unknown Photos step %s", photosStepName)
	}
}

// RunPhotosUpdateChain refreshes Apple's facts, runs configured automatic
// enrichment steps, and reports steps that are not built yet. fetch_picture
// runs explicitly via `trawl photos run` until carding waves exist, and
// look_up_places_near_spot joins automatic updates only when photon_base_url
// is explicitly configured.
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
		if !PhotosStepIsBuilt(photosStepName) {
			stepRunResults = append(stepRunResults, PhotosStepRunResult{
				PhotosStepName: photosStepName,
				StopReason:     photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_STEP_NOT_IMPLEMENTED,
			})
			continue
		}
		if photosStepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE {
			continue
		}
		if photosStepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD {
			stepRunResults = append(stepRunResults, PhotosStepRunResult{
				PhotosStepName: photosStepName,
				StopReason:     photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_CARD_RUNS_NOT_APPROVED,
			})
			continue
		}
		if photosStepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT && !photosPipeline.runPlaceLookupDuringUpdate {
			continue
		}
		stepRunResult, err := photosPipeline.RunPhotosStep(ctx, photosStepName, runOptions)
		if err != nil {
			return nil, err
		}
		stepRunResults = append(stepRunResults, stepRunResult)
	}
	return stepRunResults, nil
}

// runWritePhotoCard selects ready photos once and re-fetches any picture the
// cache has deleted right before the card model reads it. With --no-write, an
// evicted picture is fetched into a temporary folder and deleted afterwards so
// neither the archive nor the picture cache is changed.
func (photosPipeline *PhotosPipeline) runWritePhotoCard(ctx context.Context, runOptions PhotosStepRunOptions) (PhotosStepRunResult, error) {
	cardWriting := photosPipeline.photoCardWriting
	if cardWriting.CardModelClient == nil {
		return PhotosStepRunResult{}, ErrNoCardModelConfigured
	}
	if closer, ok := cardWriting.CardModelClient.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}
	cardBinding := writePhotoCardBinding{
		knownPlacesFile:   cardWriting.KnownPlacesFile,
		cardModelProvider: cardWriting.CardModelClient.CardModelProvider(),
		cardModelName:     cardWriting.CardModelClient.CardModelName(),
		cardModelEffort:   cardWriting.CardModelClient.CardModelEffort(),
	}
	step := photocard.WritePhotoCardStep{
		CardModelClient:       cardWriting.CardModelClient,
		PictureCache:          cardWriting.FetchPictureIntoCache.PictureCache,
		RefetchEvictedPicture: photosPipeline.evictedPictureRefetcher(cardWriting.FetchPictureIntoCache, runOptions.StoreStepOutputs),
	}
	return runPhotosStep(ctx, photosPipeline.photosArchive, step, cardBinding, runOptions)
}

func (photosPipeline *PhotosPipeline) evictedPictureRefetcher(
	fetchPictureStep picturefetch.FetchPictureStep,
	storeStepOutputs bool,
) photocard.EvictedPictureRefetcher {
	return func(ctx context.Context, applePhotoUUID string) (string, string, *int32, func(), string, error) {
		storedFacts, err := photosPipeline.photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, []string{applePhotoUUID})
		if err != nil {
			return "", "", nil, nil, "", err
		}
		if len(storedFacts) == 0 {
			return "", "", nil, nil, "Apple photo is not in the archive.", nil
		}
		facts := storedFacts[0]
		fetchRequest := &photosstep.FetchPictureRequest{
			ApplePhotoUuid:         facts.GetApplePhotoUuid(),
			CaptureTimeUtc:         facts.GetCaptureLocalTime().GetCaptureTimeUtc(),
			AppleMediaKind:         facts.GetAppleMediaKind(),
			AppleAdjustmentsState:  facts.GetAppleAdjustmentsState(),
			AppleAdjustmentTimeUtc: facts.GetAppleAdjustmentTimeUtc(),
		}
		activeFetchStep := fetchPictureStep
		var cleanup func()
		if !storeStepOutputs {
			temporaryCacheFolder, err := os.MkdirTemp("", "opentrawl-photo-card-picture-")
			if err != nil {
				return "", "", nil, nil, "", fmt.Errorf("make a temporary picture folder: %w", err)
			}
			cleanup = func() { _ = os.RemoveAll(temporaryCacheFolder) }
			activeFetchStep.PictureCache = picturecache.NewPictureCache(temporaryCacheFolder)
		}
		fetchResult, err := activeFetchStep.RunPhotosStepOnOneInput(ctx, fetchRequest)
		if err != nil {
			return "", "", nil, cleanup, "", err
		}
		if storeStepOutputs {
			if err := photosPipeline.photosArchive.StorePictureFetchResult(ctx, fetchResult, time.Now()); err != nil {
				return "", "", nil, cleanup, "", err
			}
		}
		if fetchResult.GetPictureFetchOutcome() != photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED {
			failureMessage := fetchResult.GetPictureFetchFailureMessage()
			if failureMessage == "" {
				failureMessage = "The evicted picture could not be fetched again."
			}
			return "", "", nil, cleanup, failureMessage, nil
		}
		pictureFilePath, pictureIsCached := activeFetchStep.PictureCache.CachedPictureFilePath(fetchResult.GetPictureSha256())
		if !pictureIsCached {
			return "", "", nil, cleanup, "The re-fetched picture is not in the picture cache.", nil
		}
		return pictureFilePath, fetchResult.GetPictureSha256(), fetchResult.PictureLongEdgePixelLimitUsed, cleanup, "", nil
	}
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
