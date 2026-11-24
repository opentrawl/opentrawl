package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StorePhotoCard replaces the photo's card and its visible text, and rebuilds
// the photo's search row from its facts and the new card, in one transaction.
func (photosArchive *PhotosArchive) StorePhotoCard(ctx context.Context, photoCard *photo.PhotoCard, writtenTime time.Time) error {
	applePhotoUUID := photoCard.GetApplePhotoUuid()
	storedFacts, err := photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, []string{applePhotoUUID})
	if err != nil {
		return err
	}
	if len(storedFacts) == 0 {
		return fmt.Errorf("store card: Apple photo %s is not in the archive", applePhotoUUID)
	}
	transaction, err := photosArchive.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storing photo card: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	// Keep an existing written card for the same pixels if a --recard attempt
	// fails or returns an invalid answer, so search never loses a working card.
	if photoCard.GetPhotoCardOutcome() != photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN {
		var existingOutcome, existingPictureSHA256 string
		err := transaction.QueryRowContext(ctx,
			`select photo_card_outcome, picture_sha256_used from photo_card where apple_photo_uuid = ?`,
			applePhotoUUID,
		).Scan(&existingOutcome, &existingPictureSHA256)
		if err == nil &&
			existingOutcome == photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String() &&
			existingPictureSHA256 == photoCard.GetPictureSha256Used() {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read existing photo card before replacing: %w", err)
		}
	}
	pictureLongEdgePixelLimitUsed := any(nil)
	if photoCard.PictureLongEdgePixelLimitUsed != nil {
		pictureLongEdgePixelLimitUsed = photoCard.GetPictureLongEdgePixelLimitUsed()
	}
	if _, err := transaction.ExecContext(ctx, `
insert or replace into photo_card (
  apple_photo_uuid, photo_card_outcome, photo_card_model_input_text_used, photo_card_model_input_sha256_used,
  picture_sha256_used, picture_long_edge_pixel_limit_used, card_model_provider, card_model_name, card_model_effort,
  one_line_summary, description, place_shown_name, place_shown_reasoning, raw_model_answer, photo_card_failure_message,
  input_tokens, output_tokens, reasoning_tokens, retry_not_before_time_utc, written_time_utc
) values (?,?,?,?, ?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?)`,
		applePhotoUUID,
		photoCard.GetPhotoCardOutcome().String(),
		photoCard.GetPhotoCardModelInputTextUsed(),
		photoCard.GetPhotoCardModelInputSha256Used(),
		photoCard.GetPictureSha256Used(),
		pictureLongEdgePixelLimitUsed,
		photoCard.GetCardModelProvider().String(),
		photoCard.GetCardModelName(),
		photoCard.GetCardModelEffort(),
		emptyTextAsNull(photoCard.GetOneLineSummary()),
		emptyTextAsNull(photoCard.GetDescription()),
		emptyTextAsNull(photoCard.GetPlaceShownName()),
		emptyTextAsNull(photoCard.GetPlaceShownReasoning()),
		emptyTextAsNull(photoCard.GetRawModelAnswer()),
		emptyTextAsNull(photoCard.GetPhotoCardFailureMessage()),
		optionalInt64Column(photoCard.InputTokens),
		optionalInt64Column(photoCard.OutputTokens),
		optionalInt64Column(photoCard.ReasoningTokens),
		optionalArchiveTimeColumn(photoCard.GetRetryNotBeforeTimeUtc()),
		formatArchiveTime(writtenTime),
	); err != nil {
		return fmt.Errorf("store photo card: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `delete from photo_card_visible_text where apple_photo_uuid = ?`, applePhotoUUID); err != nil {
		return fmt.Errorf("replace visible text of photo card: %w", err)
	}
	for textGroupRank, visibleTextGroup := range photoCard.GetVisibleTextGroups() {
		if _, err := transaction.ExecContext(ctx, `
insert into photo_card_visible_text (apple_photo_uuid, text_group_rank, where_text_appears, text_as_written, english_rendering)
values (?, ?, ?, ?, ?)`,
			applePhotoUUID, textGroupRank, visibleTextGroup.GetWhereTextAppears(), visibleTextGroup.GetTextAsWritten(),
			emptyTextAsNull(visibleTextGroup.GetEnglishRendering()),
		); err != nil {
			return fmt.Errorf("store visible text of photo card: %w", err)
		}
	}
	if err := replacePhotoSearchRow(ctx, transaction, storedFacts[0], photoCard); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit photo card: %w", err)
	}
	return nil
}

