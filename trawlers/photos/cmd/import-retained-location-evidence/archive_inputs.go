package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func loadLegacyLocalIdentifiersByAssetID(ctx context.Context, database *sql.DB) (map[string]string, error) {
	rows, err := database.QueryContext(ctx, `select id, local_identifier from asset`)
	if err != nil {
		return nil, fmt.Errorf("read legacy Photos asset identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	localIdentifiersByAssetID := make(map[string]string)
	for rows.Next() {
		var assetID, localIdentifier string
		if err := rows.Scan(&assetID, &localIdentifier); err != nil {
			return nil, err
		}
		if strings.TrimSpace(assetID) == "" || strings.TrimSpace(localIdentifier) == "" {
			return nil, errors.New("legacy Photos archive contains an incomplete asset identity")
		}
		localIdentifiersByAssetID[assetID] = localIdentifier
	}
	return localIdentifiersByAssetID, rows.Err()
}

func loadTargetCaptureIdentities(ctx context.Context, database *sql.DB) (map[string]*targetCaptureIdentity, map[string]*targetCaptureIdentity, error) {
	rows, err := database.QueryContext(ctx, `
select asset.id, asset.local_identifier, asset.creation_date, location_observation.latitude, location_observation.longitude
from asset
join location_observation on location_observation.asset_id = asset.id
order by asset.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("read current target capture identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	byLocalIdentifier := make(map[string]*targetCaptureIdentity)
	byAssetID := make(map[string]*targetCaptureIdentity)
	for rows.Next() {
		var assetID, localIdentifier, captureTimeText string
		var latitude, longitude float64
		if err := rows.Scan(&assetID, &localIdentifier, &captureTimeText, &latitude, &longitude); err != nil {
			return nil, nil, err
		}
		captureTime, err := time.Parse(time.RFC3339Nano, captureTimeText)
		if err != nil {
			return nil, nil, fmt.Errorf("parse current target capture time: %w", err)
		}
		if !finiteCoordinate(latitude, longitude) {
			return nil, nil, errors.New("target Photos archive contains an invalid capture coordinate")
		}
		identity := &targetCaptureIdentity{
			assetID:         assetID,
			localIdentifier: localIdentifier,
			captureTime:     timestamppb.New(captureTime),
			coordinate:      &locationwire.Coordinate{Latitude: latitude, Longitude: longitude},
		}
		if _, exists := byLocalIdentifier[localIdentifier]; exists {
			return nil, nil, errors.New("target Photos archive contains a duplicate Photos local identifier")
		}
		if _, exists := byAssetID[assetID]; exists {
			return nil, nil, errors.New("target Photos archive contains duplicate capture evidence for one asset")
		}
		byLocalIdentifier[localIdentifier] = identity
		byAssetID[assetID] = identity
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return byLocalIdentifier, byAssetID, nil
}

func loadKnownPlaceDefinitions(ctx context.Context, database *sql.DB) ([]*knownPlaceDefinition, error) {
	rows, err := database.QueryContext(ctx, `
select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at
from known_place
order by id`)
	if err != nil {
		return nil, fmt.Errorf("read canonical known-place definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	definitions := make([]*knownPlaceDefinition, 0, expectedKnownPlaceDefinitions)
	for rows.Next() {
		definition := new(knownPlaceDefinition)
		if err := rows.Scan(
			&definition.id,
			&definition.labelKind,
			&definition.displayName,
			&definition.latitude,
			&definition.longitude,
			&definition.radiusMetres,
			&definition.validFrom,
			&definition.validUntil,
			&definition.updatedAt,
		); err != nil {
			return nil, err
		}
		if err := validateKnownPlaceDefinition(definition); err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, rows.Err()
}

func validateKnownPlaceDefinition(definition *knownPlaceDefinition) error {
	if definition == nil || strings.TrimSpace(definition.id) == "" || strings.TrimSpace(definition.displayName) == "" {
		return errors.New("canonical known-place definition is incomplete")
	}
	switch definition.labelKind {
	case archive.KnownPlaceKindHome, archive.KnownPlaceKindFormerHome, archive.KnownPlaceKindWork:
	default:
		return errors.New("canonical known-place definition has an unsupported kind")
	}
	if !finiteCoordinate(definition.latitude, definition.longitude) || definition.radiusMetres <= 0 || math.IsNaN(definition.radiusMetres) || math.IsInf(definition.radiusMetres, 0) {
		return errors.New("canonical known-place definition has invalid geometry")
	}
	for _, optionalTime := range []string{definition.validFrom, definition.validUntil} {
		if optionalTime == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, optionalTime); err != nil {
			return errors.New("canonical known-place definition has an invalid validity time")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, definition.updatedAt); err != nil {
		return errors.New("canonical known-place definition has an invalid update time")
	}
	return nil
}

func recomputeKnownPlaceOutcomes(ctx context.Context, definitions []*knownPlaceDefinition, capturesByAssetID map[string]*targetCaptureIdentity) (map[string]*locationwire.MatchConfiguredKnownPlaceOutcome, error) {
	matchingStore, err := store.Open(ctx, store.Options{Path: ":memory:", Schema: archive.Schema})
	if err != nil {
		return nil, fmt.Errorf("open in-memory known-place matching store: %w", err)
	}
	defer func() { _ = matchingStore.Close() }()
	for _, definition := range definitions {
		if _, err := matchingStore.DB().ExecContext(ctx, `
insert into known_place(id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			definition.id,
			definition.labelKind,
			definition.displayName,
			definition.latitude,
			definition.longitude,
			definition.radiusMetres,
			definition.validFrom,
			definition.validUntil,
			definition.updatedAt,
		); err != nil {
			return nil, fmt.Errorf("load real known-place definition into dry-run matcher: %w", err)
		}
	}
	knownPlaceConfigurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, matchingStore)
	if err != nil {
		return nil, fmt.Errorf("digest current known-place configuration: %w", err)
	}
	assetIDs := make([]string, 0, len(capturesByAssetID))
	for assetID := range capturesByAssetID {
		assetIDs = append(assetIDs, assetID)
	}
	sort.Strings(assetIDs)
	outcomes := make(map[string]*locationwire.MatchConfiguredKnownPlaceOutcome, len(assetIDs))
	for _, assetID := range assetIDs {
		input := captureLocationInput(capturesByAssetID[assetID])
		outcome, err := archive.MatchConfiguredKnownPlace(ctx, matchingStore, &locationwire.MatchConfiguredKnownPlaceRequest{
			Input:                         input,
			KnownPlaceConfigurationSha256: knownPlaceConfigurationSHA256,
		})
		if err != nil {
			return nil, fmt.Errorf("recompute current known-place match: %w", err)
		}
		outcomes[assetID] = outcome
	}
	return outcomes, nil
}

func captureLocationInput(identity *targetCaptureIdentity) *locationwire.CaptureLocationInput {
	return &locationwire.CaptureLocationInput{
		AssetId:     identity.assetID,
		CaptureTime: identity.captureTime,
		Coordinate:  identity.coordinate,
	}
}

func reuseStoredKnownPlaceOutcomesWithExactCurrentRequest(ctx context.Context, database *sql.DB, recomputed map[string]*locationwire.MatchConfiguredKnownPlaceOutcome) error {
	for assetID, current := range recomputed {
		var encoded []byte
		err := database.QueryRowContext(ctx, `select outcome_proto from configured_known_place_match_outcome where asset_id=?`, assetID).Scan(&encoded)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		stored := new(locationwire.MatchConfiguredKnownPlaceOutcome)
		if err := proto.Unmarshal(encoded, stored); err != nil {
			return fmt.Errorf("decode stored known-place outcome: %w", err)
		}
		if knownPlaceOutcomeSatisfiesCurrentRequest(stored, current.GetRequest()) {
			recomputed[assetID] = stored
		}
	}
	return nil
}

func countDiscardedLegacyJudgements(ctx context.Context, database *sql.DB) (knownObservations, venueRows, tierJudgements int, err error) {
	queries := []struct {
		destination *int
		query       string
	}{
		{&knownObservations, `select count(*) from place_observation where observation_type='known_place'`},
		{&venueRows, `select count(*) from place_observation where observation_type='venue'`},
		{&tierJudgements, `select count(*) from place_observation where observation_type='poi_candidate' and tier in ('venue_candidate','confirmed_venue')`},
	}
	for _, item := range queries {
		if scanErr := database.QueryRowContext(ctx, item.query).Scan(item.destination); scanErr != nil {
			return 0, 0, 0, scanErr
		}
	}
	return knownObservations, venueRows, tierJudgements, nil
}

func finiteCoordinate(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsNaN(longitude) && !math.IsInf(latitude, 0) && !math.IsInf(longitude, 0) && latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
