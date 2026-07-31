package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardformat"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
)

const placeObservationSource = "place_context"

func writeCheckedPlaceContextFromCardInput(ctx context.Context, tx *sql.Tx, input classifyInput, prepared preparedCardRequest, generationID string, classifiedAt time.Time) (int, error) {
	if strings.TrimSpace(generationID) == "" {
		return 0, fmt.Errorf("model generation id is required")
	}
	written := 0
	preserveIDs := []string{}
	canonical := prepared.Input.Input
	if canonical == nil {
		return 0, fmt.Errorf("prepared CardInput is required")
	}
	if known := canonical.GetKnownPlace(); known != nil {
		match := KnownPlaceMatch{Kind: known.GetRelationship(), Name: known.GetName()}
		observationID := knownPlaceObservationID(input.AssetID, generationID, match)
		preserveIDs = append(preserveIDs, observationID)
		n, err := insertKnownPlaceObservation(ctx, tx, input.AssetID, generationID, match)
		if err != nil {
			return written, err
		}
		written += n
	}
	for index, projection := range canonical.GetPlaces() {
		address := projection.GetAddress()
		text := cardAddressLine(address)
		if text == "" {
			continue
		}
		observationID := stableID("place_observation", input.AssetID, generationID, "address", fmt.Sprint(index+1))
		preserveIDs = append(preserveIDs, observationID)
		value := map[string]any{
			"address":             address,
			"place_position":      index + 1,
			"provider":            projection.GetProviderIdentity(),
			"operation":           projection.GetOperation(),
			"coordinate_variant":  projection.GetCoordinateVariant(),
			"raw_response_sha256": projection.GetRawResponseSha256(),
		}
		n, err := insertPlaceObservationWithID(ctx, tx, observationID, input.AssetID, "", "address", text, value,
			projection.GetProviderIdentity(), "checked_card_input", place.TierAreaContext, 0)
		if err != nil {
			return written, err
		}
		written += n
	}
	for _, candidate := range prepared.CandidatesInSeq {
		text := strings.TrimSpace(candidate.Name)
		if text == "" {
			text = strings.TrimSpace(candidate.ProviderID)
		}
		if text == "" {
			text = candidate.ID
		}
		value := preparedPlaceCandidateValue(candidate)
		observationID := placeObservationID(input.AssetID, generationID, "poi_candidate", candidate.ID, "provider_candidate")
		preserveIDs = append(preserveIDs, observationID)
		n, err := insertPlaceObservationWithID(ctx, tx, observationID, input.AssetID, "", "poi_candidate", text, value, candidate.Provider, "checked_card_input", "provider_candidate", candidate.DistanceMeters)
		if err != nil {
			return written, err
		}
		written += n
	}
	if err := supersedePlaceObservations(ctx, tx, input.AssetID, preserveIDs, classifiedAt); err != nil {
		return written, err
	}
	return written, nil
}

func cardAddressLine(address *cardwire.Address) string {
	if address == nil {
		return ""
	}
	return place.FormatAddress(&place.Address{
		Name:                  address.GetName(),
		Thoroughfare:          address.GetThoroughfare(),
		SubThoroughfare:       address.GetSubThoroughfare(),
		Locality:              address.GetLocality(),
		SubLocality:           address.GetSubLocality(),
		AdministrativeArea:    address.GetAdministrativeArea(),
		SubAdministrativeArea: address.GetSubAdministrativeArea(),
		PostalCode:            address.GetPostalCode(),
		Country:               address.GetCountry(),
		ISOCountryCode:        address.GetIsoCountryCode(),
		TimeZone:              address.GetTimeZone(),
		AreasOfInterest:       address.GetAreasOfInterest(),
		Formatted:             address.GetFormatted(),
		Source:                address.GetSource(),
	})
}

