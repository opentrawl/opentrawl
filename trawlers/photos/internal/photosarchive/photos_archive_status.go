package photosarchive

import (
	"context"
	"fmt"
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
