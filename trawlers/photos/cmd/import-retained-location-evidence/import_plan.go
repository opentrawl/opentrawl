package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type requiredTargetWrites struct {
	knownPlaceDefinitions int
	knownPlaceOutcomes    plannedOutcomeChanges
	appleReverseOutcomes  plannedOutcomeChanges
	geoapifyReverse       plannedOutcomeChanges
	geoapifyNearby        plannedOutcomeChanges
}

type plannedOutcomeChanges struct {
	inserts                     int
	replacements                int
	replacedDifferentRequest    int
	replacedInsufficientOutcome int
	unchanged                   int
	untouchedExisting           int
}

func (changes plannedOutcomeChanges) writes() int {
	return changes.inserts + changes.replacements
}

func validateCanonicalImportPlan(plan *importPlan) error {
	if plan == nil {
		return errors.New("location import plan is missing")
	}
	checks := []struct {
		name     string
		observed int
		expected int
	}{
		{"known-place definitions", len(plan.knownPlaceDefinitions), expectedKnownPlaceDefinitions},
		{"retained Apple output files", plan.appleOutputFiles, expectedRetainedAppleOutputs},
		{"coordinate-matched Apple reverse outcomes", plan.appleCoordinateMatches, expectedAppleCoordinateMatches},
		{"coordinate-mismatched Apple outputs", plan.appleCoordinateMismatches, expectedAppleCoordinateMismatches},
		{"Apple coordinate floating-point round trips", plan.appleFloatingPointRoundTrips, expectedAppleFloatingPointRoundTrips},
		{"typed Apple reverse outcomes", len(plan.appleReverseOutcomes), expectedAppleCoordinateMatches},
		{"typed Geoapify reverse proof outcomes", len(plan.geoapifyReverse), expectedGeoapifyProofOutcomes},
		{"typed Geoapify nearby proof outcomes", len(plan.geoapifyNearby), expectedGeoapifyProofOutcomes},
		{"discarded legacy known-place observations", plan.legacyKnownPlaceObservations, expectedLegacyKnownPlaceObservations},
		{"discarded legacy venue rows", plan.discardedVenueRows, expectedDiscardedVenueRows},
		{"discarded legacy tier judgements", plan.discardedTierJudgements, expectedDiscardedTierJudgements},
	}
	validationFailures := make([]error, 0)
	for _, check := range checks {
		if check.observed != check.expected {
			validationFailures = append(validationFailures, fmt.Errorf("%s: observed %d, audited canonical count is %d", check.name, check.observed, check.expected))
		}
	}
	if plan.appleMissingTargetAssets != 0 {
		validationFailures = append(validationFailures, fmt.Errorf("retained Apple outputs whose Photos local identifier is absent from the target archive: %d", plan.appleMissingTargetAssets))
	}
	if len(plan.knownPlaceOutcomes) == 0 {
		validationFailures = append(validationFailures, errors.New("no current known-place outcomes were recomputed"))
	}
	return errors.Join(validationFailures...)
}

func countRequiredTargetWrites(ctx context.Context, targetDatabase *sql.DB, plan *importPlan) (requiredTargetWrites, error) {
	writes := requiredTargetWrites{}
	knownDefinitionsCurrent, targetKnownPlaceCount, err := targetKnownPlaceDefinitionsAreCurrent(ctx, targetDatabase, plan.knownPlaceDefinitions)
	if err != nil {
		return writes, err
	}
	if targetKnownPlaceCount != 0 && !knownDefinitionsCurrent {
		return writes, errors.New("target archive contains a different known-place definition set; refusing to merge two configurations")
	}
	if !knownDefinitionsCurrent {
		writes.knownPlaceDefinitions = len(plan.knownPlaceDefinitions)
	}
	writes.knownPlaceOutcomes, err = classifyKnownPlaceOutcomeChanges(ctx, targetDatabase, plan.knownPlaceOutcomes, knownDefinitionsCurrent)
	if err != nil {
		return writes, err
	}
	writes.appleReverseOutcomes, err = classifyAppleReverseOutcomeChanges(ctx, targetDatabase, plan.appleReverseOutcomes)
	if err != nil {
		return writes, err
	}
	writes.geoapifyReverse, err = classifyGeoapifyReverseOutcomeChanges(ctx, targetDatabase, plan.geoapifyReverse)
	if err != nil {
		return writes, err
	}
	writes.geoapifyNearby, err = classifyGeoapifyNearbyOutcomeChanges(ctx, targetDatabase, plan.geoapifyNearby)
	return writes, err
}

