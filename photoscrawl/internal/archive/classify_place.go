package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openclaw/photoscrawl/internal/cardformat"
	"github.com/openclaw/photoscrawl/internal/place"
)

const placeObservationSource = "place_context"

func writePlaceClassification(ctx context.Context, tx *sql.Tx, input classifyInput, plausibility venuePlausibility) (int, error) {
	if input.Place == nil && input.KnownPlace == nil {
		return 0, clearPlaceObservations(ctx, tx, input.AssetID)
	}
	if err := clearPlaceObservations(ctx, tx, input.AssetID); err != nil {
		return 0, err
	}
	written := 0
	if input.KnownPlace != nil {
		n, err := insertKnownPlaceObservation(ctx, tx, input.AssetID, *input.KnownPlace)
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
	candidates := applyVenuePlausibility(result.POICandidates, plausibility)
	if address := addressLine(result.Address); address != "" {
		n, err := insertPlaceObservation(ctx, tx, input.AssetID, "address", address, map[string]any{
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
		n, err := insertPlaceObservation(ctx, tx, input.AssetID, "poi_candidate", candidate.Name, value, result.Provider, input.Place.CacheStatus, candidate.Tier, candidate.DistanceM)
		if err != nil {
			return written, err
		}
		written += n
		tier, ok := venueLineTier(candidate)
		if !ok {
			continue
		}
		value["tier"] = tier
		n, err = insertPlaceObservation(ctx, tx, input.AssetID, "venue", candidate.Name, value, result.Provider, input.Place.CacheStatus, tier, candidate.DistanceM)
		if err != nil {
			return written, err
		}
		written += n
		break
	}
	return written, nil
}

func insertPlaceObservation(ctx context.Context, tx *sql.Tx, assetID, kind, text string, value any, provider, cacheStatus, tier string, distance float64) (int, error) {
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
`, observationID, assetID, kind, text, valueJSON, placeObservationSource, provider, cacheStatus, tier, distanceValue, ""); err != nil {
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

func insertKnownPlaceObservation(ctx context.Context, tx *sql.Tx, assetID string, match KnownPlaceMatch) (int, error) {
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
	observationID := stableID("place_observation", assetID, knownPlaceObservationType, match.Kind, match.Name)
	if _, err := tx.ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, observationID, assetID, knownPlaceObservationType, label, valueJSON, knownPlaceSource, knownPlaceSource, "match", knownPlaceTier, match.DistanceMeters, ""); err != nil {
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
	return nil
}

func addressLine(address *place.Address) string {
	return place.FormatAddress(address)
}

func applyVenuePlausibility(candidates []place.POICandidate, plausibility venuePlausibility) []venueCandidate {
	out := topPOICandidates(venueCandidatesFromPOIs(candidates))
	for i := range out {
		if plausibility.CandidateID == "" || plausibility.CandidateID != venueCandidateID(i) || plausibility.Verdict == "" {
			continue
		}
		row := plausibility
		row.CandidateID = venueCandidateID(i)
		out[i].Plausibility = row
		if row.Verdict == venueVerdictCorroborated {
			out[i].Tier = place.TierConfirmedVenue
		}
	}
	return out
}

func venueCandidatesFromPOIs(candidates []place.POICandidate) []venueCandidate {
	out := make([]venueCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, venueCandidate{POICandidate: candidate})
	}
	return out
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
