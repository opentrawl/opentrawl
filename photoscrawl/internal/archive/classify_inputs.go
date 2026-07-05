package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/photoscrawl/internal/place"
)

type classifyInput struct {
	QueueID         string
	AssetID         string
	SourceLibraryID string
	LocalIdentifier string
	NeedsDownload   bool
	MediaType       string
	MediaSubtypes   string
	CreationDate    string
	TimezoneName    string
	Width           int64
	Height          int64
	DurationSeconds float64
	Favorite        bool
	Hidden          bool
	BurstIdentifier string
	MetadataJSON    string
	CameraMake      string
	CameraModel     string
	LensModel       string
	FocalLengthMM   float64
	FocalLength35MM float64
	Aperture        float64
	ShutterSpeed    float64
	ISO             int64
	HasLocation     bool
	Latitude        float64
	Longitude       float64
	AccuracyMeters  float64
	Place           *classifyPlaceContext
	KnownPlace      *KnownPlaceMatch
	Resources       []classifyResource
	Albums          []classifyAlbum
}

type classifyPlaceContext struct {
	Result      place.Result
	CacheStatus string
}

type classifyResource struct {
	ID               string
	ResourceType     string
	UTI              string
	OriginalFilename string
	LocalPath        string
	FileSize         int64
	AvailableLocally bool
	NeedsDownload    bool
}

type classifyAlbum struct {
	AlbumTitle string
	AlbumKind  string
}

func loadClassifyInputs(ctx context.Context, tx *sql.Tx, limit int, refreshModelID string) ([]classifyInput, error) {
	refreshModelID = strings.TrimSpace(refreshModelID)
	// Two index-friendly branches instead of one OR: the OR forced a full
	// queue scan per batch, and the old priority_text ranking computed
	// group_concats over resources and albums for every queued row before
	// the limit — minutes of CPU per batch on a full library.
	selectColumns := `
select q.id as queue_id, q.asset_id, q.source_library_id, a.local_identifier, q.needs_download,
       a.media_type, a.media_subtypes, a.creation_date, a.timezone_name, a.width, a.height, a.duration_seconds,
       a.favorite, a.hidden, a.burst_identifier, a.metadata_json,
       a.camera_make, a.camera_model, a.lens_model,
       coalesce(a.focal_length_mm, 0) as focal_length_mm,
       coalesce(a.focal_length_35mm, 0) as focal_length_35mm,
       coalesce(a.aperture, 0) as aperture,
       coalesce(a.shutter_speed, 0) as shutter_speed,
       coalesce(a.iso, 0) as iso,
       exists(select 1 from location_observation lo where lo.asset_id = a.id) as has_location
from classification_queue q
join asset a on a.id = q.asset_id
`
	query := selectColumns + `where q.state in (` + classifyQueueStates(refreshModelID != "") + `)`
	args := []any{}
	if refreshModelID != "" {
		query += `
union all
` + selectColumns + `where q.state = '` + classifyQueueStateContentClassified + `' and not exists (
  select 1
  from model_observation mo indexed by model_observation_asset_idx
  where mo.asset_id = a.id
    and mo.source = ?
    and mo.model_id = ?
    and mo.prompt_version = ?
    and mo.observation_type = ?
)`
		args = append(args, modelClassifierSource, refreshModelID, modelPromptVersion, modelObservationCardSummary)
	}
	query += `
order by creation_date desc, has_location desc, queue_id
`
	if limit > 0 {
		query += "limit ?"
		args = append(args, limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load classification queue: %w", err)
	}
	defer func() { _ = rows.Close() }()

	inputs := []classifyInput{}
	for rows.Next() {
		var input classifyInput
		var needsDownload, favorite, hidden, hasLocation int
		if err := rows.Scan(
			&input.QueueID,
			&input.AssetID,
			&input.SourceLibraryID,
			&input.LocalIdentifier,
			&needsDownload,
			&input.MediaType,
			&input.MediaSubtypes,
			&input.CreationDate,
			&input.TimezoneName,
			&input.Width,
			&input.Height,
			&input.DurationSeconds,
			&favorite,
			&hidden,
			&input.BurstIdentifier,
			&input.MetadataJSON,
			&input.CameraMake,
			&input.CameraModel,
			&input.LensModel,
			&input.FocalLengthMM,
			&input.FocalLength35MM,
			&input.Aperture,
			&input.ShutterSpeed,
			&input.ISO,
			&hasLocation,
		); err != nil {
			return nil, err
		}
		input.NeedsDownload = needsDownload != 0
		input.Favorite = favorite != 0
		input.Hidden = hidden != 0
		input.HasLocation = hasLocation != 0
		if input.HasLocation {
			if err := loadClassifyLocation(ctx, tx, &input); err != nil {
				return nil, err
			}
		}
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
		return "'" + classifyQueueStatePending + "', '" + classifyQueueStateMetadataClassified + "'"
	}
	return "'" + classifyQueueStatePending + "'"
}

func loadClassifyResources(ctx context.Context, tx *sql.Tx, assetID string) ([]classifyResource, error) {
	rows, err := tx.QueryContext(ctx, `
select id, resource_type, uti, original_filename, local_path, file_size, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type, original_filename
`, assetID)
	if err != nil {
		return nil, fmt.Errorf("load classification resources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	resources := []classifyResource{}
	for rows.Next() {
		var resource classifyResource
		var availableLocally, needsDownload int
		if err := rows.Scan(&resource.ID, &resource.ResourceType, &resource.UTI, &resource.OriginalFilename, &resource.LocalPath, &resource.FileSize, &availableLocally, &needsDownload); err != nil {
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
	defer func() { _ = rows.Close() }()

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

func loadClassifyLocation(ctx context.Context, tx *sql.Tx, input *classifyInput) error {
	row := tx.QueryRowContext(ctx, `
select latitude, longitude, coalesce(horizontal_accuracy, 0)
from location_observation
where asset_id = ?
order by id
limit 1
`, input.AssetID)
	if err := row.Scan(&input.Latitude, &input.Longitude, &input.AccuracyMeters); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			input.HasLocation = false
			return nil
		}
		return err
	}
	return nil
}
