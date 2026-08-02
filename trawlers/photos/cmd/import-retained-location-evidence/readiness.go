package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type measuredBytes struct{ values []int }

func (m *measuredBytes) add(value int) {
	if value > 0 {
		m.values = append(m.values, value)
	}
}

func (m measuredBytes) summary() string {
	if len(m.values) == 0 {
		return "n=0"
	}
	values := append([]int(nil), m.values...)
	sort.Ints(values)
	total := 0
	for _, value := range values {
		total += value
	}
	p95Index := int(math.Ceil(float64(len(values))*0.95)) - 1
	return fmt.Sprintf("n=%d min=%d avg=%.1f p95=%d max=%d", len(values), values[0], float64(total)/float64(len(values)), values[p95Index], values[len(values)-1])
}

func (m measuredBytes) average() float64 {
	if len(m.values) == 0 {
		return 0
	}
	total := 0
	for _, value := range m.values {
		total += value
	}
	return float64(total) / float64(len(m.values))
}

func (m measuredBytes) p95() float64 {
	if len(m.values) == 0 {
		return 0
	}
	values := append([]int(nil), m.values...)
	sort.Ints(values)
	return float64(values[int(math.Ceil(float64(len(values))*0.95))-1])
}

type providerReadiness struct {
	name                    string
	rows                    int
	sufficient              int
	succeeded               int
	noResult                int
	knownPlaceSkip          int
	failedCurrentRequest    int
	otherIncompleteCurrent  int
	staleRequest            int
	absent                  int
	networkCallsNeeded      int
	knownPlaceSkipsNeeded   int
	storedSucceeded         int
	storedNoResult          int
	storedFailed            int
	storedKnownPlaceSkip    int
	storedIncomplete        int
	outcomeBytes            measuredBytes
	requestBytes            measuredBytes
	responseBytes           measuredBytes
	simulatedKnownSkipBytes measuredBytes
}

func (readiness providerReadiness) missingOutcomes() int {
	return readiness.absent + readiness.staleRequest + readiness.failedCurrentRequest + readiness.otherIncompleteCurrent
}

func (readiness providerReadiness) print() {
	fmt.Printf("%s: rows=%d sufficient=%d (success=%d no_result=%d known_place_skip=%d) missing=%d [absent=%d stale=%d failed=%d incomplete=%d] network_calls_needed=%d local_known_skips_needed=%d\n",
		readiness.name, readiness.rows, readiness.sufficient, readiness.succeeded, readiness.noResult, readiness.knownPlaceSkip,
		readiness.missingOutcomes(), readiness.absent, readiness.staleRequest, readiness.failedCurrentRequest, readiness.otherIncompleteCurrent,
		readiness.networkCallsNeeded, readiness.knownPlaceSkipsNeeded)
	fmt.Printf("%s stored states: success=%d no_result=%d failed=%d known_place_skip=%d incomplete=%d\n", readiness.name, readiness.storedSucceeded, readiness.storedNoResult, readiness.storedFailed, readiness.storedKnownPlaceSkip, readiness.storedIncomplete)
	fmt.Printf("%s sufficient outcome_proto bytes: %s\n", readiness.name, readiness.outcomeBytes.summary())
	fmt.Printf("%s sufficient request_proto bytes: %s\n", readiness.name, readiness.requestBytes.summary())
	fmt.Printf("%s retained response bytes: %s\n", readiness.name, readiness.responseBytes.summary())
	if len(readiness.simulatedKnownSkipBytes.values) > 0 {
		fmt.Printf("%s current known-place skip outcome bytes: %s\n", readiness.name, readiness.simulatedKnownSkipBytes.summary())
	}
}

func (readiness *providerReadiness) recordStoredState(exchange *locationwire.ProviderExchange) {
	switch exchange.GetState() {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED:
		readiness.storedSucceeded++
	case locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		readiness.storedNoResult++
	case locationwire.OperationState_OPERATION_STATE_FAILED:
		readiness.storedFailed++
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		readiness.storedKnownPlaceSkip++
	default:
		readiness.storedIncomplete++
	}
}

