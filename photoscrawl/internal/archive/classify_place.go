package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/cardformat"
	"github.com/openclaw/photoscrawl/internal/place"
)

const placeObservationSource = "place_context"

func enrichClassifyPlaces(ctx context.Context, paths Paths, inputs []classifyInput, result *ClassifyResult) error {
	knownPlaces, err := loadKnownPlacesForClassify(ctx, paths)
	if err != nil {
		return err
	}
	resolver := place.NewResolver(place.ResolverOptions{
		CacheDir:          paths.PlaceContextCacheDir(),
		LegacyBackfillDir: paths.LegacyPlaceBackfillDir(),
		RadiusMeters:      150,
	})
	for i := range inputs {
		if !inputs[i].HasLocation {
			continue
		}
		resolved := resolver.Resolve(ctx, place.Input{
			AssetID: inputs[i].AssetID,
			TakenAt: inputs[i].CreationDate,
			Location: place.Coordinate{
				Latitude:  inputs[i].Latitude,
				Longitude: inputs[i].Longitude,
			},
			AccuracyMeters: inputs[i].AccuracyMeters,
		})
		switch resolved.CacheStatus {
		case "hit":
			result.PlaceCacheHits++
		case "backfill_hit":
			result.PlaceCacheHits++
			result.PlaceBackfillHits++
		}
		if resolved.ProviderAttempt {
			result.PlaceProviderAttempts++
		}
		if strings.TrimSpace(resolved.ProviderError) != "" {
			result.PlaceProviderFailures++
		}
		if resolved.Result == nil {
			inputs[i].KnownPlace = matchKnownPlace(knownPlaces, inputs[i].Latitude, inputs[i].Longitude, inputs[i].CreationDate)
			continue
		}
		inputs[i].Place = &classifyPlaceContext{
			Result:      *resolved.Result,
			CacheStatus: resolved.CacheStatus,
		}
		inputs[i].KnownPlace = matchKnownPlace(knownPlaces, inputs[i].Latitude, inputs[i].Longitude, inputs[i].CreationDate)
	}
	return nil
}

func loadKnownPlacesForClassify(ctx context.Context, paths Paths) ([]KnownPlace, error) {
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return loadKnownPlaces(ctx, db.DB())
}

func writePlaceClassification(ctx context.Context, tx *sql.Tx, input classifyInput, plausibility venuePlausibility, observedAt time.Time) (int, error) {
	if input.Place == nil && input.KnownPlace == nil {
		return 0, clearPlaceObservations(ctx, tx, input.AssetID)
	}
	if err := clearPlaceObservations(ctx, tx, input.AssetID); err != nil {
		return 0, err
	}
	written := 0
	if input.KnownPlace != nil {
		n, err := insertKnownPlaceObservation(ctx, tx, input.AssetID, *input.KnownPlace, observedAt)
		if err != nil {
			return written, err
		}
		written += n
	}
	if input.Place == nil {
		return written, nil
	}
	result := input.Place.Result
	place.NormalizeResult(&result)
	candidates := topPOICandidates(applyVenuePlausibility(result.POICandidates, plausibility))
	evidenceID := stableID("evidence", input.AssetID, "place_context", result.Provider, input.Place.CacheStatus)
	evidenceJSON, err := jsonText(map[string]any{
		"provider":      result.Provider,
		"source":        result.Source,
		"cache_status":  input.Place.CacheStatus,
		"radius_meters": result.RadiusMeters,
		"generated_at":  result.GeneratedAt,
		"place_context": result,
		"observed_at":   observedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return written, err
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
`, evidenceID, input.AssetID, "place_context", placeObservationSource, input.AssetID+"/place_context", evidenceJSON); err != nil {
		return written, fmt.Errorf("write place evidence: %w", err)
	}
	if address := addressLine(result.Address); address != "" {
		n, err := insertPlaceObservation(ctx, tx, input.AssetID, evidenceID, "address", address, map[string]any{
			"address": result.Address,
			"area":    result.Area,
		}, result.Provider, input.Place.CacheStatus, place.TierAreaContext, 0)
		if err != nil {
			return written, err
		}
		written += n
	}
	if input.KnownPlace != nil {
		return written, nil
	}
	seenCandidates := map[string]bool{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Name) == "" {
			continue
		}
		// The place cache can list the same business twice; candidates are
		// nearest-first, so the first occurrence wins.
		key := strings.ToLower(strings.TrimSpace(candidate.Name)) + "\x00" + candidate.Tier
		if seenCandidates[key] {
			continue
		}
		seenCandidates[key] = true
		value := placeCandidateValue(candidate)
		n, err := insertPlaceObservation(ctx, tx, input.AssetID, evidenceID, "poi_candidate", candidate.Name, value, result.Provider, input.Place.CacheStatus, candidate.Tier, candidate.DistanceM)
		if err != nil {
			return written, err
		}
		written += n
		tier, ok := venueLineTier(candidate)
		if !ok {
			continue
		}
		value["tier"] = tier
		n, err = insertPlaceObservation(ctx, tx, input.AssetID, evidenceID, "venue", candidate.Name, value, result.Provider, input.Place.CacheStatus, tier, candidate.DistanceM)
		if err != nil {
			return written, err
		}
		written += n
		break
	}
	return written, nil
}

func insertPlaceObservation(ctx context.Context, tx *sql.Tx, assetID, evidenceID, kind, text string, value any, provider, cacheStatus, tier string, distance float64) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	valueJSON, err := jsonText(value)
	if err != nil {
		return 0, err
	}
	observationID := stableID("place_observation", assetID, kind, text, tier)
	var distanceValue any
	if distance > 0 {
		distanceValue = distance
	}
	if _, err := tx.ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, observationID, assetID, kind, text, valueJSON, placeObservationSource, provider, cacheStatus, tier, distanceValue, evidenceID); err != nil {
		return 0, fmt.Errorf("write place observation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, assetID, "", text); err != nil {
		return 0, fmt.Errorf("write place fts: %w", err)
	}
	return 1, nil
}

func insertKnownPlaceObservation(ctx context.Context, tx *sql.Tx, assetID string, match KnownPlaceMatch, observedAt time.Time) (int, error) {
	label := KnownPlaceWhereLabel(match.Kind, match.Name, match.After)
	if label == "" {
		return 0, nil
	}
	value := map[string]any{
		"kind":  match.Kind,
		"name":  match.Name,
		"after": match.After,
	}
	valueJSON, err := jsonText(value)
	if err != nil {
		return 0, err
	}
	evidenceID := stableID("evidence", assetID, knownPlaceObservationType, match.Kind, match.Name)
	evidenceJSON, err := jsonText(map[string]any{
		"known_place":    value,
		"distance_m":     match.DistanceMeters,
		"matched_at":     observedAt.Format(time.RFC3339Nano),
		"matching_layer": knownPlaceSource,
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
`, evidenceID, assetID, knownPlaceObservationType, knownPlaceSource, assetID+"/known_place", evidenceJSON); err != nil {
		return 0, fmt.Errorf("write known place evidence: %w", err)
	}
	observationID := stableID("place_observation", assetID, knownPlaceObservationType, match.Kind, match.Name)
	if _, err := tx.ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, observationID, assetID, knownPlaceObservationType, label, valueJSON, knownPlaceSource, knownPlaceSource, "match", knownPlaceTier, match.DistanceMeters, evidenceID); err != nil {
		return 0, fmt.Errorf("write known place observation: %w", err)
	}
	body := strings.Join(uniqueNonEmpty([]string{label, match.Kind, match.Name, KnownPlaceCardLine(match.Kind, match.Name, match.After)}), " ")
	if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, assetID, "", body); err != nil {
		return 0, fmt.Errorf("write known place fts: %w", err)
	}
	return 1, nil
}

