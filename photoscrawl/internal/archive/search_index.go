package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
)

// Bump when the FTS build changes in a way that requires re-deriving the
// index from source tables. History:
//
//	1: porter tokenizer (pre-versioning archives read as 0 and rebuild)
//	2: card FTS bodies are raw card prose, not deduped term lists, so bm25
//	   term frequency ranks unfiltered queries sensibly
//	3: unselected poi_candidate observations leave the index; bm25 favors
//	   short docs, so candidate business names outranked real card matches
const searchIndexVersion = 3

// Search matching quality lives in the FTS index. FTS content is derived
// state, so an archive built by an older index version is rebuilt in place
// from the source tables, once, on the write path.
func ensureSearchIndex(ctx context.Context, db *store.Store, logger classifyLogger) error {
	if _, err := db.DB().ExecContext(ctx,
		`create table if not exists search_index_state(version integer not null)`,
	); err != nil {
		return fmt.Errorf("ensure search_index_state: %w", err)
	}
	var current int
	if err := db.DB().QueryRowContext(ctx,
		`select coalesce(max(version), 0) from search_index_state`,
	).Scan(&current); err != nil {
		return fmt.Errorf("read search index version: %w", err)
	}
	if current >= searchIndexVersion {
		return nil
	}
	start := time.Now()
	var assetRows, observationRows int64
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`drop table if exists asset_fts`,
			`drop table if exists observation_fts`,
			assetFTSSchema,
			observationFTSSchema,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("recreate fts tables: %w", err)
			}
		}
		res, err := tx.ExecContext(ctx, `
insert into asset_fts(id, title, body)
select asset.id,
       coalesce((select original_filename from asset_resource r where r.asset_id = asset.id order by r.id limit 1), ''),
       trim(asset.media_type || ' ' ||
            coalesce((select group_concat(original_filename, ' ') from asset_resource r where r.asset_id = asset.id), '') || ' ' ||
            coalesce((select group_concat(album_title, ' ') from album_membership m where m.asset_id = asset.id), ''))
from asset
`)
		if err != nil {
			return fmt.Errorf("rebuild asset fts: %w", err)
		}
		assetRows, _ = res.RowsAffected()

		for _, stmt := range []string{
			`insert into observation_fts(id, asset_id, title, body)
			 select id, asset_id, label, label from metadata_observation`,
			`insert into observation_fts(id, asset_id, title, body)
			 select id, asset_id, '', value_text from place_observation
			 where observation_type not in ('` + knownPlaceObservationType + `', 'poi_candidate')`,
		} {
			res, err := tx.ExecContext(ctx, stmt)
			if err != nil {
				return fmt.Errorf("rebuild observation fts: %w", err)
			}
			n, _ := res.RowsAffected()
			observationRows += n
		}

		knownRows, err := rebuildKnownPlaceFTS(ctx, tx)
		if err != nil {
			return err
		}
		cardRows, err := rebuildCardFTS(ctx, tx)
		if err != nil {
			return err
		}
		observationRows += knownRows + cardRows
		if _, err := tx.ExecContext(ctx, `delete from search_index_state`); err != nil {
			return fmt.Errorf("clear search index version: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `insert into search_index_state(version) values (?)`, searchIndexVersion); err != nil {
			return fmt.Errorf("write search index version: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("rebuild search index: %w", err)
	}
	logger.logPhase("search_index_rebuilt", time.Since(start),
		fmt.Sprintf("asset_rows=%d", assetRows),
		fmt.Sprintf("observation_rows=%d", observationRows),
		fmt.Sprintf("reason=version_%d_to_%d", current, searchIndexVersion))
	return nil
}

// Mirrors insertKnownPlaceObservation's FTS body.
func rebuildKnownPlaceFTS(ctx context.Context, tx *sql.Tx) (int64, error) {
	rows, err := tx.QueryContext(ctx, `
select id, asset_id, value_text, value_json
from place_observation
where observation_type = ?`, knownPlaceObservationType)
	if err != nil {
		return 0, fmt.Errorf("read known place observations: %w", err)
	}
	type knownRow struct{ id, assetID, body string }
	var pending []knownRow
	for rows.Next() {
		var id, assetID, label, valueJSON string
		if err := rows.Scan(&id, &assetID, &label, &valueJSON); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan known place observation: %w", err)
		}
		var value struct {
			Kind  string `json:"kind"`
			Name  string `json:"name"`
			After bool   `json:"after"`
		}
		if err := json.Unmarshal([]byte(valueJSON), &value); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("decode known place value for %s: %w", id, err)
		}
		body := strings.Join(uniqueNonEmpty([]string{
			label, value.Kind, value.Name, KnownPlaceCardLine(value.Kind, value.Name, value.After),
		}), " ")
		pending = append(pending, knownRow{id: id, assetID: assetID, body: body})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, row := range pending {
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`,
			row.id, row.assetID, row.body); err != nil {
			return 0, fmt.Errorf("rebuild known place fts: %w", err)
		}
	}
	return int64(len(pending)), nil
}

// Mirrors writeModelClassification's card FTS row: one row per carded asset,
// keyed by the card_summary observation id, body = the card's raw text fields.
func rebuildCardFTS(ctx context.Context, tx *sql.Tx) (int64, error) {
	rows, err := tx.QueryContext(ctx, `
select asset_id, id, observation_type, value_text
from model_observation
where observation_type in (?1, ?2, ?3, ?4)
order by asset_id,
         case observation_type when ?1 then 0 when ?2 then 1 when ?3 then 2 else 3 end,
         id`,
		modelObservationCardSummary, modelObservationCardDescription,
		modelObservationCardOCR, modelObservationCardUncertainty)
	if err != nil {
		return 0, fmt.Errorf("read card observations: %w", err)
	}
	type cardAccum struct {
		summaryID string
		texts     []string
	}
	cards := map[string]*cardAccum{}
	order := []string{}
	for rows.Next() {
		var assetID, id, observationType, valueText string
		if err := rows.Scan(&assetID, &id, &observationType, &valueText); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan card observation: %w", err)
		}
		accum := cards[assetID]
		if accum == nil {
			accum = &cardAccum{}
			cards[assetID] = accum
			order = append(order, assetID)
		}
		if observationType == modelObservationCardSummary && accum.summaryID == "" {
			accum.summaryID = id
		}
		accum.texts = append(accum.texts, valueText)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	var written int64
	for _, assetID := range order {
		accum := cards[assetID]
		if accum.summaryID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`,
			accum.summaryID, assetID, strings.Join(accum.texts, "\n")); err != nil {
			return 0, fmt.Errorf("rebuild card fts: %w", err)
		}
		written++
	}
	return written, nil
}
