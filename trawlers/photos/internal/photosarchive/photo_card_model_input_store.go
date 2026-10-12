package photosarchive

import (
	"context"
	"fmt"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

func (photosArchive *PhotosArchive) StorePhotoCardModelInputIfChanged(
	ctx context.Context,
	photoCardModelInput *photo.PhotoCardModelInput,
	writtenTime time.Time,
) error {
	_, err := photosArchive.database.ExecContext(ctx, `
insert into photo_card_model_input (
  apple_photo_uuid, photo_card_model_input_text, photo_card_model_input_sha256, written_time_utc
) values (?, ?, ?, ?)
on conflict(apple_photo_uuid) do update set
  photo_card_model_input_text = excluded.photo_card_model_input_text,
  photo_card_model_input_sha256 = excluded.photo_card_model_input_sha256,
  written_time_utc = excluded.written_time_utc
where photo_card_model_input.photo_card_model_input_text is not excluded.photo_card_model_input_text
   or photo_card_model_input.photo_card_model_input_sha256 is not excluded.photo_card_model_input_sha256`,
		photoCardModelInput.GetApplePhotoUuid(), photoCardModelInput.GetPhotoCardModelInputText(),
		photoCardModelInput.GetPhotoCardModelInputSha256(), formatArchiveTime(writtenTime))
	if err != nil {
		return fmt.Errorf("store changed photo card model input: %w", err)
	}
	return nil
}