func measureBackfillReadiness(ctx context.Context, archivePath string) error {
	openedStore, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = openedStore.Close() }()
	database := openedStore.DB()
	var assetCount int
	if err := database.QueryRowContext(ctx, `select count(*) from asset`).Scan(&assetCount); err != nil {
		return err
	}
	_, allCapturesByAssetID, err := loadTargetCaptureIdentities(ctx, database)
	if err != nil {
		return err
	}
	var stillImageCount int
	if err := database.QueryRowContext(ctx, `select count(*) from asset where media_type='image'`).Scan(&stillImageCount); err != nil {
		return err
	}
	capturesByAssetID, err := retainStillImageCaptures(ctx, database, allCapturesByAssetID)
	if err != nil {
		return err
	}
	configurationSHA256, err := archive.KnownPlaceConfigurationSHA256(ctx, openedStore)
	if err != nil {
		return err
	}
	knownOutcomes, knownSufficient, knownFailed, knownStale, err := loadEffectiveKnownPlaceOutcomes(ctx, openedStore, capturesByAssetID, configurationSHA256)
	if err != nil {
		return err
	}
	knownMatches := 0
	for _, outcome := range knownOutcomes {
		if len(outcome.GetMatches()) > 0 {
			knownMatches++
		}
	}
	fmt.Printf("Source corpus: assets=%d gps_eligible=%d no_gps=%d\n", assetCount, len(allCapturesByAssetID), assetCount-len(allCapturesByAssetID))
	fmt.Printf("Backfill-eligible still images: assets=%d gps_eligible=%d no_gps=%d; excluded videos=%d located_videos=%d\n", stillImageCount, len(capturesByAssetID), stillImageCount-len(capturesByAssetID), assetCount-stillImageCount, len(allCapturesByAssetID)-len(capturesByAssetID))
	fmt.Printf("Known places: sufficient=%d matched=%d unmatched=%d failed_current=%d stale_or_missing=%d\n", knownSufficient, knownMatches, len(capturesByAssetID)-knownMatches, knownFailed, knownStale)

	appleReverse, err := measureAppleReverse(ctx, database, capturesByAssetID)
	if err != nil {
		return err
	}
	appleNearby, err := measureAppleNearby(ctx, database, capturesByAssetID, knownOutcomes)
	if err != nil {
		return err
	}
	geoapifyReverse, err := measureGeoapifyReverse(ctx, database, capturesByAssetID)
	if err != nil {
		return err
	}
	geoapifyNearby, err := measureGeoapifyNearby(ctx, database, capturesByAssetID, knownOutcomes)
	if err != nil {
		return err
	}
	for _, readiness := range []providerReadiness{appleReverse, appleNearby, geoapifyReverse, geoapifyNearby} {
		readiness.print()
	}

	if err := printDerivedAndCardMeasurements(ctx, database, stillImageCount); err != nil {
		return err
	}
	if err := printAttemptDurations(ctx, database); err != nil {
		return err
	}
	return printMeasuredArchiveEstimate(ctx, database, archivePath, stillImageCount, len(capturesByAssetID), appleReverse, appleNearby, geoapifyReverse, geoapifyNearby)
}

