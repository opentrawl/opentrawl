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

type SearchOptions struct {
	Query  string
	Limit  int
	After  string
	Before string
}

type SearchResult struct {
	Query        string      `json:"query"`
	Limit        int         `json:"-"`
	Results      []SearchHit `json:"results"`
	TotalMatches int         `json:"total_matches"`
	Truncated    bool        `json:"truncated"`
}

type SearchHit struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`

	ID            string `json:"-"`
	HitType       string `json:"-"`
	ObservationID string `json:"-"`
	MediaType     string `json:"-"`
	CreationDate  string `json:"-"`
	Title         string `json:"-"`
}

const searchWhoSQL = `coalesce((
  select group_concat(person_label, ', ')
  from (
    select distinct person_label
    from face_observation
    where asset_id = asset.id and trim(person_label) <> ''
    order by person_label
    limit 3
  )
), '')`

const searchWhereSQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and trim(value_text) <> ''
    and observation_type in ('merchant_or_venue_name_candidate', 'landmark_or_place_name_candidate', 'place_type_candidate')
  order by case observation_type
    when 'merchant_or_venue_name_candidate' then 1
    when 'landmark_or_place_name_candidate' then 2
    when 'place_type_candidate' then 3
    else 4
  end, confidence desc, value_text
  limit 1
), (
  select 'GPS ' || printf('%.4f', latitude) || ', ' || printf('%.4f', longitude) ||
         case when horizontal_accuracy is not null then ' +/-' || printf('%.0f', horizontal_accuracy) || 'm' else '' end
  from location_observation
  where asset_id = asset.id
  order by id
  limit 1
), '')`

func Search(ctx context.Context, paths Paths, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, errors.New("query is required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	after, err := searchTimeBound(opts.After)
	if err != nil {
		return SearchResult{}, fmt.Errorf("after must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	before, err := searchTimeBound(opts.Before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("before must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return SearchResult{}, err
	}
	defer db.Close()

	fts := ftsQuery(query)
	assetMatches, err := ftsCount(ctx, db.DB(), "asset_fts", fts, after, before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count asset matches: %w", err)
	}
	observationMatches, err := ftsCount(ctx, db.DB(), "observation_fts", fts, after, before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count observation matches: %w", err)
	}
	totalMatches := assetMatches + observationMatches
	rows, err := db.DB().QueryContext(ctx, `
select asset.id, asset.media_type, asset.creation_date, asset_fts.title,
       `+searchWhoSQL+` as who,
       `+searchWhereSQL+` as where_label,
       snippet(asset_fts, 2, '', '', ' ', 12) as snippet
from asset_fts
join asset on asset.id = asset_fts.id
where asset_fts match ?
  and (? = '' or asset.creation_date >= ?)
  and (? = '' or asset.creation_date <= ?)
order by rank
limit ?
`, fts, after, after, before, before, limit)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search assets: %w", err)
	}
	defer rows.Close()

	result := SearchResult{
		Query:        query,
		Limit:        limit,
		Results:      []SearchHit{},
		TotalMatches: totalMatches,
		Truncated:    totalMatches > limit,
	}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &hit.Title, &hit.Who, &hit.Where, &hit.Snippet); err != nil {
			return SearchResult{}, err
		}
		hit.HitType = "asset"
		hit.Ref = assetRef(hit.ID)
		hit.Time = localRFC3339(hit.CreationDate)
		hit.Snippet = cleanSnippet(hit.Snippet)
		result.Results = append(result.Results, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, err
	}

	if len(result.Results) < limit {
		observationLimit := limit - len(result.Results)
		observationRows, err := db.DB().QueryContext(ctx, `
select asset.id, observation_fts.id, asset.media_type, asset.creation_date, observation_fts.title,
       `+searchWhoSQL+` as who,
       `+searchWhereSQL+` as where_label,
       snippet(observation_fts, 3, '', '', ' ', 12) as snippet
from observation_fts
join asset on asset.id = observation_fts.asset_id
where observation_fts match ?
  and (? = '' or asset.creation_date >= ?)
  and (? = '' or asset.creation_date <= ?)
order by rank
limit ?
`, fts, after, after, before, before, observationLimit)
		if err != nil {
			return SearchResult{}, fmt.Errorf("search observations: %w", err)
		}
		defer observationRows.Close()
		for observationRows.Next() {
			var hit SearchHit
			if err := observationRows.Scan(&hit.ID, &hit.ObservationID, &hit.MediaType, &hit.CreationDate, &hit.Title, &hit.Who, &hit.Where, &hit.Snippet); err != nil {
				return SearchResult{}, err
			}
			hit.HitType = "observation"
			hit.Ref = assetRef(hit.ID)
			hit.Time = localRFC3339(hit.CreationDate)
			hit.Snippet = cleanSnippet(hit.Snippet)
			result.Results = append(result.Results, hit)
		}
		if err := observationRows.Err(); err != nil {
			return SearchResult{}, err
		}
	}
	return result, nil
}

