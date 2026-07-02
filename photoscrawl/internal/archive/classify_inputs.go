package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type classifyInput struct {
	QueueID         string
	AssetID         string
	SourceLibraryID string
	NeedsDownload   bool
	MediaType       string
	MediaSubtypes   string
	CreationDate    string
	Width           int64
	Height          int64
	Favorite        bool
	Hidden          bool
	BurstIdentifier string
	MetadataJSON    string
	Resources       []classifyResource
	Albums          []classifyAlbum
}

type classifyResource struct {
	ID               string
	ResourceType     string
	UTI              string
	OriginalFilename string
	LocalPath        string
	AvailableLocally bool
	NeedsDownload    bool
}

type classifyAlbum struct {
	AlbumTitle string
	AlbumKind  string
}

func loadClassifyInputs(ctx context.Context, tx *sql.Tx, limit int, includeMetadataClassified bool) ([]classifyInput, error) {
	query := `
select q.id, q.asset_id, q.source_library_id, q.needs_download,
       a.media_type, a.media_subtypes, a.creation_date, a.width, a.height,
       a.favorite, a.hidden, a.burst_identifier, a.metadata_json
from classification_queue q
join asset a on a.id = q.asset_id
where q.state in (` + classifyQueueStates(includeMetadataClassified) + `)
order by a.creation_date desc, q.id
`
	args := []any{}
	if limit > 0 {
		query += "limit ?"
		args = append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load classification queue: %w", err)
	}
	defer rows.Close()

	inputs := []classifyInput{}
	for rows.Next() {
		var input classifyInput
		var needsDownload, favorite, hidden int
		if err := rows.Scan(
			&input.QueueID,
			&input.AssetID,
			&input.SourceLibraryID,
			&needsDownload,
			&input.MediaType,
			&input.MediaSubtypes,
			&input.CreationDate,
			&input.Width,
			&input.Height,
			&favorite,
			&hidden,
			&input.BurstIdentifier,
			&input.MetadataJSON,
		); err != nil {
			return nil, err
		}
		input.NeedsDownload = needsDownload != 0
		input.Favorite = favorite != 0
		input.Hidden = hidden != 0
		input.Resources, err = loadClassifyResources(ctx, tx, input.AssetID)
		if err != nil {
			return nil, err
		}
		input.Albums, err = loadClassifyAlbums(ctx, tx, input.AssetID)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, rows.Err()
}

func classifyQueueStates(includeMetadataClassified bool) string {
	if includeMetadataClassified {
		return "'pending', 'metadata_classified'"
	}
	return "'pending'"
}

func loadClassifyResources(ctx context.Context, tx *sql.Tx, assetID string) ([]classifyResource, error) {
	rows, err := tx.QueryContext(ctx, `
select id, resource_type, uti, original_filename, local_path, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type, original_filename
`, assetID)
	if err != nil {
		return nil, fmt.Errorf("load classification resources: %w", err)
	}
	defer rows.Close()

	resources := []classifyResource{}
	for rows.Next() {
		var resource classifyResource
		var availableLocally, needsDownload int
		if err := rows.Scan(&resource.ID, &resource.ResourceType, &resource.UTI, &resource.OriginalFilename, &resource.LocalPath, &availableLocally, &needsDownload); err != nil {
			return nil, err
		}
		resource.AvailableLocally = availableLocally != 0
		resource.NeedsDownload = needsDownload != 0
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func loadClassifyAlbums(ctx context.Context, tx *sql.Tx, assetID string) ([]classifyAlbum, error) {
	rows, err := tx.QueryContext(ctx, `
select album_title, album_kind
from album_membership
where asset_id = ?
order by album_title, album_kind
`, assetID)
	if err != nil {
		return nil, fmt.Errorf("load classification albums: %w", err)
	}
	defer rows.Close()

	albums := []classifyAlbum{}
	for rows.Next() {
		var album classifyAlbum
		if err := rows.Scan(&album.AlbumTitle, &album.AlbumKind); err != nil {
			return nil, err
		}
		albums = append(albums, album)
	}
	return albums, rows.Err()
}

func (input classifyInput) hasLocalContent() bool {
	for _, resource := range input.Resources {
		if resource.AvailableLocally || strings.TrimSpace(resource.LocalPath) != "" {
			return true
		}
	}
	return false
}

func (input classifyInput) keywordText() string {
	parts := []string{input.MediaType, input.MediaSubtypes, input.MetadataJSON}
	for _, resource := range input.Resources {
		parts = append(parts, resource.ResourceType, resource.UTI, resource.OriginalFilename)
	}
	for _, album := range input.Albums {
		parts = append(parts, album.AlbumTitle, album.AlbumKind)
	}
	return strings.ToLower(strings.Join(parts, " "))
}
