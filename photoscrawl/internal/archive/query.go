package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

	ShortRefsRebuilt bool `json:"-"`
}

type SearchHit struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
	Stale   bool   `json:"stale,omitempty"`

	ID           string `json:"-"`
	ShortRef     string `json:"short_ref,omitempty"`
	HitType      string `json:"-"`
	MediaType    string `json:"-"`
	CreationDate string `json:"-"`
	Title        string `json:"-"`
	StaleSince   string `json:"-"`
	StaleReason  string `json:"-"`
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

const searchWherePlaceSQL = `coalesce((
  select value_text
  from place_observation
  where asset_id = asset.id
    and observation_type = 'known_place'
    and superseded_at is null
    and trim(value_text) <> ''
  order by id
  limit 1
), (
  select value_text
  from place_observation
  where asset_id = asset.id
    and observation_type = 'venue'
    and superseded_at is null
    and tier in ('confirmed_venue', 'venue_candidate')
    and trim(value_text) <> ''
  order by case tier when 'confirmed_venue' then 1 else 2 end, distance_meters, id
  limit 1
), (
  select value_text
  from place_observation
  where asset_id = asset.id
    and observation_type = 'address'
    and superseded_at is null
    and trim(value_text) <> ''
  order by id
  limit 1
), (
  select 'GPS ' || printf('%.4f', latitude) || ', ' || printf('%.4f', longitude) ||
         case when horizontal_accuracy is not null then ' +/-' || printf('%.0f', horizontal_accuracy) || 'm' else '' end
  from location_observation
  where asset_id = asset.id
  order by id
  limit 1
), '')`

const searchWhereGPSOnlySQL = `coalesce((
  select 'GPS ' || printf('%.4f', latitude) || ', ' || printf('%.4f', longitude) ||
         case when horizontal_accuracy is not null then ' +/-' || printf('%.0f', horizontal_accuracy) || 'm' else '' end
  from location_observation
  where asset_id = asset.id
  order by id
  limit 1
), '')`

const searchCardSummarySQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and observation_type = '` + modelObservationCardSummary + `'
    and superseded_at is null
    and trim(value_text) <> ''
  order by id
  limit 1
), '')`

const searchCardDescriptionSQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and observation_type = '` + modelObservationCardDescription + `'
    and superseded_at is null
    and trim(value_text) <> ''
  order by id
  limit 1
), '')`

const searchStaleSinceSQL = `coalesce((
  select stale_since
  from (
    select stale_since, stale_reason
    from model_observation
    where asset_id = asset.id
      and superseded_at is null
      and trim(coalesce(stale_since, '')) <> ''
    union all
    select stale_since, stale_reason
    from place_observation
    where asset_id = asset.id
      and superseded_at is null
      and trim(coalesce(stale_since, '')) <> ''
  )
  order by stale_since
  limit 1
), '')`

const searchStaleReasonSQL = `coalesce((
  select coalesce(stale_reason, '')
  from (
    select stale_since, stale_reason
    from model_observation
    where asset_id = asset.id
      and superseded_at is null
      and trim(coalesce(stale_since, '')) <> ''
    union all
    select stale_since, stale_reason
    from place_observation
    where asset_id = asset.id
      and superseded_at is null
      and trim(coalesce(stale_since, '')) <> ''
  )
  order by stale_since
  limit 1
), '')`