func targetKnownPlaceDefinitionsAreCurrent(ctx context.Context, database *sql.DB, wanted []*knownPlaceDefinition) (bool, int, error) {
	rows, err := database.QueryContext(ctx, `
select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at
from known_place
order by id`)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = rows.Close() }()
	existing := make([]*knownPlaceDefinition, 0, len(wanted))
	for rows.Next() {
		definition := new(knownPlaceDefinition)
		if err := rows.Scan(&definition.id, &definition.labelKind, &definition.displayName, &definition.latitude, &definition.longitude, &definition.radiusMetres, &definition.validFrom, &definition.validUntil, &definition.updatedAt); err != nil {
			return false, 0, err
		}
		existing = append(existing, definition)
	}
	if err := rows.Err(); err != nil {
		return false, 0, err
	}
	if len(existing) != len(wanted) {
		return false, len(existing), nil
	}
	for index := range existing {
		if *existing[index] != *wanted[index] {
			return false, len(existing), nil
		}
	}
	return true, len(existing), nil
}

func classifyKnownPlaceOutcomeChanges(ctx context.Context, database *sql.DB, wanted map[string]*locationwire.MatchConfiguredKnownPlaceOutcome, definitionsCurrent bool) (plannedOutcomeChanges, error) {
	changes := plannedOutcomeChanges{}
	for _, assetID := range sortedMapKeys(wanted) {
		existing := new(locationwire.MatchConfiguredKnownPlaceOutcome)
		found, err := loadStoredProto(ctx, database, "configured_known_place_match_outcome", assetID, existing)
		if err != nil {
			return changes, err
		}
		if !found {
			changes.inserts++
		} else if definitionsCurrent && knownPlaceOutcomeSatisfiesCurrentRequest(existing, wanted[assetID].GetRequest()) {
			changes.unchanged++
		} else {
			changes.replacements++
			if proto.Equal(existing.GetRequest(), wanted[assetID].GetRequest()) {
				changes.replacedInsufficientOutcome++
			} else {
				changes.replacedDifferentRequest++
			}
		}
	}
	return completeOutcomeChangeCounts(ctx, database, "configured_known_place_match_outcome", changes)
}

func classifyAppleReverseOutcomeChanges(ctx context.Context, database *sql.DB, wanted map[string]*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome) (plannedOutcomeChanges, error) {
	changes := plannedOutcomeChanges{}
	for _, assetID := range sortedMapKeys(wanted) {
		existing := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
		found, err := loadStoredProto(ctx, database, "apple_reverse_geocoding_evidence_outcome", assetID, existing)
		if err != nil {
			return changes, err
		}
		if !found {
			changes.inserts++
		} else {
			sameRequest := proto.Equal(existing.GetRequest(), wanted[assetID].GetRequest())
			if sameRequest && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), false) {
				changes.unchanged++
			} else {
				changes.replacements++
				if sameRequest {
					changes.replacedInsufficientOutcome++
				} else {
					changes.replacedDifferentRequest++
				}
			}
		}
	}
	return completeOutcomeChangeCounts(ctx, database, "apple_reverse_geocoding_evidence_outcome", changes)
}

func classifyGeoapifyReverseOutcomeChanges(ctx context.Context, database *sql.DB, wanted map[string]*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome) (plannedOutcomeChanges, error) {
	changes := plannedOutcomeChanges{}
	for _, assetID := range sortedMapKeys(wanted) {
		existing := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
		found, err := loadStoredProto(ctx, database, "geoapify_reverse_geocoding_evidence_outcome", assetID, existing)
		if err != nil {
			return changes, err
		}
		if !found {
			changes.inserts++
		} else {
			sameRequest := proto.Equal(existing.GetRequest(), wanted[assetID].GetRequest())
			if sameRequest && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), false) {
				changes.unchanged++
			} else {
				changes.replacements++
				if sameRequest {
					changes.replacedInsufficientOutcome++
				} else {
					changes.replacedDifferentRequest++
				}
			}
		}
	}
	return completeOutcomeChangeCounts(ctx, database, "geoapify_reverse_geocoding_evidence_outcome", changes)
}

