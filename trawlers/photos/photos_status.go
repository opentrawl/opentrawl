package photos

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlkit"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	archiveStatus := &status.TrawlerArchiveStatus{}
	pipelineStatus := &photosstep.PhotosPipelineStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: archiveStatus, PhotosPipelineStatus: pipelineStatus}
	for _, stepName := range photospipeline.AllPhotosStepNamesInOrder() {
		pipelineStatus.StepsInExecutionOrder = append(pipelineStatus.StepsInExecutionOrder, &photosstep.PhotosStepStatus{
			PhotosStepName: stepName,
			StepIsBuilt:    stepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY,
		})
	}
	archive, archiveExists, err := photosarchive.UseExistingPhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
	if err != nil || !archiveExists {
		return response, err
	}
	lastRuns, err := archive.ReadPhotosStepLastRuns(ctx)
	if err != nil {
		return nil, err
	}
	parameters, err := c.placeLookupRequestParameters()
	if err != nil {
		return nil, err
	}
	counts, err := archive.CountPhotosStepWork(ctx, time.Now(), parameters)
	if err != nil {
		return nil, err
	}
	archiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{{
		ArchiveContentKindName: "apple_library_photos",
		ArchiveContentCount:    uint64(counts.ApplePhotosInLibrary),
	}}
	waitingCounts := []int{0, counts.ApplePhotosWaitingForPictureFetch, counts.CameraSpotLookupsWaiting,
		counts.ApplePhotosWithoutCardModelInput, counts.ApplePhotosWithoutCardModelInput + counts.PhotoCardModelInputsWaitingForCard}
	for index, stepStatus := range pipelineStatus.StepsInExecutionOrder {
		if index > 0 {
			waitingCount := uint64(waitingCounts[index])
			stepStatus.WaitingItemCount = &waitingCount
		}
		stepStatus.LastRunStopReason = lastRuns[stepStatus.PhotosStepName].LastRunStopReason
		if stepStatus.PhotosStepName == photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_CARD_MODEL_INPUT {
			stepStatus.WaitingOnEarlierSteps = counts.ApplePhotosReadyForCardModelInput == 0 && counts.ApplePhotosWithoutCardModelInput > 0
		}
	}
	return response, nil
}