// ReadStoredPhotoCard returns nil when the photo has no card.
func (photosArchive *PhotosArchive) ReadStoredPhotoCard(ctx context.Context, applePhotoUUID string) (*photo.PhotoCard, error) {
	return readStoredPhotoCard(ctx, photosArchive.database, applePhotoUUID)
}

func readStoredPhotoCard(ctx context.Context, database archiveQueryer, applePhotoUUID string) (*photo.PhotoCard, error) {
	var photoCard *photo.PhotoCard
	err := scanStoredRows(ctx, database, `
select photo_card_outcome, photo_card_model_input_text_used, photo_card_model_input_sha256_used,
  picture_sha256_used, picture_long_edge_pixel_limit_used, card_model_provider, card_model_name, card_model_effort,
  one_line_summary, description, place_shown_name, place_shown_reasoning, raw_model_answer, photo_card_failure_message,
  input_tokens, output_tokens, reasoning_tokens, retry_not_before_time_utc, written_time_utc
from photo_card where apple_photo_uuid = ?`, []any{applePhotoUUID},
		func(rows *sql.Rows) error {
			storedCard := &photo.PhotoCard{ApplePhotoUuid: applePhotoUUID}
			var outcome, provider, writtenTimeUTC string
			var summary, description, placeShownName, placeShownReasoning, rawAnswer, failureMessage, retryNotBefore sql.NullString
			var pictureLongEdgePixelLimitUsed, inputTokens, outputTokens, reasoningTokens sql.NullInt64
			if err := rows.Scan(&outcome, &storedCard.PhotoCardModelInputTextUsed, &storedCard.PhotoCardModelInputSha256Used,
				&storedCard.PictureSha256Used, &pictureLongEdgePixelLimitUsed, &provider, &storedCard.CardModelName, &storedCard.CardModelEffort,
				&summary, &description, &placeShownName, &placeShownReasoning, &rawAnswer, &failureMessage,
				&inputTokens, &outputTokens, &reasoningTokens, &retryNotBefore, &writtenTimeUTC); err != nil {
				return err
			}
			storedCard.PhotoCardOutcome = photo.PhotoCardOutcome(photo.PhotoCardOutcome_value[outcome])
			storedCard.CardModelProvider = photo.CardModelProvider(photo.CardModelProvider_value[provider])
			if pictureLongEdgePixelLimitUsed.Valid {
				limitUsed := int32(pictureLongEdgePixelLimitUsed.Int64)
				storedCard.PictureLongEdgePixelLimitUsed = &limitUsed
			}
			storedCard.OneLineSummary = summary.String
			storedCard.Description = description.String
			storedCard.PlaceShownName = placeShownName.String
			storedCard.PlaceShownReasoning = placeShownReasoning.String
			storedCard.RawModelAnswer = rawAnswer.String
			storedCard.PhotoCardFailureMessage = failureMessage.String
			storedCard.InputTokens = optionalInt64FromColumn(inputTokens)
			storedCard.OutputTokens = optionalInt64FromColumn(outputTokens)
			storedCard.ReasoningTokens = optionalInt64FromColumn(reasoningTokens)
			var err error
			if storedCard.RetryNotBeforeTimeUtc, err = optionalArchiveTimeFromColumn(retryNotBefore); err != nil {
				return err
			}
			writtenTime, err := parseArchiveTime(writtenTimeUTC)
			if err != nil {
				return err
			}
			storedCard.CardWrittenTimeUtc = timestamppb.New(writtenTime)
			photoCard = storedCard
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read stored photo card: %w", err)
	}
	if photoCard == nil {
		return nil, nil
	}
	if err := scanStoredRows(ctx, database, `
select where_text_appears, text_as_written, english_rendering from photo_card_visible_text
where apple_photo_uuid = ? order by text_group_rank`, []any{applePhotoUUID},
		func(rows *sql.Rows) error {
			visibleTextGroup := &photo.PhotoCardVisibleTextGroup{}
			var englishRendering sql.NullString
			if err := rows.Scan(&visibleTextGroup.WhereTextAppears, &visibleTextGroup.TextAsWritten, &englishRendering); err != nil {
				return err
			}
			visibleTextGroup.EnglishRendering = englishRendering.String
			photoCard.VisibleTextGroups = append(photoCard.VisibleTextGroups, visibleTextGroup)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("read stored visible text of photo card: %w", err)
	}
	return photoCard, nil
}