func classifyGeoapifyNearbyOutcomeChanges(ctx context.Context, database *sql.DB, wanted map[string]*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome) (plannedOutcomeChanges, error) {
	changes := plannedOutcomeChanges{}
	for _, assetID := range sortedMapKeys(wanted) {
		existing := new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
		found, err := loadStoredProto(ctx, database, "geoapify_nearby_place_evidence_outcome", assetID, existing)
		if err != nil {
			return changes, err
		}
		if !found {
			changes.inserts++
		} else {
			sameRequest := proto.Equal(existing.GetRequest(), wanted[assetID].GetRequest())
			if sameRequest && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), true) {
				changes.unchanged++
			} else {
				changes.replacements++
				if sameRequest {
					changes.replacedInsufficientOutcome++
				} else {
					changes.replacedDifferentRequest++
				}
			}
		}
	}
	return completeOutcomeChangeCounts(ctx, database, "geoapify_nearby_place_evidence_outcome", changes)
}

func completeOutcomeChangeCounts(ctx context.Context, database *sql.DB, tableName string, changes plannedOutcomeChanges) (plannedOutcomeChanges, error) {
	var existingRows int
	if err := database.QueryRowContext(ctx, `select count(*) from `+tableName).Scan(&existingRows); err != nil {
		return changes, err
	}
	changes.untouchedExisting = existingRows - changes.replacements - changes.unchanged
	if changes.untouchedExisting < 0 {
		return changes, errors.New("planned location outcome counts are inconsistent")
	}
	return changes, nil
}

func loadStoredProto(ctx context.Context, database *sql.DB, tableName, assetID string, destination proto.Message) (bool, error) {
	var encoded []byte
	err := database.QueryRowContext(ctx, `select outcome_proto from `+tableName+` where asset_id=?`, assetID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := proto.Unmarshal(encoded, destination); err != nil {
		return false, fmt.Errorf("decode existing typed location outcome: %w", err)
	}
	return true, nil
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func applyImportPlan(ctx context.Context, targetArchivePath string, plan *importPlan) error {
	targetStore, err := store.Open(ctx, store.Options{Path: targetArchivePath, Schema: archive.Schema})
	if err != nil {
		return fmt.Errorf("open current target archive for approved apply: %w", err)
	}
	defer func() { _ = targetStore.Close() }()
	return withTransaction(ctx, targetStore.DB(), func(transaction *sql.Tx) error {
		definitionsCurrent, existingDefinitionCount, err := targetKnownPlaceDefinitionsAreCurrentInTransaction(ctx, transaction, plan.knownPlaceDefinitions)
		if err != nil {
			return err
		}
		if existingDefinitionCount != 0 && !definitionsCurrent {
			return errors.New("target known-place definitions changed after dry-run")
		}
		if !definitionsCurrent {
			for _, definition := range plan.knownPlaceDefinitions {
				if _, err := transaction.ExecContext(ctx, `
insert into known_place(id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)`, definition.id, definition.labelKind, definition.displayName, definition.latitude, definition.longitude, definition.radiusMetres, definition.validFrom, definition.validUntil, definition.updatedAt); err != nil {
					return err
				}
			}
		}
		for _, assetID := range sortedMapKeys(plan.knownPlaceOutcomes) {
			if err := upsertProtoWhenIdentityChanged(ctx, transaction, "configured_known_place_match_outcome", assetID, plan.knownPlaceOutcomes[assetID], definitionsCurrent, knownPlaceRequestIdentityMatches); err != nil {
				return err
			}
		}
		for _, assetID := range sortedMapKeys(plan.appleReverseOutcomes) {
			if err := upsertProtoWhenIdentityChanged(ctx, transaction, "apple_reverse_geocoding_evidence_outcome", assetID, plan.appleReverseOutcomes[assetID], true, appleReverseRequestIdentityMatches); err != nil {
				return err
			}
		}
		for _, assetID := range sortedMapKeys(plan.geoapifyReverse) {
			if err := upsertProtoWhenIdentityChanged(ctx, transaction, "geoapify_reverse_geocoding_evidence_outcome", assetID, plan.geoapifyReverse[assetID], true, geoapifyReverseRequestIdentityMatches); err != nil {
				return err
			}
		}
		for _, assetID := range sortedMapKeys(plan.geoapifyNearby) {
			if err := upsertProtoWhenIdentityChanged(ctx, transaction, "geoapify_nearby_place_evidence_outcome", assetID, plan.geoapifyNearby[assetID], true, geoapifyNearbyRequestIdentityMatches); err != nil {
				return err
			}
		}
		return nil
	})
}

func withTransaction(ctx context.Context, database *sql.DB, operation func(*sql.Tx) error) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := operation(transaction); err != nil {
		return errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit validated retained location evidence: %w", err)
	}
	return nil
}

func targetKnownPlaceDefinitionsAreCurrentInTransaction(ctx context.Context, transaction *sql.Tx, wanted []*knownPlaceDefinition) (bool, int, error) {
	rows, err := transaction.QueryContext(ctx, `select id, label_kind, display_name, latitude, longitude, radius_meters, valid_from, valid_until, updated_at from known_place order by id`)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = rows.Close() }()
	existing := make([]knownPlaceDefinition, 0, len(wanted))
	for rows.Next() {
		var definition knownPlaceDefinition
		if err := rows.Scan(&definition.id, &definition.labelKind, &definition.displayName, &definition.latitude, &definition.longitude, &definition.radiusMetres, &definition.validFrom, &definition.validUntil, &definition.updatedAt); err != nil {
			return false, 0, err
		}
		existing = append(existing, definition)
	}
	if err := rows.Err(); err != nil || len(existing) != len(wanted) {
		return false, len(existing), err
	}
	for index := range existing {
		if existing[index] != *wanted[index] {
			return false, len(existing), nil
		}
	}
	return true, len(existing), nil
}