func ftsCount(ctx context.Context, db *sql.DB, table, fts, after, before string) (int, error) {
	var count int
	var query string
	switch table {
	case "asset_fts":
		query = `select count(*) from asset_fts join asset on asset.id = asset_fts.id where asset_fts match ? and (? = '' or asset.creation_date >= ?) and (? = '' or asset.creation_date <= ?)`
	case "observation_fts":
		query = `select count(*) from observation_fts join asset on asset.id = observation_fts.asset_id where observation_fts match ? and (? = '' or asset.creation_date >= ?) and (? = '' or asset.creation_date <= ?)`
	default:
		return 0, fmt.Errorf("unknown FTS table %q", table)
	}
	if err := db.QueryRowContext(ctx, query, fts, after, after, before, before).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer db.Close()

	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, creation_date, width, height, duration_seconds, favorite, hidden
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
select resource_type, uti, original_filename, file_size, available_locally, needs_download
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
	visualObservations, err := rows(ctx, db.DB(), `
select observation_type, label, confidence, evidence_id
from visual_observation
where asset_id = ?
order by observation_type, confidence desc, label
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	textObservations, err := rows(ctx, db.DB(), `
select text, confidence, evidence_id
from text_observation
where asset_id = ?
order by confidence desc, id
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	faceObservations, err := rows(ctx, db.DB(), `
select person_label, confidence, evidence_id
from face_observation
where asset_id = ?
order by confidence desc, id
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	modelObservations, err := rows(ctx, db.DB(), `
select observation_type, value_text, confidence, evidence_id
from model_observation
where asset_id = ?
order by observation_type, confidence desc, value_text
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	evidence, err := evidenceRows(ctx, db.DB(), rowID)
	if err != nil {
		return OpenResult{}, err
	}
	return newOpenResult(asset, resources, albums, locations, visualObservations, textObservations, faceObservations, modelObservations, evidence), nil
}

func assetRef(id string) string {
	return photoscrawlRef(id)
}

func photoscrawlRef(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return "photoscrawl:" + strings.Replace(id, ":", "/", 1)
}

func normalizeRef(ref string) string {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "photoscrawl:")
	return strings.Replace(ref, "/", ":", 1)
}

func searchTimeBound(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("invalid time %q", value)
}

func localRFC3339(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.Local().Format(time.RFC3339)
}

func cleanSnippet(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func Evidence(ctx context.Context, paths Paths, rowID string) (EvidenceResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return EvidenceResult{}, errors.New("ref is required")
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
	return EvidenceResult{Ref: photoscrawlRef(rowID), Evidence: openEvidenceRefs(evidence)}, nil
}

func evidenceRows(ctx context.Context, db *sql.DB, rowID string) ([]map[string]any, error) {
	return rows(ctx, db, `
select id, asset_id, evidence_kind, source
from evidence_ref
where asset_id = ? or id = ? or id in (
  select evidence_id from location_observation where id = ?
  union
  select evidence_id from visual_observation where id = ?
  union
  select evidence_id from text_observation where id = ?
  union
  select evidence_id from face_observation where id = ?
  union
  select evidence_id from model_observation where id = ?
  union
  select evidence_id from edge where id = ?
)
order by evidence_kind, id
`, rowID, rowID, rowID, rowID, rowID, rowID, rowID, rowID)
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
