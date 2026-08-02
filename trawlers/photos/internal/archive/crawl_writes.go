package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func (c *updateImporter) insertResource(ctx context.Context, assetID string, resource photos.Resource) error {
	resourceID := stableID("asset_resource", assetID, strconv.FormatInt(resource.PhotosSQLiteResourcePrimaryKey, 10))
	if _, err := c.stmts.resource.ExecContext(
		ctx,
		resourceID,
		assetID,
		resource.PhotosSQLiteResourcePrimaryKey,
		resource.PhotosSQLiteResourceType,
		resource.PhotosSQLiteCompactUTI,
		resource.PhotosSQLiteResourceVersion,
		resource.PhotosSQLiteLocalAvailability,
		resource.PhotosSQLiteRemoteAvailability,
		resource.PhotosSQLiteStableHash,
		resource.PhotosSQLiteFingerprint,
		resource.ResourceTypeProjection,
		resource.UniformTypeIdentifierProjection,
		resource.AvailabilityProjection,
		resource.OriginalFilename,
		resource.LocalPath,
		resource.FileSize,
		boolInt(resource.AvailableLocally),
		boolInt(resource.NeedsDownload),
	); err != nil {
		return fmt.Errorf("insert asset resource: %w", err)
	}
	return nil
}

func (c *updateImporter) insertAlbum(ctx context.Context, assetID string, album photos.AlbumMembership) error {
	membershipID := stableID("album_membership", assetID, album.AlbumID)
	if _, err := c.stmts.album.ExecContext(ctx, membershipID, assetID, album.AlbumID, album.AlbumTitle, album.AlbumKind); err != nil {
		return fmt.Errorf("insert album membership: %w", err)
	}
	return nil
}

func (c *updateImporter) insertLocation(ctx context.Context, assetID, localIdentifier string, location photos.Location) error {
	locationID := stableID("location_observation", assetID, localIdentifier)
	if _, err := c.stmts.location.ExecContext(ctx, locationID, assetID, location.Latitude, location.Longitude, nullableFloat(location.Altitude), nullableFloat(location.HorizontalAccuracy), c.snapshot.Provider, ""); err != nil {
		return fmt.Errorf("insert location observation: %w", err)
	}
	return nil
}

func (c *updateImporter) insertFTS(ctx context.Context, tx *sql.Tx, assetID string, asset photos.Asset) error {
	title := ""
	bodyParts := []string{asset.MediaType}
	for _, resource := range asset.Resources {
		if title == "" {
			title = resource.OriginalFilename
		}
		bodyParts = append(bodyParts, resource.OriginalFilename)
	}
	for _, album := range asset.Albums {
		bodyParts = append(bodyParts, album.AlbumTitle)
	}
	body := strings.Join(uniqueNonEmpty(bodyParts), " ")
	if _, err := c.stmts.fts.ExecContext(ctx, assetID, title, body); err != nil {
		return fmt.Errorf("insert asset fts: %w", err)
	}
	return nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (c *updateImporter) upsertSeenAsset(ctx context.Context, sourceID, assetID, snapshotID, fingerprint string) error {
	if _, err := c.stmts.seen.ExecContext(ctx, sourceID, assetID, snapshotID, snapshotID, fingerprint, c.completedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upsert seen asset: %w", err)
	}
	return nil
}

func resetAssetDerivedRows(ctx context.Context, tx *sql.Tx, assetID string) error {
	if _, err := tx.ExecContext(ctx, `delete from observation_fts where asset_id = ?`, assetID); err != nil {
		return err
	}
	tables := []string{
		"asset_resource", "album_membership", "location_observation",
		"asset_fts", "current_photo_card", "photo_model_generation_transmission_attempt", "photo_model_generation_operation", "photo_card_generation", "photo_text_verification", "photo_text_extraction", "failed_location_operation_history", "provider_location_transmission_attempt",
		"photo_update_asset_outcome", "current_photo_location_evidence", "current_photo_media_evidence",
	}
	for _, table := range tables {
		column := "asset_id"
		if table == "asset_fts" {
			column = "id"
		}
		query := "delete from " + store.QuoteIdent(table) + " where " + store.QuoteIdent(column) + " = ?"
		_, err := tx.ExecContext(ctx, query, assetID)
		if err != nil {
			return fmt.Errorf("clear %s for asset: %w", table, err)
		}
	}
	return nil
}