func retainStillImageCaptures(ctx context.Context, database *sql.DB, allCaptures map[string]*targetCaptureIdentity) (map[string]*targetCaptureIdentity, error) {
	rows, err := database.QueryContext(ctx, `select id from asset where media_type='image'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	images := make(map[string]*targetCaptureIdentity)
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return nil, err
		}
		if capture, found := allCaptures[assetID]; found {
			images[assetID] = capture
		}
	}
	return images, rows.Err()
}

func loadEffectiveKnownPlaceOutcomes(ctx context.Context, openedStore *store.Store, captures map[string]*targetCaptureIdentity, configurationSHA256 []byte) (map[string]*locationwire.MatchConfiguredKnownPlaceOutcome, int, int, int, error) {
	outcomes := make(map[string]*locationwire.MatchConfiguredKnownPlaceOutcome, len(captures))
	sufficient, failed, staleOrMissing := 0, 0, 0
	for assetID, capture := range captures {
		request := &locationwire.MatchConfiguredKnownPlaceRequest{Input: captureLocationInput(capture), KnownPlaceConfigurationSha256: configurationSHA256}
		stored, found, err := archive.LoadMatchConfiguredKnownPlaceOutcome(ctx, openedStore, assetID)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		if found && knownPlaceOutcomeSatisfiesCurrentRequest(stored, request) {
			outcomes[assetID] = stored
			sufficient++
			continue
		}
		if found && proto.Equal(stored.GetRequest(), request) && stored.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED {
			failed++
		} else {
			staleOrMissing++
		}
		computed, err := archive.MatchConfiguredKnownPlace(ctx, openedStore, request)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		outcomes[assetID] = computed
	}
	return outcomes, sufficient, failed, staleOrMissing, nil
}

func loadTypedOutcomes[M proto.Message](ctx context.Context, database *sql.DB, tableName string, newMessage func() M) (map[string]M, error) {
	rows, err := database.QueryContext(ctx, `select asset_id, outcome_proto from `+tableName)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	outcomes := make(map[string]M)
	for rows.Next() {
		var assetID string
		var encoded []byte
		if err := rows.Scan(&assetID, &encoded); err != nil {
			return nil, err
		}
		outcome := newMessage()
		if err := proto.Unmarshal(encoded, outcome); err != nil {
			return nil, err
		}
		outcomes[assetID] = outcome
	}
	return outcomes, rows.Err()
}

func classifyProviderOutcome(readiness *providerReadiness, sameRequest bool, exchange *locationwire.ProviderExchange, encodedOutcome, encodedRequest []byte, allowKnownPlaceSkip bool) {
	if !sameRequest {
		readiness.staleRequest++
		return
	}
	if !place.ProviderExchangeSatisfiesCurrentLocationEvidence(exchange, allowKnownPlaceSkip) {
		if exchange.GetState() == locationwire.OperationState_OPERATION_STATE_FAILED {
			readiness.failedCurrentRequest++
		} else {
			readiness.otherIncompleteCurrent++
		}
		return
	}
	readiness.sufficient++
	switch exchange.GetState() {
	case locationwire.OperationState_OPERATION_STATE_SUCCEEDED:
		readiness.succeeded++
	case locationwire.OperationState_OPERATION_STATE_NO_RESULT:
		readiness.noResult++
	case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
		readiness.knownPlaceSkip++
	}
	readiness.outcomeBytes.add(len(encodedOutcome))
	readiness.requestBytes.add(len(encodedRequest))
	readiness.responseBytes.add(len(exchange.GetExactResponse()))
}

func measureAppleReverse(ctx context.Context, database *sql.DB, captures map[string]*targetCaptureIdentity) (providerReadiness, error) {
	readiness := providerReadiness{name: "Apple reverse"}
	rows, err := loadTypedOutcomes(ctx, database, "apple_reverse_geocoding_evidence_outcome", func() *locationwire.AcquireAppleReverseGeocodingEvidenceOutcome {
		return new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
	})
	if err != nil {
		return readiness, err
	}
	for assetID, outcome := range rows {
		if _, relevant := captures[assetID]; relevant {
			readiness.rows++
			readiness.recordStoredState(outcome.GetExchange())
		}
	}
	for assetID, capture := range captures {
		expected := &locationwire.AcquireAppleReverseGeocodingEvidenceRequest{Input: captureLocationInput(capture)}
		outcome, found := rows[assetID]
		if !found {
			readiness.absent++
			continue
		}
		encodedOutcome, _ := proto.Marshal(outcome)
		encodedRequest, _ := proto.Marshal(expected)
		classifyProviderOutcome(&readiness, proto.Equal(outcome.GetRequest(), expected), outcome.GetExchange(), encodedOutcome, encodedRequest, false)
	}
	readiness.networkCallsNeeded = readiness.missingOutcomes()
	return readiness, nil
}

func measureAppleNearby(ctx context.Context, database *sql.DB, captures map[string]*targetCaptureIdentity, known map[string]*locationwire.MatchConfiguredKnownPlaceOutcome) (providerReadiness, error) {
	readiness := providerReadiness{name: "Apple nearby"}
	rows, err := loadTypedOutcomes(ctx, database, "apple_nearby_place_evidence_outcome", func() *locationwire.AcquireAppleNearbyPlaceEvidenceOutcome {
		return new(locationwire.AcquireAppleNearbyPlaceEvidenceOutcome)
	})
	if err != nil {
		return readiness, err
	}
	for assetID, outcome := range rows {
		if _, relevant := captures[assetID]; relevant {
			readiness.rows++
			readiness.recordStoredState(outcome.GetExchange())
		}
	}
	for assetID, capture := range captures {
		expected := &locationwire.AcquireAppleNearbyPlaceEvidenceRequest{Input: captureLocationInput(capture), RadiusMeters: currentNearbyPlaceRadiusMetres, MaximumCandidates: currentMaximumNearbyPlaceCandidates, KnownPlaceOutcome: known[assetID]}
		outcome, found := rows[assetID]
		if !found {
			readiness.absent++
		} else {
			encodedOutcome, _ := proto.Marshal(outcome)
			encodedRequest, _ := proto.Marshal(expected)
			classifyProviderOutcome(&readiness, proto.Equal(outcome.GetRequest(), expected), outcome.GetExchange(), encodedOutcome, encodedRequest, true)
		}
		if len(known[assetID].GetMatches()) > 0 {
			if !found || !proto.Equal(outcome.GetRequest(), expected) || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
				readiness.knownPlaceSkipsNeeded++
			}
			skip := &locationwire.AcquireAppleNearbyPlaceEvidenceOutcome{Request: expected, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE}, CompletedAt: known[assetID].GetCompletedAt()}
			encoded, _ := proto.Marshal(skip)
			readiness.simulatedKnownSkipBytes.add(len(encoded))
		} else if !found || !proto.Equal(outcome.GetRequest(), expected) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(outcome.GetExchange(), true) {
			readiness.networkCallsNeeded++
		}
	}
	return readiness, nil
}

func measureGeoapifyReverse(ctx context.Context, database *sql.DB, captures map[string]*targetCaptureIdentity) (providerReadiness, error) {
	readiness := providerReadiness{name: "Geoapify reverse"}
	rows, err := loadTypedOutcomes(ctx, database, "geoapify_reverse_geocoding_evidence_outcome", func() *locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome {
		return new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
	})
	if err != nil {
		return readiness, err
	}
	for assetID, outcome := range rows {
		if _, relevant := captures[assetID]; relevant {
			readiness.rows++
			readiness.recordStoredState(outcome.GetExchange())
		}
	}
	for assetID, capture := range captures {
		expected := &locationwire.AcquireGeoapifyReverseGeocodingEvidenceRequest{Input: captureLocationInput(capture)}
		outcome, found := rows[assetID]
		if !found {
			readiness.absent++
			continue
		}
		encodedOutcome, _ := proto.Marshal(outcome)
		encodedRequest, _ := proto.Marshal(expected)
		classifyProviderOutcome(&readiness, proto.Equal(outcome.GetRequest(), expected), outcome.GetExchange(), encodedOutcome, encodedRequest, false)
	}
	readiness.networkCallsNeeded = readiness.missingOutcomes()
	return readiness, nil
}

func measureGeoapifyNearby(ctx context.Context, database *sql.DB, captures map[string]*targetCaptureIdentity, known map[string]*locationwire.MatchConfiguredKnownPlaceOutcome) (providerReadiness, error) {
	readiness := providerReadiness{name: "Geoapify nearby"}
	rows, err := loadTypedOutcomes(ctx, database, "geoapify_nearby_place_evidence_outcome", func() *locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome {
		return new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
	})
	if err != nil {
		return readiness, err
	}
	for assetID, outcome := range rows {
		if _, relevant := captures[assetID]; relevant {
			readiness.rows++
			readiness.recordStoredState(outcome.GetExchange())
		}
	}
	for assetID, capture := range captures {
		expected := &locationwire.AcquireGeoapifyNearbyPlaceEvidenceRequest{Input: captureLocationInput(capture), RadiusMeters: currentNearbyPlaceRadiusMetres, MaximumCandidates: currentMaximumNearbyPlaceCandidates, KnownPlaceOutcome: known[assetID]}
		outcome, found := rows[assetID]
		if !found {
			readiness.absent++
		} else {
			encodedOutcome, _ := proto.Marshal(outcome)
			encodedRequest, _ := proto.Marshal(expected)
			classifyProviderOutcome(&readiness, proto.Equal(outcome.GetRequest(), expected), outcome.GetExchange(), encodedOutcome, encodedRequest, true)
		}
		if len(known[assetID].GetMatches()) > 0 {
			if !found || !proto.Equal(outcome.GetRequest(), expected) || outcome.GetExchange().GetState() != locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE {
				readiness.knownPlaceSkipsNeeded++
			}
			skip := &locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome{Request: expected, Exchange: &locationwire.ProviderExchange{State: locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE}, CompletedAt: known[assetID].GetCompletedAt()}
			encoded, _ := proto.Marshal(skip)
			readiness.simulatedKnownSkipBytes.add(len(encoded))
		} else if !found || !proto.Equal(outcome.GetRequest(), expected) || !place.ProviderExchangeSatisfiesCurrentLocationEvidence(outcome.GetExchange(), true) {
			readiness.networkCallsNeeded++
		}
	}
	return readiness, nil
}

func printDerivedAndCardMeasurements(ctx context.Context, database *sql.DB, assetCount int) error {
	countTables := []struct {
		name, table string
		showMissing bool
	}{
		{"composed_location", "composed_photo_location_evidence_outcome", false}, {"current_location", "current_photo_location_evidence", false}, {"current_media", "current_photo_media_evidence", true},
		{"card_generations", "photo_card_generation", true}, {"current_cards", "current_photo_card", true}, {"model_attempts", "photo_card_generation_transmission_attempt", false},
	}
	for _, item := range countTables {
		var count int
		if err := database.QueryRowContext(ctx, `select count(*) from `+item.table).Scan(&count); err != nil {
			return err
		}
		if item.showMissing {
			fmt.Printf("%s=%d missing_from_all_assets=%d\n", item.name, count, assetCount-count)
		} else {
			fmt.Printf("%s=%d\n", item.name, count)
		}
	}
	var currentCards int
	if err := database.QueryRowContext(ctx, `select count(*) from current_photo_card`).Scan(&currentCards); err != nil {
		return err
	}
	fmt.Printf("Luna classifications remaining upper_bound=%d; exact calls depend on current media acquisition because unavailable stills terminate without Luna.\n", assetCount-currentCards)
	var generationCompleted, generationFailed, generationWithResponse int
	if err := database.QueryRowContext(ctx, `select sum(completed_at is not null), sum(failure_text<>''), sum(response_body is not null and length(response_body)>0) from photo_card_generation`).Scan(&generationCompleted, &generationFailed, &generationWithResponse); err != nil {
		return err
	}
	fmt.Printf("PhotoCard generations: completed=%d failed=%d with_retained_response=%d\n", generationCompleted, generationFailed, generationWithResponse)
	measurements := []struct{ name, query string }{
		{"Composed location outcome_proto", `select length(outcome_proto) from composed_photo_location_evidence_outcome`},
		{"Current location outcome_proto", `select length(outcome_proto) from current_photo_location_evidence`},
		{"Media immutable facts proto", `select length(immutable_original_facts_proto) from current_photo_media_evidence`},
		{"PhotoCard generation request", `select length(cast(request_text as blob)) from photo_card_generation where request_text<>''`},
		{"PhotoCard generation response", `select length(response_body) from photo_card_generation where response_body is not null and length(response_body)>0`},
		{"PhotoCard repair request", `select length(cast(descriptions_repair_request_text as blob)) from photo_card_generation where descriptions_repair_request_text<>''`},
		{"PhotoCard repair response", `select length(descriptions_repair_response_body) from photo_card_generation where descriptions_repair_response_body is not null and length(descriptions_repair_response_body)>0`},
		{"Current PhotoCard proto", `select length(card_proto) from current_photo_card`},
	}
	for _, measurement := range measurements {
		values, err := loadIntegerMetric(ctx, database, measurement.query)
		if err != nil {
			return err
		}
		fmt.Printf("%s bytes: %s\n", measurement.name, values.summary())
	}
	return nil
}

func loadIntegerMetric(ctx context.Context, database *sql.DB, query string) (measuredBytes, error) {
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return measuredBytes{}, err
	}
	defer func() { _ = rows.Close() }()
	values := measuredBytes{}
	for rows.Next() {
		var value int
		if err := rows.Scan(&value); err != nil {
			return values, err
		}
		values.add(value)
	}
	return values, rows.Err()
}

func printAttemptDurations(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `select provider_operation, transmission_started_at, completed_at from provider_location_transmission_attempt order by attempt_id`)
	if err != nil {
		return err
	}
	providerDurations := map[int]*measuredBytes{}
	providerTotals := map[int]int{}
	for rows.Next() {
		var operation int
		var started string
		var completed sql.NullString
		if err := rows.Scan(&operation, &started, &completed); err != nil {
			_ = rows.Close()
			return err
		}
		providerTotals[operation]++
		if !completed.Valid {
			continue
		}
		durationMilliseconds, err := elapsedMilliseconds(started, completed.String)
		if err != nil {
			_ = rows.Close()
			return err
		}
		if providerDurations[operation] == nil {
			providerDurations[operation] = new(measuredBytes)
		}
		providerDurations[operation].add(durationMilliseconds)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, operation := range []int{1, 2, 3, 4} {
		name := map[int]string{1: "Apple reverse", 2: "Apple nearby", 3: "Geoapify reverse", 4: "Geoapify nearby"}[operation]
		durations := measuredBytes{}
		if providerDurations[operation] != nil {
			durations = *providerDurations[operation]
		}
		fmt.Printf("%s retained attempts: total=%d completed=%d elapsed_ms=%s\n", name, providerTotals[operation], len(durations.values), durations.summary())
	}
	modelRows, err := database.QueryContext(ctx, `select transmission_started_at, completed_at from photo_card_generation_transmission_attempt order by attempt_id`)
	if err != nil {
		return err
	}
	defer func() { _ = modelRows.Close() }()
	modelTotal := 0
	modelDurations := measuredBytes{}
	for modelRows.Next() {
		var started string
		var completed sql.NullString
		if err := modelRows.Scan(&started, &completed); err != nil {
			return err
		}
		modelTotal++
		if completed.Valid {
			durationMilliseconds, err := elapsedMilliseconds(started, completed.String)
			if err != nil {
				return err
			}
			modelDurations.add(durationMilliseconds)
		}
	}
	fmt.Printf("PhotoCard retained model attempts: total=%d completed=%d elapsed_ms=%s\n", modelTotal, len(modelDurations.values), modelDurations.summary())
	return modelRows.Err()
}

func elapsedMilliseconds(startedText, completedText string) (int, error) {
	started, err := time.Parse(time.RFC3339Nano, startedText)
	if err != nil {
		return 0, err
	}
	completed, err := time.Parse(time.RFC3339Nano, completedText)
	if err != nil {
		return 0, err
	}
	return int(completed.Sub(started).Milliseconds()), nil
}

func printMeasuredArchiveEstimate(ctx context.Context, database *sql.DB, archivePath string, assetCount, gpsEligible int, providers ...providerReadiness) error {
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		return err
	}
	composedBytes, err := loadIntegerMetric(ctx, database, `select length(outcome_proto) from composed_photo_location_evidence_outcome`)
	if err != nil {
		return err
	}
	cardRequests, err := loadIntegerMetric(ctx, database, `select length(cast(request_text as blob)) from photo_card_generation where request_text<>''`)
	if err != nil {
		return err
	}
	cardResponses, err := loadIntegerMetric(ctx, database, `select length(response_body) from photo_card_generation where response_body is not null and length(response_body)>0`)
	if err != nil {
		return err
	}
	cardProtos, err := loadIntegerMetric(ctx, database, `select length(card_proto) from current_photo_card`)
	if err != nil {
		return err
	}
	repairRequests, err := loadIntegerMetric(ctx, database, `select length(cast(descriptions_repair_request_text as blob)) from photo_card_generation where descriptions_repair_request_text<>''`)
	if err != nil {
		return err
	}
	repairResponses, err := loadIntegerMetric(ctx, database, `select length(descriptions_repair_response_body) from photo_card_generation where descriptions_repair_response_body is not null and length(descriptions_repair_response_body)>0`)
	if err != nil {
		return err
	}
	mediaFacts, err := loadIntegerMetric(ctx, database, `select length(immutable_original_facts_proto) from current_photo_media_evidence`)
	if err != nil {
		return err
	}
	lowAdditional, highAdditional := float64(0), float64(0)
	for _, provider := range providers {
		lowAdditional += float64(provider.networkCallsNeeded) * provider.outcomeBytes.average()
		highAdditional += float64(provider.networkCallsNeeded) * provider.outcomeBytes.p95()
		lowAdditional += float64(provider.knownPlaceSkipsNeeded) * provider.simulatedKnownSkipBytes.average()
		highAdditional += float64(provider.knownPlaceSkipsNeeded) * provider.simulatedKnownSkipBytes.p95()
	}
	missingComposed := gpsEligible
	var currentComposed int
	if err := database.QueryRowContext(ctx, `select count(*) from current_photo_location_evidence`).Scan(&currentComposed); err != nil {
		return err
	}
	missingComposed -= currentComposed
	lowAdditional += float64(missingComposed) * composedBytes.average() * 2
	highAdditional += float64(missingComposed) * composedBytes.p95() * 2
	var currentCards int
	if err := database.QueryRowContext(ctx, `select count(*) from current_photo_card`).Scan(&currentCards); err != nil {
		return err
	}
	missingCards := assetCount - currentCards
	lowAdditional += float64(missingCards) * (cardRequests.average() + cardResponses.average() + cardProtos.average())
	highAdditional += float64(missingCards) * (cardRequests.p95() + cardResponses.p95() + cardProtos.p95())
	observedRepairRate := float64(0)
	if len(cardRequests.values) > 0 {
		observedRepairRate = float64(len(repairRequests.values)) / float64(len(cardRequests.values))
	}
	lowAdditional += float64(missingCards) * observedRepairRate * (repairRequests.average() + repairResponses.average())
	highAdditional += float64(missingCards) * observedRepairRate * (repairRequests.p95() + repairResponses.p95())
	var currentMedia int
	if err := database.QueryRowContext(ctx, `select count(*) from current_photo_media_evidence`).Scan(&currentMedia); err != nil {
		return err
	}
	missingMedia := assetCount - currentMedia
	lowAdditional += float64(missingMedia) * mediaFacts.average()
	highAdditional += float64(missingMedia) * mediaFacts.p95()
	lowFinal := float64(archiveInfo.Size()) + lowAdditional
	highFinal := float64(archiveInfo.Size()) + highAdditional
	fmt.Printf("Measured archive estimate: current_bytes=%d projected_bytes_low=%.0f projected_bytes_high=%.0f. Range adds measured average-to-p95 provider outcomes, two composed-location copies, media-facts protobufs, and retained PhotoCard request/response/card protobufs. It applies the observed descriptions-repair rate %.3f. SQLite page/index overhead beyond current allocation is not separately modelled.\n", archiveInfo.Size(), lowFinal, highFinal, observedRepairRate)
	fmt.Printf("Largest estimate uncertainty: only %d current cards, %d composed locations and 4-12 current nearby-provider outcomes exist; full-corpus PhotoCard and candidate/composition size distributions may differ materially.\n", currentCards, len(composedBytes.values))
	return nil
}