func Search(ctx context.Context, paths Paths, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, errors.New("query is required")
	}
	// A positive limit is honored exactly with no hidden cap; limit 0 returns
	// every match for internal callers.
	limit := opts.Limit
	if limit < 0 {
		limit = 0
	}
	sqlLimit := limit
	if sqlLimit == 0 {
		sqlLimit = -1 // SQLite: a negative LIMIT is unbounded.
	}
	after, err := searchTimeBound(opts.After)
	if err != nil {
		return SearchResult{}, fmt.Errorf("after must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	before, err := searchTimeBound(opts.Before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("before must be a date (2006-01-02) or RFC 3339 timestamp: %w", err)
	}
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return SearchResult{}, err
	}
	defer func() { _ = db.Close() }()
	whereSQL := searchWhereGPSOnlySQL
	if ok, err := tableExists(ctx, db.DB(), "place_observation"); err == nil && ok {
		whereSQL = searchWherePlaceSQL
	}

	fts := ftsQuery(query)
	totalMatches, err := ftsDistinctAssetCount(ctx, db.DB(), fts, after, before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count search matches: %w", err)
	}
	shortRefsReady, err := shortRefsCurrent(ctx, db.DB())
	if err != nil {
		return SearchResult{}, err
	}
	rows, err := db.DB().QueryContext(ctx, `
with asset_matches as (
  select asset.id, asset_fts.rank as hit_rank
  from asset_fts
  join asset on asset.id = asset_fts.id
  where asset_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
observation_matches as (
  select asset.id, observation_fts.rank as hit_rank
  from observation_fts
  join asset on asset.id = observation_fts.asset_id
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
matched_assets as (
  select id, min(hit_rank) as hit_rank
  from (
    select id, hit_rank from asset_matches
    union all
    select id, hit_rank from observation_matches
  )
  group by id
)
select asset.id, asset.media_type, asset.creation_date, asset.timezone_name,
       coalesce((select original_filename from asset_resource where asset_id = asset.id order by id limit 1), '') as title,
       coalesce((
         select group_concat(part, ' ')
         from (
           select original_filename as part from asset_resource where asset_id = asset.id
           union
           select album_title from album_membership where asset_id = asset.id
         )
       ), '') as asset_body,
       `+searchWhoSQL+` as who,
       `+whereSQL+` as where_label,
       `+searchCardSummarySQL+` as card_summary,
       `+searchCardDescriptionSQL+` as card_description,
       `+searchStaleSinceSQL+` as stale_since,
       `+searchStaleReasonSQL+` as stale_reason
from matched_assets
join asset on asset.id = matched_assets.id
order by matched_assets.hit_rank, asset.creation_date desc, asset.id
limit ?
`, fts, after, after, before, before, fts, after, after, before, before, sqlLimit)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search assets: %w", err)
	}

	result := SearchResult{
		Query:        query,
		Limit:        limit,
		Results:      []SearchHit{},
		TotalMatches: totalMatches,
		Truncated:    limit > 0 && totalMatches > limit,
	}
	for rows.Next() {
		var hit SearchHit
		var assetBody, cardSummary, cardDescription, timezoneName string
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &timezoneName, &hit.Title, &assetBody, &hit.Who, &hit.Where, &cardSummary, &cardDescription, &hit.StaleSince, &hit.StaleReason); err != nil {
			return SearchResult{}, err
		}
		hit.HitType = "asset"
		hit.Ref = assetRef(hit.ID)
		hit.Time = localCaptureTime(hit.CreationDate, timezoneName)
		if !strings.HasPrefix(hit.Where, "GPS ") {
			hit.Where = cleanPlacePhrase(hit.Where)
		}
		hit.Snippet = searchSnippet(query, cardSummary, cardDescription, hit.Title, assetBody)
		hit.Stale = strings.TrimSpace(hit.StaleSince) != ""
		result.Results = append(result.Results, hit)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SearchResult{}, err
	}
	if err := rows.Close(); err != nil {
		return SearchResult{}, err
	}
	if shortRefsReady {
		for i := range result.Results {
			if alias, err := shortRefForFullRef(ctx, db.DB(), result.Results[i].Ref); err == nil {
				result.Results[i].ShortRef = alias
			}
		}
	}
	return result, nil
}

func ftsDistinctAssetCount(ctx context.Context, db *sql.DB, fts, after, before string) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, `
with asset_matches as (
  select asset.id
  from asset_fts
  join asset on asset.id = asset_fts.id
  where asset_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
observation_matches as (
  select asset.id
  from observation_fts
  join asset on asset.id = observation_fts.asset_id
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
)
select count(*)
from (
  select id from asset_matches
  union
  select id from observation_matches
)
`, fts, after, after, before, before, fts, after, after, before, before).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
