package photosarchive

import (
	"context"
	"database/sql"
	"fmt"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// replacePhotoSearchRow writes the photo's one search row from its Apple facts
// and, when the card was written, the card's text. Addressing photo_search by
// apple_library_photo's stable rowid avoids scanning the FTS table on every write.
func replacePhotoSearchRow(ctx context.Context, transaction *sql.Tx, applePhotoFacts *photo.AppleLibraryPhotoFacts, photoCard *photo.PhotoCard) error {
	applePhotoUUID := applePhotoFacts.GetApplePhotoUuid()
	var photoRowID int64
	if err := transaction.QueryRowContext(ctx, `select rowid from apple_library_photo where apple_photo_uuid = ?`, applePhotoUUID).Scan(&photoRowID); err != nil {
		return fmt.Errorf("read photo rowid for search row: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `delete from photo_search where rowid = ?`, photoRowID); err != nil {
		return fmt.Errorf("clear search row of photo: %w", err)
	}
	var columnTexts [photoSearchIndexedColumnCount]string
	for _, source := range photoSearchSourceTexts(applePhotoFacts, photoCard) {
		columnTexts[source.indexColumn] = joinNonEmptyLines(columnTexts[source.indexColumn], source.sourceText)
	}
	arguments := []any{photoRowID, applePhotoUUID}
	for _, columnText := range columnTexts {
		arguments = append(arguments, SearchTextWithEastAsianCharactersSeparated(columnText))
	}
	if _, err := transaction.ExecContext(ctx, `
insert into photo_search (
  rowid, apple_photo_uuid, apple_caption_and_title, apple_place_names, apple_named_people,
  apple_album_titles, apple_moment_title, apple_recognised_text,
  card_one_line_summary, card_description, card_visible_text, card_place_shown_name
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, arguments...); err != nil {
		return fmt.Errorf("write search row of photo: %w", err)
	}
	return nil
}