func insertPlaceObservationWithID(ctx context.Context, tx *sql.Tx, observationID, assetID, modelGenerationID, kind, text string, value any, provider, cacheStatus, tier string, distance float64) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	valueJSON, err := jsonText(value)
	if err != nil {
		return 0, err
	}
	var distanceValue any
	if distance > 0 {
		distanceValue = distance
	}
	if kind != "poi_candidate" {
		if _, err := tx.ExecContext(ctx, `delete from observation_fts where id = ?`, observationID); err != nil {
			return 0, fmt.Errorf("clear existing place fts: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, generation_id, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do nothing
`, observationID, assetID, kind, text, valueJSON, placeObservationSource, provider, cacheStatus, tier, distanceValue, nullableGenerationID(modelGenerationID), "")
	if err != nil {
		return 0, fmt.Errorf("write place observation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read place observation insert count: %w", err)
	}
	// Unselected POI candidates are selection provenance, not claims about
	// the photo. They stay out of the search index so a nearby "Meadow
	// Grill" cannot outrank a card that is actually about grilling.
	if kind != "poi_candidate" {
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, assetID, "", text); err != nil {
			return 0, fmt.Errorf("write place fts: %w", err)
		}
	}
	return int(inserted), nil
}

func preparedPlaceCandidateValue(candidate preparedPlaceCandidate) map[string]any {
	value := map[string]any{
		"candidate_id":        candidate.ID,
		"provider_index":      candidate.ProviderIndex,
		"place_position":      candidate.PlacePosition,
		"candidate_position":  candidate.CandidatePosition,
		"name":                candidate.Name,
		"distance_m":          candidate.DistanceMeters,
		"source":              candidate.Source,
		"provider":            candidate.Provider,
		"raw_response_sha256": candidate.RawResponseID,
	}
	if strings.TrimSpace(candidate.ProviderID) != "" {
		value["provider_id"] = candidate.ProviderID
	}
	if len(candidate.Categories) > 0 {
		value["categories"] = candidate.Categories
	}
	if candidate.Coordinate != nil {
		value["coordinate"] = candidate.Coordinate
	}
	if candidate.Address != nil {
		value["address"] = candidate.Address
	}
	return value
}

func insertKnownPlaceObservation(ctx context.Context, tx *sql.Tx, assetID, identity string, match KnownPlaceMatch) (int, error) {
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
	observationID := knownPlaceObservationID(assetID, identity, match)
	if _, err := tx.ExecContext(ctx, `delete from observation_fts where id = ?`, observationID); err != nil {
		return 0, fmt.Errorf("clear existing known place fts: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, generation_id, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, null, ?)
on conflict(id) do nothing
`, observationID, assetID, knownPlaceObservationType, label, valueJSON, knownPlaceSource, knownPlaceSource, "match", knownPlaceTier, match.DistanceMeters, "")
	if err != nil {
		return 0, fmt.Errorf("write known place observation: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read known place observation insert count: %w", err)
	}
	body := strings.Join(uniqueNonEmpty([]string{label, match.Kind, match.Name, KnownPlaceCardLine(match.Kind, match.Name, match.After)}), " ")
	if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, assetID, "", body); err != nil {
		return 0, fmt.Errorf("write known place fts: %w", err)
	}
	return int(inserted), nil
}

func supersedePlaceObservations(ctx context.Context, tx *sql.Tx, assetID string, preserveIDs []string, supersededAt time.Time) error {
	if strings.TrimSpace(assetID) == "" {
		return nil
	}
	timestamp := supersededAt.UTC().Format(time.RFC3339Nano)
	preserveClause := ""
	if len(preserveIDs) > 0 {
		preserveClause = " and id not in (" + strings.TrimRight(strings.Repeat("?,", len(preserveIDs)), ",") + ")"
	}
	args := []any{assetID}
	for _, id := range preserveIDs {
		args = append(args, id)
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (
    select id from place_observation
    where asset_id = ? and superseded_at is null`+preserveClause+`
  )
`, append([]any{assetID}, args...)...); err != nil {
		return fmt.Errorf("clear superseded place observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
update place_observation
set superseded_at = ?
where asset_id = ? and superseded_at is null
  `+preserveClause+`
`, append([]any{timestamp}, args...)...); err != nil {
		return fmt.Errorf("supersede place observations: %w", err)
	}
	return nil
}

func placeObservationID(assetID, identity, kind, text, tier string) string {
	return stableID("place_observation", assetID, identity, kind, strings.TrimSpace(text), tier)
}

func knownPlaceObservationID(assetID, identity string, match KnownPlaceMatch) string {
	return stableID("place_observation", assetID, identity, knownPlaceObservationType, match.Kind, match.Name)
}

func nullableGenerationID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func placeCategory(category string) string {
	return cardformat.NormalizePOICategory(category)
}
