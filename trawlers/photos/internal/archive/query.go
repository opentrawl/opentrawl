package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type SearchOptions struct {
	Query         string
	Limit         int
	BoundedTotals bool
	After         string
	Before        string
}

type SearchResult struct {
	Query             string      `json:"query"`
	Limit             int         `json:"-"`
	Results           []SearchHit `json:"results"`
	TotalMatches      int         `json:"total_matches"`
	TotalIsLowerBound bool        `json:"total_is_lower_bound,omitempty"`
	Truncated         bool        `json:"truncated"`
}

type SearchHit struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
	Stale   bool   `json:"stale,omitempty"`

	ID           string        `json:"-"`
	ShortRef     string        `json:"short_ref,omitempty"`
	HitType      string        `json:"-"`
	MediaType    string        `json:"-"`
	CreationDate string        `json:"-"`
	Title        string        `json:"-"`
	StaleSince   string        `json:"-"`
	StaleReason  string        `json:"-"`
	AnchorID     string        `json:"-"`
	Matches      []SearchMatch `json:"-"`
}

type SearchMatch struct {
	Field string
	Runs  []SearchTextRun
}

type SearchTextRun struct {
	Text    string
	Matched bool
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
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return SearchResult{}, err
	}
	defer func() { _ = db.Close() }()
	return search(ctx, db, opts)
}

// SearchWithStore searches the runner-owned read-only Photos store.
func SearchWithStore(ctx context.Context, db *store.Store, opts SearchOptions) (SearchResult, error) {
	if err := validateReadStore(ctx, db); err != nil {
		return SearchResult{}, err
	}
	return search(ctx, db, opts)
}

