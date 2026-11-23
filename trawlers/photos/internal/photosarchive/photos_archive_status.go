package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type CurrentAppleLibraryPhotoWeeklyCount struct {
	UTCWeekStartUnixSeconds int64
	RecordCount             uint64
}

type CurrentAppleLibraryPhotoWeeklyHistory struct {
	OldestCaptureTimeUTC time.Time
	WeeklyCounts         []CurrentAppleLibraryPhotoWeeklyCount
	UndatedPhotoCount    uint64
}

func (photosArchive *PhotosArchive) CountCurrentAppleLibraryPhotos(ctx context.Context) (uint64, error) {
	var currentAppleLibraryPhotoCount uint64
	if err := photosArchive.database.QueryRowContext(ctx, `
select count(*) from apple_library_photo where absent_from_apple_library_since_time_utc is null`,
	).Scan(&currentAppleLibraryPhotoCount); err != nil {
		return 0, fmt.Errorf("count current Apple library photos: %w", err)
	}
	return currentAppleLibraryPhotoCount, nil
}

func (photosArchive *PhotosArchive) NewestCurrentAppleLibraryPhotoCaptureTime(ctx context.Context) (time.Time, error) {
	var newestCaptureTimeUTC string
	err := photosArchive.database.QueryRowContext(ctx, `
select capture_time_utc from apple_library_photo where absent_from_apple_library_since_time_utc is null order by capture_time_utc desc limit 1`,
	).Scan(&newestCaptureTimeUTC)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read newest Apple library photo capture time: %w", err)
	}
	return parseArchiveTime(newestCaptureTimeUTC)
}

func (photosArchive *PhotosArchive) ReadCurrentAppleLibraryPhotoWeeklyHistory(
	ctx context.Context,
) (CurrentAppleLibraryPhotoWeeklyHistory, error) {
	var history CurrentAppleLibraryPhotoWeeklyHistory

	var oldestCaptureTimeUTC sql.NullString
	if err := photosArchive.database.QueryRowContext(ctx, `
select min(capture_time_utc)
from apple_library_photo
where absent_from_apple_library_since_time_utc is null
  and capture_time_utc <> ''`,
	).Scan(&oldestCaptureTimeUTC); err != nil {
		return history, fmt.Errorf("read oldest Apple library photo capture time: %w", err)
	}
	if oldestCaptureTimeUTC.Valid && strings.TrimSpace(oldestCaptureTimeUTC.String) != "" {
		if parsed, err := parseArchiveTime(oldestCaptureTimeUTC.String); err == nil {
			history.OldestCaptureTimeUTC = parsed
		}
	}

	// Group current photos into Monday 00:00:00 UTC weeks.
	rows, err := photosArchive.database.QueryContext(ctx, `
select
  case
    when length(capture_time_utc) >= 10
     and date(substr(capture_time_utc, 1, 10), 'weekday 0', '-6 days') is not null
    then cast(strftime('%s', date(substr(capture_time_utc, 1, 10), 'weekday 0', '-6 days')) as integer)
    else null
  end as utc_week_start_unix_seconds,
  count(*)
from apple_library_photo
where absent_from_apple_library_since_time_utc is null
group by utc_week_start_unix_seconds`)
	if err != nil {
		return history, fmt.Errorf("read Apple library photo weekly counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var weekStartUnix sql.NullInt64
		var count uint64
		if scanErr := rows.Scan(&weekStartUnix, &count); scanErr != nil {
			return history, fmt.Errorf("scan Apple library photo weekly count: %w", scanErr)
		}
		if count == 0 {
			continue
		}
		if !weekStartUnix.Valid {
			history.UndatedPhotoCount += count
			continue
		}
		history.WeeklyCounts = append(history.WeeklyCounts, CurrentAppleLibraryPhotoWeeklyCount{
			UTCWeekStartUnixSeconds: weekStartUnix.Int64,
			RecordCount:             count,
		})
	}
	if err := rows.Err(); err != nil {
		return history, err
	}
	sort.Slice(history.WeeklyCounts, func(i, j int) bool {
		return history.WeeklyCounts[i].UTCWeekStartUnixSeconds < history.WeeklyCounts[j].UTCWeekStartUnixSeconds
	})
	return history, nil
}
