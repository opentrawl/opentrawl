package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

func (c *syncImporter) insertResource(ctx context.Context, assetID string, index int, resource photos.Resource) error {
	resourceID := stableID("asset_resource", assetID, fmt.Sprintf("%06d", index), resource.Type, resource.UTI, resource.OriginalFilename)
	if _, err := c.stmts.resource.ExecContext(ctx, resourceID, assetID, resource.Type, resource.UTI, resource.OriginalFilename, resource.LocalPath, resource.FileSize, resource.StableHash, boolInt(resource.AvailableLocally), boolInt(resource.NeedsDownload)); err != nil {
		return fmt.Errorf("insert asset resource: %w", err)
	}
	return nil
}

func (c *syncImporter) insertAlbum(ctx context.Context, assetID string, album photos.AlbumMembership) error {
	membershipID := stableID("album_membership", assetID, album.AlbumID)
	if _, err := c.stmts.album.ExecContext(ctx, membershipID, assetID, album.AlbumID, album.AlbumTitle, album.AlbumKind); err != nil {
		return fmt.Errorf("insert album membership: %w", err)
	}
	return nil
}

func (c *syncImporter) insertLocation(ctx context.Context, assetID, localIdentifier string, location photos.Location) error {
	locationID := stableID("location_observation", assetID, localIdentifier)
	if _, err := c.stmts.location.ExecContext(ctx, locationID, assetID, location.Latitude, location.Longitude, nullableFloat(location.Altitude), nullableFloat(location.HorizontalAccuracy), c.snapshot.Provider, ""); err != nil {
		return fmt.Errorf("insert location observation: %w", err)
	}
	return nil
}

func (c *syncImporter) insertFTS(ctx context.Context, tx *sql.Tx, assetID string, asset photos.Asset) error {
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

func (c *syncImporter) upsertSeenAsset(ctx context.Context, sourceID, assetID, snapshotID, fingerprint string) error {
	if _, err := c.stmts.seen.ExecContext(ctx, sourceID, assetID, snapshotID, snapshotID, fingerprint, c.completedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upsert seen asset: %w", err)
	}
	return nil
}

func (c *syncImporter) upsertClassifyQueue(ctx context.Context, tx *sql.Tx, sourceID, assetID string, asset photos.Asset) error {
	hasLocalContent := false
	needsDownload := false
	for _, resource := range asset.Resources {
		if resource.AvailableLocally || strings.TrimSpace(resource.LocalPath) != "" {
			hasLocalContent = true
		}
		if resource.NeedsDownload {
			needsDownload = true
		}
	}
	needsDownload = needsDownload && !hasLocalContent
	queueID := stableID("classification_queue", assetID)
	if _, err := c.stmts.queue.ExecContext(ctx, queueID, assetID, sourceID, "pending", "metadata_ingested", boolInt(needsDownload), c.completedAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("upsert classification queue: %w", err)
	}
	c.result.QueuedForClassify++
	if needsDownload {
		c.result.QueuedNeedsDownload++
	}
	return nil
}

func resetAssetDerivedRows(ctx context.Context, tx *sql.Tx, assetID string) error {
	tables := []string{
		"asset_resource", "album_membership", "location_observation",
		"metadata_observation", "text_observation", "face_observation",
		"model_observation", "place_observation",
		"asset_fts", "observation_fts", "edge",
	}
	for _, table := range tables {
		column := "asset_id"
		if table == "asset_fts" {
			column = "id"
		}
		query := "delete from " + store.QuoteIdent(table) + " where " + store.QuoteIdent(column) + " = ?"
		if table == "edge" {
			query = "delete from edge where from_id = ? or to_id = ?"
		}
		var err error
		if table == "edge" {
			_, err = tx.ExecContext(ctx, query, assetID, assetID)
		} else {
			_, err = tx.ExecContext(ctx, query, assetID)
		}
		if err != nil {
			return fmt.Errorf("clear %s for asset: %w", table, err)
		}
	}
	return nil
}
