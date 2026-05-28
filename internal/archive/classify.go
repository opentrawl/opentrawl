package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
)

const (
	metadataClassifierSource       = "archive_metadata"
	metadataClassifierModelID      = "photoscrawl.archive-metadata.v1"
	metadataClassifierInputVersion = "asset-resource-album.v1"
)

type ClassifyOptions struct {
	All   bool
	Limit int
	Now   func() time.Time
}

type ClassifyResult struct {
	Database                  string `json:"database"`
	Classifier                string `json:"classifier"`
	ModelID                   string `json:"model_id"`
	InputVersion              string `json:"input_version"`
	Limit                     int    `json:"limit"`
	Processed                 int    `json:"processed"`
	MetadataClassified        int    `json:"metadata_classified"`
	WaitingForLocalContent    int    `json:"waiting_for_local_content"`
	VisualObservationsWritten int    `json:"visual_observations_written"`
}

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
	ResourceType     string
	UTI              string
	OriginalFilename string
	AvailableLocally bool
	NeedsDownload    bool
}

type classifyAlbum struct {
	AlbumTitle string
	AlbumKind  string
}

type visualObservation struct {
	ObservationType string
	Label           string
	Confidence      float64
}

func Classify(ctx context.Context, paths Paths, opts ClassifyOptions) (ClassifyResult, error) {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	limit := opts.Limit
	if opts.All {
		limit = 0
	} else if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return ClassifyResult{}, err
	}
	defer db.Close()

	result := ClassifyResult{
		Database:     paths.Database,
		Classifier:   metadataClassifierSource,
		ModelID:      metadataClassifierModelID,
		InputVersion: metadataClassifierInputVersion,
		Limit:        limit,
	}
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, limit)
		if err != nil {
			return err
		}
		for _, input := range inputs {
			observations := classifyFromMetadata(input)
			written, err := writeMetadataClassification(ctx, tx, input, observations, now().UTC())
			if err != nil {
				return err
			}
			result.Processed++
			result.MetadataClassified++
			result.VisualObservationsWritten += written
			if input.NeedsDownload || !input.hasLocalContent() {
				result.WaitingForLocalContent++
			}
		}
		return nil
	})
	if err != nil {
		return ClassifyResult{}, err
	}
	return result, nil
}

