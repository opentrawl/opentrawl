package photos

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlkit"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	archiveStatus := &status.TrawlerArchiveStatus{}
	pipelineStatus := &photosstep.PhotosPipelineStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: archiveStatus, PhotosPipelineStatus: pipelineStatus}
	for _, stepName := range photospipeline.AllPhotosStepNamesInOrder() {
		pipelineStatus.StepsInExecutionOrder = append(pipelineStatus.StepsInExecutionOrder, &photosstep.PhotosStepStatus{
			PhotosStepName: stepName,
			StepIsBuilt:    photospipeline.PhotosStepIsBuilt(stepName),
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
	currentAppleLibraryPhotoCount, err := archive.CountCurrentAppleLibraryPhotos(ctx)
	if err != nil {
		return nil, err
	}
	newestCaptureTime, err := archive.NewestCurrentAppleLibraryPhotoCaptureTime(ctx)
	if err != nil {
		return nil, err
	}
	monthlyHistory, err := archive.ReadCurrentAppleLibraryPhotoMonthlyHistory(ctx)
	if err != nil {
		return nil, err
	}
	archiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{{
		ArchiveContentKindName: "apple_library_photos",
		ArchiveContentCount:    currentAppleLibraryPhotoCount,
	}}
	archiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	if !newestCaptureTime.IsZero() {
		archiveStatus.NewestArchivedRecordTime = timestamppb.New(newestCaptureTime)
	}
	if currentAppleLibraryPhotoCount > 0 || len(monthlyHistory.MonthlyCounts) > 0 || monthlyHistory.UndatedPhotoCount > 0 {
		protoHistory := &status.ArchiveMonthlyHistory{
			UndatedRecordCount: monthlyHistory.UndatedPhotoCount,
		}
		if !monthlyHistory.OldestCaptureTimeUTC.IsZero() {
			protoHistory.OldestArchivedRecordTime = timestamppb.New(monthlyHistory.OldestCaptureTimeUTC)
		}
		for _, bucket := range monthlyHistory.MonthlyCounts {
			protoHistory.MonthlyRecordCountsInChronologicalOrder = append(
				protoHistory.MonthlyRecordCountsInChronologicalOrder,
				&status.ArchiveMonthlyRecordCount{
					CalendarYear:  bucket.CalendarYear,
					CalendarMonth: bucket.CalendarMonth,
					RecordCount:   bucket.RecordCount,
				},
			)
		}
		archiveStatus.ArchiveMonthlyHistory = protoHistory
	}
	lastAppleLibraryRead := lastRuns[photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY]
	if lastAppleLibraryRead.LastRunStopReason == photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_NOTHING_PENDING {
		archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(lastAppleLibraryRead.LastRunFinishedTime)
	}
	for _, stepStatus := range pipelineStatus.StepsInExecutionOrder {
		stepStatus.LastRunStopReason = lastRuns[stepStatus.PhotosStepName].LastRunStopReason
	}
	return response, nil
}
