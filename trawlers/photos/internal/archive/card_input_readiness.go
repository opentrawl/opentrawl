package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	photosmedia "github.com/opentrawl/opentrawl/trawlers/photos/internal/media"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

var selectCardInputLiveReadiness = photosmedia.NewInstalledOpenTrawlClient().InspectPhotoAssetReadiness

var preflightCardInputMedia = func(ctx context.Context, input classifyInput) (*mediawire.PhotoAssetReadiness, error) {
	readiness, err := selectCardInputLiveReadiness(ctx, input.LocalIdentifier)
	if err != nil {
		return nil, err
	}
	if err := validateCardInputLiveReadiness(input, readiness); err != nil {
		return nil, err
	}
	return readiness, nil
}

// CardInputReadiness records the one archive asset that matched the installed
// OpenTrawl application's live PhotoKit identity and resource facts.
// It proves no byte availability or export result.
type CardInputReadiness struct {
	AssetID string `json:"asset_id"`
}

// CardInputReadinessOptions names the canonical archive and any exact assets
// that the operator has already stopped and must not retry.
type CardInputReadinessOptions struct {
	CardInputAuditInventoryOptions
	ExcludedAssetIDs []string
}

// SelectCardInputReadyAsset chooses one unlocated live PhotoKit image through
// the installed OpenTrawl application, then verifies that the archive has the same canonical
// identity and the source facts required by both media boundaries.
func SelectCardInputReadyAsset(ctx context.Context, options CardInputReadinessOptions) (CardInputReadiness, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputReadiness{}, err
	}
	defer func() { _ = db.Close() }()
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return CardInputReadiness{}, err
	}
	if !complete {
		return CardInputReadiness{}, errors.New("photos archive snapshot is not complete")
	}
	input, err := selectCardInputArchiveCandidate(ctx, db.DB(), options.SourceLibraryID, options.ExcludedAssetIDs)
	if err != nil {
		return CardInputReadiness{}, err
	}
	_, err = preflightCardInputMedia(ctx, input)
	if err != nil {
		return CardInputReadiness{}, err
	}
	return CardInputReadiness{AssetID: input.AssetID}, nil
}

func selectCardInputArchiveCandidate(ctx context.Context, db *sql.DB, sourceLibraryID string, excludedAssetIDs []string) (classifyInput, error) {
	rows, err := db.QueryContext(ctx, `
		select a.id from asset a
		where a.source_library_id=? and a.source_state=? and a.media_type='image'
		  and not exists(select 1 from location_observation where asset_id=a.id)
		  and a.first_card_blocked_at is null
		order by a.creation_date, a.id`,
		strings.TrimSpace(sourceLibraryID), sourceStateCurrent,
	)
	if err != nil {
		return classifyInput{}, fmt.Errorf("select archive image candidate: %w", err)
	}
	var assetIDs []string
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			_ = rows.Close()
			return classifyInput{}, fmt.Errorf("scan archive image candidate: %w", err)
		}
		assetIDs = append(assetIDs, assetID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return classifyInput{}, fmt.Errorf("read archive image candidates: %w", err)
	}
	if err := rows.Close(); err != nil {
		return classifyInput{}, fmt.Errorf("close archive image candidates: %w", err)
	}
	excluded := make(map[string]bool, len(excludedAssetIDs))
	for _, assetID := range excludedAssetIDs {
		if assetID = strings.TrimSpace(assetID); assetID != "" {
			excluded[assetID] = true
		}
	}
	for _, assetID := range assetIDs {
		if excluded[assetID] {
			continue
		}
		input, err := loadCardInputAuditInput(ctx, db, sourceLibraryID, assetID)
		if err != nil {
			return classifyInput{}, err
		}
		return input, nil
	}
	return classifyInput{}, errors.New("archive has no current unlocated image candidate after stopped assets were excluded")
}

func validateCardInputLiveReadiness(input classifyInput, readiness *mediawire.PhotoAssetReadiness) error {
	archiveAssetUUID := photos.CanonicalAssetUUID(input.LocalIdentifier)
	liveAssetUUID := ""
	if readiness != nil {
		liveAssetUUID = photos.CanonicalAssetUUID(readiness.GetPhotoAssetLocalIdentifier())
	}
	if archiveAssetUUID == "" || liveAssetUUID == "" || !strings.EqualFold(archiveAssetUUID, liveAssetUUID) {
		return errors.New("live PhotoKit identity does not match the archive asset")
	}
	if input.SourceState != sourceStateCurrent || input.MediaType != "image" {
		return errors.New("live PhotoKit asset is not a current image in the archive")
	}
	if readiness.CreationTime == nil {
		return errors.New("live PhotoKit asset has no creation instant")
	}
	if !sameCardInputReadinessInstant(input.CreationDate, readiness.GetCreationTime().AsTime().Format(time.RFC3339Nano)) {
		return errors.New("live PhotoKit creation instant does not match the archive asset")
	}
	if uint64(input.Width) != readiness.GetPixelWidth() || uint64(input.Height) != readiness.GetPixelHeight() {
		return errors.New("live PhotoKit dimensions do not match the archive asset")
	}
	original := input.immutableOriginalIdentity()
	if original.OriginalFilename == "" || original.OriginalFilename != readiness.GetImmutableOriginalFilename() || (original.OriginalUTI != "" && original.OriginalUTI != readiness.GetImmutableOriginalUniformTypeIdentifier()) {
		return errors.New("live PhotoKit immutable-original resource does not match the archive asset")
	}
	current, err := input.currentStillRequest()
	if err != nil {
		return err
	}
	if modification, ok := current.Freshness.ExpectedModification(); ok {
		if readiness.ModificationTime == nil {
			return errors.New("live PhotoKit asset has no modification instant for current-still freshness")
		}
		observed := readiness.GetModificationTime().AsTime()
		expected := time.Unix(modification.UnixSeconds, int64(modification.Microseconds)*int64(time.Microsecond))
		if difference := observed.Sub(expected).Abs(); difference >= time.Millisecond {
			return fmt.Errorf("live PhotoKit current-still freshness differs from the archive asset by %s", difference)
		}
	}
	return nil
}

func sameCardInputReadinessInstant(left, right string) bool {
	leftInstant, leftErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(left))
	rightInstant, rightErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(right))
	return leftErr == nil && rightErr == nil && leftInstant.Sub(rightInstant).Abs() < time.Millisecond
}
