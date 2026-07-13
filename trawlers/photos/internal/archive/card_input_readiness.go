package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

var selectCardInputLiveReadiness = photos.AssetReadinessThroughApp

var preflightCardInputMedia = func(ctx context.Context, input classifyInput) (photos.AssetReadiness, error) {
	readiness, err := selectCardInputLiveReadiness(ctx, input.LocalIdentifier)
	if err != nil {
		return photos.AssetReadiness{}, err
	}
	if err := validateCardInputLiveReadiness(input, readiness); err != nil {
		return photos.AssetReadiness{}, err
	}
	return readiness, nil
}

// CardInputReadiness records the one archive asset that matched the signed
// helper's live PhotoKit identity and resource facts before either export.
// It proves no byte availability or export result.
type CardInputReadiness struct {
	AssetID string `json:"asset_id"`
}

// SelectCardInputReadyAsset chooses one unlocated live PhotoKit image through
// the signed helper, then verifies that the archive has the same canonical
// identity and the source facts required by both media boundaries.
func SelectCardInputReadyAsset(ctx context.Context, options CardInputAuditInventoryOptions) (CardInputReadiness, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputReadiness{}, err
	}
	defer db.Close()
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return CardInputReadiness{}, err
	}
	if !complete {
		return CardInputReadiness{}, errors.New("Photos archive snapshot is not complete")
	}
	input, err := selectCardInputArchiveCandidate(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return CardInputReadiness{}, err
	}
	_, err = preflightCardInputMedia(ctx, input)
	if err != nil {
		return CardInputReadiness{}, err
	}
	return CardInputReadiness{AssetID: input.AssetID}, nil
}

func selectCardInputArchiveCandidate(ctx context.Context, db *sql.DB, sourceLibraryID string) (classifyInput, error) {
	var assetID string
	err := db.QueryRowContext(ctx, `
		select a.id from asset a
		where a.source_library_id=? and a.source_state=? and a.media_type='image'
		  and not exists(select 1 from location_observation where asset_id=a.id)
		  and a.first_card_blocked_at is null
		order by a.creation_date, a.id limit 1`,
		strings.TrimSpace(sourceLibraryID), sourceStateCurrent,
	).Scan(&assetID)
	if errors.Is(err, sql.ErrNoRows) {
		return classifyInput{}, errors.New("archive has no current unlocated image candidate")
	}
	if err != nil {
		return classifyInput{}, fmt.Errorf("select archive image candidate: %w", err)
	}
	return loadCardInputAuditInput(ctx, db, sourceLibraryID, assetID)
}

func validateCardInputLiveReadiness(input classifyInput, readiness photos.AssetReadiness) error {
	if photos.CanonicalAssetUUID(input.LocalIdentifier) == "" || !strings.EqualFold(photos.CanonicalAssetUUID(input.LocalIdentifier), readiness.AssetUUID) {
		return errors.New("live PhotoKit identity does not match the archive asset")
	}
	if input.SourceState != sourceStateCurrent || input.MediaType != "image" || input.HasLocation || readiness.MediaType != "image" || readiness.HasLocation {
		return errors.New("live PhotoKit asset is not a current unlocated image in the archive")
	}
	if input.CreationDate != readiness.CreationDate || input.Width != readiness.PixelWidth || input.Height != readiness.PixelHeight {
		return errors.New("live PhotoKit immutable-original facts do not match the archive asset")
	}
	original := input.originalRequest().Query
	if original.OriginalFilename == "" || original.OriginalFilename != readiness.OriginalFilename || (original.OriginalUTI != "" && original.OriginalUTI != readiness.OriginalUTI) {
		return errors.New("live PhotoKit immutable-original resource does not match the archive asset")
	}
	current, err := input.currentStillRequest()
	if err != nil {
		return err
	}
	if modification, ok := current.Freshness.ExpectedModification(); ok {
		observed, err := photos.ParseCurrentStillModification(readiness.ModificationDate)
		if err != nil || observed != modification {
			return errors.New("live PhotoKit current-still freshness does not match the archive asset")
		}
	}
	return nil
}
