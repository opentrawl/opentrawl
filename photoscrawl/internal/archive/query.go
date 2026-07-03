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

	ID           string `json:"-"`
	HitType      string `json:"-"`
	MediaType    string `json:"-"`
	CreationDate string `json:"-"`
	Title        string `json:"-"`
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
    and trim(value_text) <> ''
  order by id
  limit 1
), (
  select value_text
  from place_observation
  where asset_id = asset.id
    and observation_type = 'venue'
    and tier in ('confirmed_venue', 'venue_candidate')
    and trim(value_text) <> ''
  order by case tier when 'confirmed_venue' then 1 else 2 end, distance_meters, id
  limit 1
), (
  select value_text
  from place_observation
  where asset_id = asset.id
    and observation_type = 'address'
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
    and trim(value_text) <> ''
  order by id
  limit 1
), '')`

const searchCardDescriptionSQL = `coalesce((
  select value_text
  from model_observation
  where asset_id = asset.id
    and observation_type = '` + modelObservationCardDescription + `'
    and trim(value_text) <> ''
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
	whereSQL := searchWhereGPSOnlySQL
	if ok, err := tableExists(ctx, db.DB(), "place_observation"); err == nil && ok {
		whereSQL = searchWherePlaceSQL
	}

	fts := ftsQuery(query)
	totalMatches, err := ftsDistinctAssetCount(ctx, db.DB(), fts, after, before)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count search matches: %w", err)
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
select asset.id, asset.media_type, asset.creation_date,
       coalesce((select title from asset_fts where id = asset.id limit 1), '') as title,
       coalesce((select body from asset_fts where id = asset.id limit 1), '') as asset_body,
       `+searchWhoSQL+` as who,
       `+whereSQL+` as where_label,
       `+searchCardSummarySQL+` as card_summary,
       `+searchCardDescriptionSQL+` as card_description
from matched_assets
join asset on asset.id = matched_assets.id
order by matched_assets.hit_rank, asset.creation_date desc, asset.id
limit ?
`, fts, after, after, before, before, fts, after, after, before, before, limit)
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
		var assetBody, cardSummary, cardDescription string
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &hit.Title, &assetBody, &hit.Who, &hit.Where, &cardSummary, &cardDescription); err != nil {
			return SearchResult{}, err
		}
		hit.HitType = "asset"
		hit.Ref = assetRef(hit.ID)
		hit.Time = localRFC3339(hit.CreationDate)
		if !strings.HasPrefix(hit.Where, "GPS ") {
			hit.Where = cleanPlacePhrase(hit.Where)
		}
		hit.Snippet = searchSnippet(query, cardSummary, cardDescription, hit.Title, assetBody)
		result.Results = append(result.Results, hit)
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, err
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
