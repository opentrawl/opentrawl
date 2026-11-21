package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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