type requestIdentityMatcher func(existingEncoded []byte, wanted proto.Message) (bool, error)

func upsertProtoWhenIdentityChanged(ctx context.Context, transaction *sql.Tx, tableName, assetID string, wanted proto.Message, existingIdentityMayBeReused bool, identityMatches requestIdentityMatcher) error {
	var existingEncoded []byte
	err := transaction.QueryRowContext(ctx, `select outcome_proto from `+tableName+` where asset_id=?`, assetID).Scan(&existingEncoded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existingIdentityMayBeReused {
		matches, err := identityMatches(existingEncoded, wanted)
		if err != nil {
			return err
		}
		if matches {
			return nil
		}
	}
	encoded, err := proto.Marshal(wanted)
	if err != nil {
		return err
	}
	_, err = transaction.ExecContext(ctx, `insert into `+tableName+`(asset_id, outcome_proto) values (?, ?) on conflict(asset_id) do update set outcome_proto=excluded.outcome_proto`, assetID, encoded)
	return err
}

func knownPlaceRequestIdentityMatches(encoded []byte, wanted proto.Message) (bool, error) {
	existing := new(locationwire.MatchConfiguredKnownPlaceOutcome)
	if err := proto.Unmarshal(encoded, existing); err != nil {
		return false, err
	}
	return knownPlaceOutcomeSatisfiesCurrentRequest(existing, wanted.(*locationwire.MatchConfiguredKnownPlaceOutcome).GetRequest()), nil
}

func appleReverseRequestIdentityMatches(encoded []byte, wanted proto.Message) (bool, error) {
	existing := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
	if err := proto.Unmarshal(encoded, existing); err != nil {
		return false, err
	}
	return proto.Equal(existing.GetRequest(), wanted.(*locationwire.AcquireAppleReverseGeocodingEvidenceOutcome).GetRequest()) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), false), nil
}

func geoapifyReverseRequestIdentityMatches(encoded []byte, wanted proto.Message) (bool, error) {
	existing := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	if err := proto.Unmarshal(encoded, existing); err != nil {
		return false, err
	}
	return proto.Equal(existing.GetRequest(), wanted.(*locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome).GetRequest()) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), false), nil
}

func geoapifyNearbyRequestIdentityMatches(encoded []byte, wanted proto.Message) (bool, error) {
	existing := new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
	if err := proto.Unmarshal(encoded, existing); err != nil {
		return false, err
	}
	return proto.Equal(existing.GetRequest(), wanted.(*locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome).GetRequest()) && place.ProviderExchangeSatisfiesCurrentLocationEvidence(existing.GetExchange(), true), nil
}