func clearPlaceObservations(ctx context.Context, tx *sql.Tx, assetID string) error {
	if strings.TrimSpace(assetID) == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (select id from place_observation where asset_id = ?)
`, assetID, assetID); err != nil {
		return fmt.Errorf("clear place observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `delete from place_observation where asset_id = ?`, assetID); err != nil {
		return fmt.Errorf("clear place observations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
delete from evidence_ref
where asset_id = ?
  and (
    (evidence_kind = 'place_context' and source = ?)
    or (evidence_kind = ? and source = ?)
  )
`, assetID, placeObservationSource, knownPlaceObservationType, knownPlaceSource); err != nil {
		return fmt.Errorf("clear place evidence: %w", err)
	}
	return nil
}

func addressLine(address *place.Address) string {
	return place.FormatAddress(address)
}

func applyVenuePlausibility(candidates []place.POICandidate, plausibility venuePlausibility) []venueCandidate {
	out := make([]venueCandidate, 0, len(candidates))
	topIndex := topProviderVenueCandidate(candidates, plausibility)
	for i, candidate := range candidates {
		row := venueCandidate{POICandidate: candidate}
		if i == topIndex && plausibility.Verdict != "" {
			if plausibility.CandidateName == "" {
				plausibility.CandidateName = candidate.Name
			}
			row.Plausibility = plausibility
			if plausibility.Verdict == venueVerdictCorroborated {
				row.Tier = place.TierConfirmedVenue
			}
		}
		out = append(out, row)
	}
	return out
}

func topProviderVenueCandidate(candidates []place.POICandidate, plausibility venuePlausibility) int {
	if plausibility.CandidateName != "" {
		target := strings.ToLower(strings.TrimSpace(plausibility.CandidateName))
		for i, candidate := range candidates {
			if strings.ToLower(strings.TrimSpace(candidate.Name)) == target {
				return i
			}
		}
	}
	for i, candidate := range candidates {
		if candidate.Tier == place.TierVenueCandidate {
			return i
		}
	}
	if len(candidates) > 0 {
		return 0
	}
	return -1
}

func venueLineTier(candidate venueCandidate) (string, bool) {
	switch candidate.Plausibility.Verdict {
	case venueVerdictCorroborated:
		return place.TierConfirmedVenue, true
	case venueVerdictPlausible:
		return candidate.Tier, candidate.Tier == place.TierVenueCandidate
	default:
		return "", false
	}
}

func placeCandidateValue(candidate venueCandidate) map[string]any {
	value := map[string]any{
		"name":       candidate.Name,
		"distance_m": candidate.DistanceM,
		"tier":       candidate.Tier,
		"source":     candidate.Source,
	}
	if category := placeCategory(candidate.Category); category != "" {
		value["category"] = category
	}
	if candidate.Coordinate != nil {
		value["coordinate"] = candidate.Coordinate
	}
	if candidate.Address != nil {
		value["address"] = candidate.Address
	}
	if len(candidate.Provenance) > 0 {
		value["provenance"] = candidate.Provenance
	}
	if candidate.Plausibility.Verdict != "" {
		value["venue_plausibility"] = candidate.Plausibility
	}
	return value
}

func placeCategory(category string) string {
	return cardformat.NormalizePOICategory(category)
}