func search(ctx context.Context, db *store.Store, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, errors.New("query is required")
	}
	if err := requireCurrentSearchIndex(ctx, db.DB()); err != nil {
		return SearchResult{}, err
	}
	// A positive limit is honored exactly with no hidden cap; limit 0 returns
	// every match for internal callers.
	limit := opts.Limit
	if limit < 0 {
		limit = 0
	}
	boundedTotals := opts.BoundedTotals && limit > 0
	sqlLimit := limit
	if boundedTotals {
		sqlLimit++
	} else if sqlLimit == 0 {
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
	whereSQL := searchWhereGPSOnlySQL
	observationPlaceJoinSQL := ""
	observationKindSQL := `case
  when album_membership.id is not null then 'album'
  when model_observation.observation_type = '` + modelObservationCardSummary + `' then 'summary'
  when model_observation.observation_type = '` + modelObservationCardDescription + `' then 'description'
  when model_observation.observation_type = '` + modelObservationCardOCR + `' then 'ocr'
  when model_observation.observation_type = '` + modelObservationCardVisibleText + `' then 'visible-text'
  when model_observation.observation_type = '` + modelObservationCardLocation + `' then 'model-location'
  when metadata_observation.id is not null then 'metadata'
  else ''
end`
	if ok, err := tableExists(ctx, db.DB(), "place_observation"); err == nil && ok {
		whereSQL = searchWherePlaceSQL
		observationPlaceJoinSQL = `left join place_observation on place_observation.id = observation_fts.id`
		observationKindSQL = `case
  when album_membership.id is not null then 'album'
  when model_observation.observation_type = '` + modelObservationCardSummary + `' then 'summary'
  when model_observation.observation_type = '` + modelObservationCardDescription + `' then 'description'
  when model_observation.observation_type = '` + modelObservationCardOCR + `' then 'ocr'
  when model_observation.observation_type = '` + modelObservationCardVisibleText + `' then 'visible-text'
  when model_observation.observation_type = '` + modelObservationCardLocation + `' then 'model-location'
  when place_observation.id is not null then case place_observation.observation_type
    when 'address' then 'address'
    when 'known_place' then 'known-place'
    when 'venue' then 'venue'
    else ''
  end
  when metadata_observation.id is not null then 'metadata'
  else ''
end`
	}

	fts := ftsQuery(query)
	totalMatches := 0
	if !boundedTotals {
		totalMatches, err = ftsDistinctAssetCount(ctx, db.DB(), fts, after, before, observationPlaceJoinSQL)
		if err != nil {
			return SearchResult{}, fmt.Errorf("count search matches: %w", err)
		}
	}
	rows, err := db.DB().QueryContext(ctx, `
with asset_snippets as (
  select asset.id, asset_fts.rank as hit_rank,
         snippet(asset_fts, 1, char(57344), char(57345), '…', 32) as title_match,
         snippet(asset_fts, 2, char(57344), char(57345), '…', 32) as body_match
  from asset_fts
  join asset on asset.id = asset_fts.id
  where asset_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
asset_matches as (
  select id, hit_rank,
         case
           when instr(title_match, char(57344)) > 0 then 'filename'
           when instr(body_match, char(57344)) > 0 then 'media'
           else ''
         end as match_kind,
         '' as match_id,
         case
           when instr(title_match, char(57344)) > 0 then title_match
           else body_match
         end as title_match,
         '' as body_match
  from asset_snippets
  where instr(title_match, char(57344)) > 0 or instr(body_match, char(57344)) > 0
),
observation_matches as (
  select asset.id, observation_fts.rank as hit_rank, `+observationKindSQL+` as match_kind,
         observation_fts.id as match_id,
         snippet(observation_fts, 2, char(57344), char(57345), '…', 32) as title_match,
         snippet(observation_fts, 3, char(57344), char(57345), '…', 32) as body_match
  from observation_fts
  join asset on asset.id = observation_fts.asset_id
  left join model_observation on model_observation.id = observation_fts.id
  left join metadata_observation on metadata_observation.id = observation_fts.id
  left join album_membership on album_membership.id = observation_fts.id
  `+observationPlaceJoinSQL+`
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
ranked_matches as (
  select id, hit_rank, match_kind, match_id, title_match, body_match,
         row_number() over (partition by id order by hit_rank, match_kind, match_id) as match_order
  from (
    select id, hit_rank, match_kind, match_id, title_match, body_match from asset_matches
    union all
    select id, hit_rank, match_kind, match_id, title_match, body_match from observation_matches
  )
),
matched_assets as (
  select id, hit_rank, match_kind, match_id, title_match, body_match
  from ranked_matches
  where match_order = 1
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
       coalesce((select group_concat(album_title, char(10)) from album_membership where asset_id = asset.id), '') as album_titles,
       `+searchWhoSQL+` as who,
       `+whereSQL+` as where_label,
       `+searchCardSummarySQL+` as card_summary,
       `+searchCardDescriptionSQL+` as card_description,
       `+searchStaleSinceSQL+` as stale_since,
       `+searchStaleReasonSQL+` as stale_reason,
       asset.source_state,
       matched_assets.match_kind, matched_assets.match_id,
       matched_assets.title_match, matched_assets.body_match
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
		Truncated:    !boundedTotals && limit > 0 && totalMatches > limit,
	}
	type pendingHit struct {
		hit         SearchHit
		albumTitles string
		sourceState string
		matchKind   string
		matchID     string
		titleMatch  string
		bodyMatch   string
	}
	pending := make([]pendingHit, 0)
	hasProbe := false
	for rows.Next() {
		var hit SearchHit
		var assetBody, albumTitles, cardSummary, cardDescription, timezoneName, sourceState string
		var matchKind, matchID, titleMatch, bodyMatch string
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &timezoneName, &hit.Title, &assetBody, &albumTitles, &hit.Who, &hit.Where, &cardSummary, &cardDescription, &hit.StaleSince, &hit.StaleReason, &sourceState, &matchKind, &matchID, &titleMatch, &bodyMatch); err != nil {
			return SearchResult{}, err
		}
		hit.HitType = "asset"
		hit.Ref = AssetRef(hit.ID)
		hit.Time = localCaptureTime(hit.CreationDate, timezoneName)
		if !strings.HasPrefix(hit.Where, "GPS ") {
			hit.Where = cleanPlacePhrase(hit.Where)
		}
		hit.Snippet = searchSnippet(query, cardSummary, cardDescription, hit.Title, assetBody)
		if sourceState == sourceStateDeletedUpstream {
			hit.Snippet = "Deleted upstream · " + hit.Snippet
		}
		hit.Stale = strings.TrimSpace(hit.StaleSince) != ""
		if boundedTotals && len(pending) == limit {
			hasProbe = true
		} else {
			pending = append(pending, pendingHit{
				hit: hit, albumTitles: albumTitles, sourceState: sourceState,
				matchKind: matchKind, matchID: matchID, titleMatch: titleMatch, bodyMatch: bodyMatch,
			})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SearchResult{}, err
	}
	if err := rows.Close(); err != nil {
		return SearchResult{}, err
	}
	for _, pendingHit := range pending {
		matchKind := pendingHit.matchKind
		if matchKind == "media" && markedSnippetMatchesAlbum(pendingHit.titleMatch, pendingHit.albumTitles) {
			matchKind = "album"
		}
		var err error
		matchKind, err = matchedAssetField(ctx, db.DB(), pendingHit.hit.ID, matchKind, pendingHit.titleMatch+pendingHit.bodyMatch)
		if err != nil {
			return SearchResult{}, err
		}
		pendingHit.hit.AnchorID, pendingHit.hit.Matches = photoSearchMatch(matchKind, pendingHit.matchID, pendingHit.titleMatch, pendingHit.bodyMatch)
		result.Results = append(result.Results, pendingHit.hit)
	}
	if boundedTotals {
		if hasProbe {
			result.TotalMatches = limit + 1
			result.TotalIsLowerBound = true
			result.Truncated = true
		} else {
			result.TotalMatches = len(result.Results)
		}
	}
	return result, nil
}

func requireCurrentSearchIndex(ctx context.Context, db *sql.DB) error {
	available, err := tableExists(ctx, db, "search_index_state")
	if err != nil {
		return fmt.Errorf("read photo search index state: %w", err)
	}
	version := 0
	if available {
		if err := db.QueryRowContext(ctx, `select coalesce(max(version), 0) from search_index_state`).Scan(&version); err != nil {
			return fmt.Errorf("read photo search index state: %w", err)
		}
	}
	if version < searchIndexVersion {
		var assets int
		if err := db.QueryRowContext(ctx, `select count(*) from asset`).Scan(&assets); err == nil && assets == 0 {
			return nil
		}
		return errors.New("photo search index is out of date; run 'trawl photos sync'")
	}
	return nil
}

func ftsDistinctAssetCount(ctx context.Context, db *sql.DB, fts, after, before, observationPlaceJoinSQL string) (int, error) {
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
  left join model_observation on model_observation.id = observation_fts.id
  left join metadata_observation on metadata_observation.id = observation_fts.id
  left join album_membership on album_membership.id = observation_fts.id
  `+observationPlaceJoinSQL+`
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