func knownPlaceOutcomeSatisfiesCurrentRequest(outcome *locationwire.MatchConfiguredKnownPlaceOutcome, request *locationwire.MatchConfiguredKnownPlaceRequest) bool {
	if outcome == nil || !proto.Equal(outcome.GetRequest(), request) || outcome.GetFailure() != nil {
		return false
	}
	if len(outcome.GetMatches()) > 0 {
		return outcome.GetState() == locationwire.OperationState_OPERATION_STATE_SUCCEEDED
	}
	return outcome.GetState() == locationwire.OperationState_OPERATION_STATE_NO_RESULT
}

func printOutcomeChangePlan(name string, changes plannedOutcomeChanges) {
	fmt.Printf("%s target plan: %d inserts, %d replacements (%d different request, %d insufficient terminal outcome), %d unchanged, %d unrelated existing rows retained.\n", name, changes.inserts, changes.replacements, changes.replacedDifferentRequest, changes.replacedInsufficientOutcome, changes.unchanged, changes.untouchedExisting)
}

func printImportSummary(apply bool, plan *importPlan, writes requiredTargetWrites) {
	mode := "dry run"
	if apply {
		mode = "approved apply"
	}
	knownMatches := 0
	for _, outcome := range plan.knownPlaceOutcomes {
		if len(outcome.GetMatches()) > 0 {
			knownMatches++
		}
	}
	geoapifyNearbyNetworkEvidence := 0
	geoapifyNearbyKnownPlaceSkips := 0
	for _, outcome := range plan.geoapifyNearby {
		switch outcome.GetExchange().GetState() {
		case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
			geoapifyNearbyKnownPlaceSkips++
		case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
			geoapifyNearbyNetworkEvidence++
		}
	}
	fmt.Printf("Retained Photos location evidence — %s\n", mode)
	fmt.Printf("Known places: %d definitions; %d current capture inputs recomputed; %d matched. Legacy match observations imported: 0.\n", len(plan.knownPlaceDefinitions), len(plan.knownPlaceOutcomes), knownMatches)
	fmt.Printf("Apple reverse geocoding: %d retained outputs inspected; %d current-coordinate outcomes accepted (%d exact values and %d values differing only by at most four representable float64 steps); %d changed coordinates discarded.\n", plan.appleOutputFiles, len(plan.appleReverseOutcomes), len(plan.appleReverseOutcomes)-plan.appleFloatingPointRoundTrips, plan.appleFloatingPointRoundTrips, plan.appleCoordinateMismatches)
	fmt.Printf("Apple nearby places: 0 imported. Legacy 150 metre evidence does not satisfy the current 500 metre request.\n")
	fmt.Printf("Geoapify proof evidence: %d unique reverse outcomes and %d unique nearby outcomes have a current full typed request identity; nearby contains %d retained network outcomes and %d recomputed known-place skips.\n", len(plan.geoapifyReverse), len(plan.geoapifyNearby), geoapifyNearbyNetworkEvidence, geoapifyNearbyKnownPlaceSkips)
	fmt.Printf("Legacy judgements discarded: %d venue rows and %d tier labels. Failed attempts and old confidence/candidate decisions imported: 0.\n", plan.discardedVenueRows, plan.discardedTierJudgements)
	fmt.Printf("Known-place definition target plan: %d inserts; conflicting existing definitions: 0.\n", writes.knownPlaceDefinitions)
	printOutcomeChangePlan("Known-place outcomes", writes.knownPlaceOutcomes)
	printOutcomeChangePlan("Apple reverse outcomes", writes.appleReverseOutcomes)
	printOutcomeChangePlan("Geoapify reverse outcomes", writes.geoapifyReverse)
	printOutcomeChangePlan("Geoapify nearby outcomes", writes.geoapifyNearby)
	fmt.Printf("Target writes required: %d rows; deletions: 0; conflicts: 0.\n", writes.knownPlaceDefinitions+writes.knownPlaceOutcomes.writes()+writes.appleReverseOutcomes.writes()+writes.geoapifyReverse.writes()+writes.geoapifyNearby.writes())
}
