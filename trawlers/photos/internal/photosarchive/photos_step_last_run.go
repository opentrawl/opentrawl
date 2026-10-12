package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

type PhotosStepLastRun struct {
	PhotosStepName      photosstep.PhotosStepName
	LastRunStartedTime  time.Time
	LastRunFinishedTime time.Time
	LastRunItemCount    int
	LastRunStopReason   photosstep.PhotosStepRunStopReason
}

func (photosArchive *PhotosArchive) RecordPhotosStepLastRun(ctx context.Context, stepLastRun PhotosStepLastRun) error {
	if _, err := photosArchive.database.ExecContext(ctx, `
insert or replace into photos_step_last_run (
  photos_step_name, last_run_started_time_utc, last_run_finished_time_utc, last_run_item_count, last_run_stop_reason
) values (?, ?, ?, ?, ?)`,
		stepLastRun.PhotosStepName.String(),
		formatArchiveTime(stepLastRun.LastRunStartedTime),
		formatArchiveTime(stepLastRun.LastRunFinishedTime),
		stepLastRun.LastRunItemCount,
		stepLastRun.LastRunStopReason.String(),
	); err != nil {
		return fmt.Errorf("record last run of %s: %w", stepLastRun.PhotosStepName, err)
	}
	return nil
}

func (photosArchive *PhotosArchive) ReadPhotosStepLastRuns(ctx context.Context) (map[photosstep.PhotosStepName]PhotosStepLastRun, error) {
	stepLastRuns := map[photosstep.PhotosStepName]PhotosStepLastRun{}
	err := scanStoredRows(ctx, photosArchive.database, `
select photos_step_name, last_run_started_time_utc, last_run_finished_time_utc, last_run_item_count, last_run_stop_reason
from photos_step_last_run`, nil, func(rows *sql.Rows) error {
		var stepName, startedTimeUTC, finishedTimeUTC, stopReason string
		var stepLastRun PhotosStepLastRun
		if err := rows.Scan(&stepName, &startedTimeUTC, &finishedTimeUTC, &stepLastRun.LastRunItemCount, &stopReason); err != nil {
			return err
		}
		var err error
		if stepLastRun.LastRunStartedTime, err = parseArchiveTime(startedTimeUTC); err != nil {
			return err
		}
		if stepLastRun.LastRunFinishedTime, err = parseArchiveTime(finishedTimeUTC); err != nil {
			return err
		}
		stepLastRun.PhotosStepName = photosstep.PhotosStepName(photosstep.PhotosStepName_value[stepName])
		stepLastRun.LastRunStopReason = photosstep.PhotosStepRunStopReason(photosstep.PhotosStepRunStopReason_value[stopReason])
		stepLastRuns[stepLastRun.PhotosStepName] = stepLastRun
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read last runs of Photos steps: %w", err)
	}
	return stepLastRuns, nil
}