func loadClassifyInputs(ctx context.Context, tx *sql.Tx, limit int) ([]classifyInput, error) {
	query := `
select q.id, q.asset_id, q.source_library_id, q.needs_download,
       a.media_type, a.media_subtypes, a.creation_date, a.width, a.height,
       a.favorite, a.hidden, a.burst_identifier, a.metadata_json
from classification_queue q
join asset a on a.id = q.asset_id
where q.state = 'pending'
order by a.creation_date, q.id
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

func loadClassifyResources(ctx context.Context, tx *sql.Tx, assetID string) ([]classifyResource, error) {
	rows, err := tx.QueryContext(ctx, `
select resource_type, uti, original_filename, available_locally, needs_download
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
		if err := rows.Scan(&resource.ResourceType, &resource.UTI, &resource.OriginalFilename, &availableLocally, &needsDownload); err != nil {
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

func classifyFromMetadata(input classifyInput) []visualObservation {
	out := []visualObservation{}
	add := func(kind, label string, confidence float64) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		out = append(out, visualObservation{ObservationType: kind, Label: label, Confidence: confidence})
	}

	add("media_type", input.MediaType, 1)
	add("content_availability", pickLabel(input.hasLocalContent(), "local_content_available", "local_content_unavailable"), 1)
	if input.Width > 0 && input.Height > 0 {
		switch {
		case input.Width > input.Height:
			add("geometry", "landscape", 1)
		case input.Height > input.Width:
			add("geometry", "portrait", 1)
		default:
			add("geometry", "square", 1)
		}
	}
	if strings.TrimSpace(input.BurstIdentifier) != "" {
		add("capture_mode", "burst_member", 1)
	}
	for _, resource := range input.Resources {
		add("resource_type", resource.ResourceType, 1)
		add("resource_uti", resource.UTI, 1)
	}

	keywords := input.keywordText()
	if strings.Contains(keywords, "screenshot") || strings.Contains(keywords, "screen shot") {
		add("document_signal", "screenshot_candidate", 0.75)
	}
	if containsAny(keywords, "receipt", "invoice", "bill", "statement") {
		add("document_signal", "receipt_candidate", 0.65)
	}
	if containsAny(keywords, "document", "passport", "ticket", "boarding pass", "menu") {
		add("document_signal", "document_candidate", 0.6)
	}
	return dedupeVisualObservations(out)
}

func writeMetadataClassification(ctx context.Context, tx *sql.Tx, input classifyInput, observations []visualObservation, classifiedAt time.Time) (int, error) {
	if err := clearMetadataObservations(ctx, tx, input.AssetID); err != nil {
		return 0, err
	}
	evidenceID := stableID("evidence", input.AssetID, "classification_input", metadataClassifierSource, metadataClassifierInputVersion)
	evidenceJSON, err := jsonText(map[string]any{
		"classifier":         metadataClassifierSource,
		"model_id":           metadataClassifierModelID,
		"input_version":      metadataClassifierInputVersion,
		"media_type":         input.MediaType,
		"media_subtypes":     input.MediaSubtypes,
		"creation_date":      input.CreationDate,
		"favorite":           input.Favorite,
		"hidden":             input.Hidden,
		"resource_count":     len(input.Resources),
		"album_count":        len(input.Albums),
		"width":              input.Width,
		"height":             input.Height,
		"has_local_content":  input.hasLocalContent(),
		"needs_download":     input.NeedsDownload,
		"classified_at":      classifiedAt.Format(time.RFC3339Nano),
		"metadata_only":      true,
		"content_not_opened": true,
	})
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into evidence_ref(id, asset_id, evidence_kind, source, pointer, value_json)
values (?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  asset_id = excluded.asset_id,
  evidence_kind = excluded.evidence_kind,
  source = excluded.source,
  pointer = excluded.pointer,
  value_json = excluded.value_json
`, evidenceID, input.AssetID, "classification_input", metadataClassifierSource, input.AssetID+"/classification/archive_metadata", evidenceJSON); err != nil {
		return 0, fmt.Errorf("write classification evidence: %w", err)
	}

	written := 0
	for _, observation := range observations {
		observationID := stableID("visual_observation", input.AssetID, metadataClassifierSource, observation.ObservationType, observation.Label)
		if _, err := tx.ExecContext(ctx, `
insert into visual_observation(id, asset_id, observation_type, label, confidence, bounding_box_json, source, model_id, evidence_id)
values (?, ?, ?, ?, ?, '{}', ?, ?, ?)
`, observationID, input.AssetID, observation.ObservationType, observation.Label, observation.Confidence, metadataClassifierSource, metadataClassifierModelID, evidenceID); err != nil {
			return written, fmt.Errorf("write visual observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, input.AssetID, observation.Label, strings.Join(nonEmpty(observation.ObservationType, observation.Label, metadataClassifierSource), " ")); err != nil {
			return written, fmt.Errorf("write observation fts: %w", err)
		}
		written++
	}

	state := "metadata_classified"
	reason := "local_metadata_observations"
	if input.NeedsDownload || !input.hasLocalContent() {
		reason = "local_metadata_observations_waiting_for_content"
	}
	if _, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = ?, updated_at = ?
where id = ?
`, state, reason, classifiedAt.Format(time.RFC3339Nano), input.QueueID); err != nil {
		return written, fmt.Errorf("update classification queue: %w", err)
	}
	return written, nil
}

func clearMetadataObservations(ctx context.Context, tx *sql.Tx, assetID string) error {
	if strings.TrimSpace(assetID) == "" {
		return errors.New("asset id is required")
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (
    select id from visual_observation
    where asset_id = ? and source = ? and model_id = ?
  )
`, assetID, assetID, metadataClassifierSource, metadataClassifierModelID); err != nil {
		return fmt.Errorf("clear metadata observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
delete from visual_observation
where asset_id = ? and source = ? and model_id = ?
`, assetID, metadataClassifierSource, metadataClassifierModelID); err != nil {
		return fmt.Errorf("clear metadata visual observations: %w", err)
	}
	return nil
}

func (input classifyInput) hasLocalContent() bool {
	for _, resource := range input.Resources {
		if resource.AvailableLocally {
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

func dedupeVisualObservations(observations []visualObservation) []visualObservation {
	seen := map[string]bool{}
	out := make([]visualObservation, 0, len(observations))
	for _, observation := range observations {
		key := observation.ObservationType + "\x00" + observation.Label
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, observation)
	}
	return out
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func pickLabel(condition bool, ifTrue, ifFalse string) string {
	if condition {
		return ifTrue
	}
	return ifFalse
}
