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
	Runs  []store.FTS5TextRun
}

const searchWhoSQL = `''`

const searchWherePlaceSQL = `coalesce((
  select photographed_place_text
  from current_photo_card
  where asset_id = asset.id and trim(photographed_place_text) <> ''
), (
  select 'GPS ' || printf('%.4f', latitude) || ', ' || printf('%.4f', longitude) ||
         case when horizontal_accuracy is not null then ' +/-' || printf('%.0f', horizontal_accuracy) || 'm' else '' end
  from location_observation
  where asset_id = asset.id
  order by id
  limit 1
), '')`

const searchCardSummarySQL = `coalesce((
  select concise_description
  from current_photo_card
  where asset_id = asset.id and trim(concise_description) <> ''
), '')`

const searchCardDescriptionSQL = `coalesce((
  select detailed_description
  from current_photo_card
  where asset_id = asset.id and trim(detailed_description) <> ''
), '')`

const searchStaleSinceSQL = `''`

const searchStaleReasonSQL = `''`

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
	whereSQL := searchWherePlaceSQL
	observationPlaceJoinSQL := ``
	observationKindSQL := `'description'`

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
  `+observationPlaceJoinSQL+`
  where observation_fts match ?
    and (? = '' or asset.creation_date >= ?)
    and (? = '' or asset.creation_date <= ?)
),
matched_asset_ids as (
  select id from asset_matches
  union
  select id from observation_matches
),
matched_assets as (
  select matched_asset_ids.id,
         case
           when observation_matches.id is null or (asset_matches.id is not null and asset_matches.hit_rank <= observation_matches.hit_rank) then asset_matches.hit_rank
           else observation_matches.hit_rank
         end as hit_rank,
         case
           when observation_matches.id is null or (asset_matches.id is not null and asset_matches.hit_rank <= observation_matches.hit_rank) then asset_matches.match_kind
           else observation_matches.match_kind
         end as match_kind,
         case
           when observation_matches.id is null or (asset_matches.id is not null and asset_matches.hit_rank <= observation_matches.hit_rank) then asset_matches.match_id
           else observation_matches.match_id
         end as match_id,
         case
           when observation_matches.id is null or (asset_matches.id is not null and asset_matches.hit_rank <= observation_matches.hit_rank) then asset_matches.title_match
           else observation_matches.title_match
         end as title_match,
         case
           when observation_matches.id is null or (asset_matches.id is not null and asset_matches.hit_rank <= observation_matches.hit_rank) then asset_matches.body_match
           else observation_matches.body_match
         end as body_match
  from matched_asset_ids
  left join asset_matches on asset_matches.id = matched_asset_ids.id
  left join observation_matches on observation_matches.id = matched_asset_ids.id
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
       matched_assets.match_kind,
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
		titleMatch  string
		bodyMatch   string
	}
	pending := make([]pendingHit, 0)
	hasProbe := false
	for rows.Next() {
		var hit SearchHit
		var assetBody, albumTitles, cardSummary, cardDescription, timezoneName, sourceState string
		var matchKind, titleMatch, bodyMatch string
		if err := rows.Scan(&hit.ID, &hit.MediaType, &hit.CreationDate, &timezoneName, &hit.Title, &assetBody, &albumTitles, &hit.Who, &hit.Where, &cardSummary, &cardDescription, &hit.StaleSince, &hit.StaleReason, &sourceState, &matchKind, &titleMatch, &bodyMatch); err != nil {
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
				matchKind: matchKind, titleMatch: titleMatch, bodyMatch: bodyMatch,
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
		if matchKind == "description" && len(store.ParseFTS5MarkedText(pendingHit.titleMatch)) > 0 {
			matchKind = "summary"
		}
		var err error
		matchKind, err = matchedAssetField(ctx, db.DB(), pendingHit.hit.ID, matchKind, pendingHit.titleMatch+pendingHit.bodyMatch, pendingHit.hit.Where)
		if err != nil {
			return SearchResult{}, err
		}
		pendingHit.hit.AnchorID, pendingHit.hit.Matches = photoSearchMatch(matchKind, pendingHit.titleMatch, pendingHit.bodyMatch)
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
