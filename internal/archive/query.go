package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/store"
)

type SearchOptions struct {
	Query string
	Limit int
}

type SearchResult struct {
	Query   string      `json:"query"`
	Limit   int         `json:"limit"`
	Results []SearchHit `json:"results"`
}

type SearchHit struct {
	ID           string `json:"id"`
	MediaType    string `json:"media_type"`
	CreationDate string `json:"creation_date"`
	Title        string `json:"title"`
	Snippet      string `json:"snippet"`
}

type OpenResult struct {
	Asset     map[string]any   `json:"asset"`
	Resources []map[string]any `json:"resources"`
	Albums    []map[string]any `json:"albums"`
	Locations []map[string]any `json:"locations"`
	Evidence  []map[string]any `json:"evidence"`
}

type EvidenceResult struct {
	RowID    string           `json:"row_id"`
	Evidence []map[string]any `json:"evidence"`
}

func Search(ctx context.Context, paths Paths, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, errors.New("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return SearchResult{}, err
	}
	defer db.Close()

	fts := ftsQuery(query)
	rows, err := db.DB().QueryContext(ctx, `
select asset.id, asset.media_type, asset.creation_date, asset_fts.title,
       snippet(asset_fts, 2, '[', ']', ' ... ', 12) as snippet
from asset_fts
join asset on asset.id = asset_fts.id
where asset_fts match ?
order by rank
limit ?
`, fts, limit)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search assets: %w", err)
	}
	defer rows.Close()

	result := SearchResult{Query: query, Limit: limit, Results: []SearchHit{}}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &hit.Title, &hit.Snippet); err != nil {
			return SearchResult{}, err
		}
		result.Results = append(result.Results, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, err
	}
	return result, nil
}

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	rowID = strings.TrimSpace(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("id is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer db.Close()

	asset, err := oneRow(ctx, db.DB(), `
select id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date,
       timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier,
       represents_burst, source_library_id, metadata_json
from asset
where id = ?
`, rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenResult{}, fmt.Errorf("asset not found: %s", rowID)
	}
	if err != nil {
		return OpenResult{}, err
	}
	resources, err := rows(ctx, db.DB(), `
select id, resource_type, uti, original_filename, local_path, file_size, sha256, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type, original_filename
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	albums, err := rows(ctx, db.DB(), `
select id, album_id, album_title, album_kind
from album_membership
where asset_id = ?
order by album_title, album_id
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	locations, err := rows(ctx, db.DB(), `
select id, latitude, longitude, altitude, horizontal_accuracy, source, evidence_id
from location_observation
where asset_id = ?
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	evidence, err := evidenceRows(ctx, db.DB(), rowID)
	if err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Asset: asset, Resources: resources, Albums: albums, Locations: locations, Evidence: evidence}, nil
}

func Evidence(ctx context.Context, paths Paths, rowID string) (EvidenceResult, error) {
	rowID = strings.TrimSpace(rowID)
	if rowID == "" {
		return EvidenceResult{}, errors.New("row id is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return EvidenceResult{}, err
	}
	defer db.Close()
	evidence, err := evidenceRows(ctx, db.DB(), rowID)
	if err != nil {
		return EvidenceResult{}, err
	}
	return EvidenceResult{RowID: rowID, Evidence: evidence}, nil
}

func evidenceRows(ctx context.Context, db *sql.DB, rowID string) ([]map[string]any, error) {
	return rows(ctx, db, `
select id, asset_id, evidence_kind, source, pointer, value_json
from evidence_ref
where asset_id = ? or id = ?
order by evidence_kind, id
`, rowID, rowID)
}

func oneRow(ctx context.Context, db *sql.DB, query string, args ...any) (map[string]any, error) {
	result, err := rows(ctx, db, query, args...)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, sql.ErrNoRows
	}
	return result[0], nil
}

func rows(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = normalizeSQLValue(values[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func normalizeSQLValue(value any) any {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	default:
		return typed
	}
}

func ftsQuery(query string) string {
	terms := strings.Fields(query)
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		term = strings.ReplaceAll(term, `"`, `""`)
		quoted = append(quoted, `"`+term+`"`)
	}
	if len(quoted) == 0 {
		return `""`
	}
	return strings.Join(quoted, " AND ")
}
