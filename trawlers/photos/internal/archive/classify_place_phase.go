package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	classifyQueueStatePending            = "pending"
	classifyQueueStateMetadataClassified = "metadata_classified"
	classifyQueueStateContentClassified  = "content_classified"
	classifyQueueStatePlacePending       = "place_pending"

	classifyPlacePendingReason        = "place_pending: apple geocoder unavailable"
	classifyPlaceUnparkedReason       = "place context cached"
	classifyPlacePendingKeyLimit      = 200
	classifyPlaceProviderStartSpacing = 250 * time.Millisecond
)

type classifyPlaceResolver struct {
	key             func(place.Input) string
	resolveCached   func(context.Context, place.Input) place.ResolveResult
	resolveProvider func(context.Context, place.Input) place.ResolveResult
	sleep           func(context.Context, time.Duration) error
}

type classifyPlaceKeyState struct {
	key            string
	representative place.Input
	pending        []classifyInput
	resolved       *classifyPlaceContext
}

func prepareClassifyPlaces(ctx context.Context, db *store.Store, paths Paths, inputs []classifyInput, now func() time.Time, result *ClassifyResult, logger classifyLogger) ([]classifyInput, error) {
	knownPlaces, err := loadKnownPlacesForClassify(ctx, db)
	if err != nil {
		return nil, err
	}
	resolver := newClassifyPlaceResolver(paths)
	pending, err := loadPlacePendingInputs(ctx, db, resolver, classifyPlacePendingKeyLimit)
	if err != nil {
		return nil, err
	}
	return resolveClassifyPlaces(ctx, db, inputs, pending, knownPlaces, resolver, now, result, logger)
}

func newClassifyPlaceResolver(paths Paths) classifyPlaceResolver {
	resolver := place.NewResolver(place.ResolverOptions{
		CacheDir:           paths.PlaceContextCacheDir(),
		BackfillDir:        paths.PlaceBackfillDir(),
		RadiusMeters:       150,
		ProviderStartEvery: classifyPlaceProviderStartSpacing,
	})
	return classifyPlaceResolver{
		key:             resolver.Key,
		resolveCached:   resolver.ResolveCached,
		resolveProvider: resolver.ResolveProvider,
		sleep:           sleepClassifyPlace,
	}
}

func loadKnownPlacesForClassify(ctx context.Context, db *store.Store) ([]KnownPlace, error) {
	return loadKnownPlaces(ctx, db.DB())
}

func resolveClassifyPlaces(ctx context.Context, db *store.Store, inputs []classifyInput, pending []classifyInput, knownPlaces []KnownPlace, resolver classifyPlaceResolver, now func() time.Time, result *ClassifyResult, logger classifyLogger) ([]classifyInput, error) {
	states, order := collectClassifyPlaceKeys(inputs, pending, knownPlaces, resolver)
	liveStopped := false
	for _, key := range order {
		state := states[key]
		if state == nil {
			continue
		}
		resolved, stop, err := resolveClassifyPlaceKey(ctx, state, resolver, &liveStopped, result, logger)
		if err != nil {
			return nil, err
		}
		if resolved != nil {
			state.resolved = resolved
		}
		if stop {
			liveStopped = true
		}
	}

	ready := make([]classifyInput, 0, len(inputs))
	parked := []classifyInput{}
	unparked := []classifyInput{}
	for i := range inputs {
		if !inputs[i].HasLocation {
			ready = append(ready, inputs[i])
			continue
		}
		key := resolver.Key(classifyPlaceInput(inputs[i]))
		state := states[key]
		if state != nil && state.resolved != nil {
			inputs[i].Place = state.resolved
			ready = append(ready, inputs[i])
			continue
		}
		parked = append(parked, inputs[i])
	}
	for _, key := range order {
		state := states[key]
		if state == nil || state.resolved == nil {
			continue
		}
		unparked = append(unparked, state.pending...)
	}
	if err := updateClassifyPlaceQueue(ctx, db, parked, unparked, now().UTC()); err != nil {
		return nil, err
	}
	for _, input := range parked {
		logger.logPlaceParked(input, classifyPlacePendingReason)
	}
	for _, input := range unparked {
		logger.logPlaceUnparked(input, classifyPlaceUnparkedReason)
	}
	return ready, nil
}

func collectClassifyPlaceKeys(inputs []classifyInput, pending []classifyInput, knownPlaces []KnownPlace, resolver classifyPlaceResolver) (map[string]*classifyPlaceKeyState, []string) {
	states := map[string]*classifyPlaceKeyState{}
	order := []string{}
	add := func(input classifyInput) (string, *classifyPlaceKeyState) {
		placeInput := classifyPlaceInput(input)
		key := resolver.Key(placeInput)
		if key == "" {
			return "", nil
		}
		state := states[key]
		if state == nil {
			state = &classifyPlaceKeyState{key: key, representative: placeInput}
			states[key] = state
			order = append(order, key)
		}
		return key, state
	}
	for i := range inputs {
		if !inputs[i].HasLocation {
			continue
		}
		inputs[i].KnownPlace = matchKnownPlace(knownPlaces, inputs[i].Latitude, inputs[i].Longitude, inputs[i].CreationDate)
		add(inputs[i])
	}
	for _, input := range pending {
		if _, state := add(input); state != nil {
			state.pending = append(state.pending, input)
		}
	}
	return states, order
}

func resolveClassifyPlaceKey(ctx context.Context, state *classifyPlaceKeyState, resolver classifyPlaceResolver, liveStopped *bool, result *ClassifyResult, logger classifyLogger) (*classifyPlaceContext, bool, error) {
	cached := resolver.ResolveCached(ctx, state.representative)
	switch cached.CacheStatus {
	case "hit":
		result.PlaceCacheHits++
	case "backfill_hit":
		result.PlaceCacheHits++
		result.PlaceBackfillHits++
	}
	if cached.Result != nil {
		return classifyPlaceContextFromResolve(cached), false, nil
	}
	if liveStopped != nil && *liveStopped {
		return nil, false, nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		startedAt := time.Now()
		resolved := resolver.ResolveProvider(ctx, state.representative)
		duration := time.Since(startedAt)
		if resolved.ProviderAttempt {
			result.PlaceProviderAttempts++
		}
		if resolved.Result != nil {
			reason := ""
			if resolved.Result.Address == nil {
				reason = place.NoPlacemarkReason
			}
			logger.logPlaceGeocode(state.key, "ok", duration, reason)
			return classifyPlaceContextFromResolve(resolved), false, nil
		}
		result.PlaceProviderFailures++
		reason := strings.TrimSpace(resolved.ProviderError)
		if reason == "" {
			reason = "apple geocoder returned no place context"
		}
		switch {
		case errors.Is(resolved.ProviderErr, place.ErrProviderThrottled):
			logger.logPlaceGeocode(state.key, "throttled", duration, "apple geocoder throttled")
			if attempt == 3 {
				return nil, true, nil
			}
			if err := resolver.Sleep(ctx, classifyPlaceThrottleBackoff(attempt)); err != nil {
				return nil, false, err
			}
			continue
		case errors.Is(resolved.ProviderErr, place.ErrProviderTimeout):
			// Timeouts are Apple tarpitting rather than fast-rejecting.
			// Retrying other keys just burns 20s each: stop live geocoding
			// for this run and park the rest.
			logger.logPlaceGeocode(state.key, "timeout", duration, reason)
			return nil, true, nil
		}
		logger.logPlaceGeocode(state.key, "error", duration, reason)
		return nil, false, nil
	}
	return nil, true, nil
}

func classifyPlaceContextFromResolve(resolved place.ResolveResult) *classifyPlaceContext {
	if resolved.Result == nil {
		return nil
	}
	result := *resolved.Result
	place.NormalizeResult(&result)
	cacheStatus := strings.TrimSpace(resolved.CacheStatus)
	if cacheStatus == "" {
		cacheStatus = strings.TrimSpace(result.CacheStatus)
	}
	return &classifyPlaceContext{
		Result:      result,
		CacheStatus: cacheStatus,
	}
}

func loadPlacePendingInputs(ctx context.Context, db *store.Store, resolver classifyPlaceResolver, maxKeys int) ([]classifyInput, error) {
	if maxKeys <= 0 {
		return nil, nil
	}
	var inputs []classifyInput
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
select q.id, q.asset_id, q.source_library_id, a.local_identifier, q.needs_download,
       a.creation_date, lo.latitude, lo.longitude, coalesce(lo.horizontal_accuracy, 0)
from classification_queue q
join asset a on a.id = q.asset_id
join location_observation lo on lo.id = (
  select id
  from location_observation
  where asset_id = a.id
  order by id
  limit 1
)
where q.state = ?
order by q.updated_at, a.creation_date desc, q.id
`, classifyQueueStatePlacePending)
		if err != nil {
			return fmt.Errorf("load place pending queue: %w", err)
		}
		defer func() { _ = rows.Close() }()
		seenKeys := map[string]bool{}
		for rows.Next() {
			var input classifyInput
			var needsDownload int
			if err := rows.Scan(
				&input.QueueID,
				&input.AssetID,
				&input.SourceLibraryID,
				&input.LocalIdentifier,
				&needsDownload,
				&input.CreationDate,
				&input.Latitude,
				&input.Longitude,
				&input.AccuracyMeters,
			); err != nil {
				return err
			}
			input.NeedsDownload = needsDownload != 0
			input.HasLocation = true
			key := resolver.Key(classifyPlaceInput(input))
			if key == "" {
				continue
			}
			if !seenKeys[key] {
				if len(seenKeys) >= maxKeys {
					continue
				}
				seenKeys[key] = true
			}
			inputs = append(inputs, input)
		}
		return rows.Err()
	})
	return inputs, err
}

func updateClassifyPlaceQueue(ctx context.Context, db *store.Store, parked []classifyInput, unparked []classifyInput, updatedAt time.Time) error {
	if len(parked) == 0 && len(unparked) == 0 {
		return nil
	}
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, input := range parked {
			if err := updateClassificationQueue(ctx, tx, input.QueueID, classifyQueueStatePlacePending, classifyPlacePendingReason, updatedAt); err != nil {
				return err
			}
		}
		for _, input := range unparked {
			if err := updateClassificationQueue(ctx, tx, input.QueueID, classifyQueueStateMetadataClassified, classifyPlaceUnparkedReason, updatedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func (resolver classifyPlaceResolver) Key(input place.Input) string {
	if resolver.key == nil {
		return ""
	}
	return resolver.key(input)
}

func (resolver classifyPlaceResolver) ResolveCached(ctx context.Context, input place.Input) place.ResolveResult {
	if resolver.resolveCached == nil {
		return place.ResolveResult{CacheStatus: "disabled"}
	}
	return resolver.resolveCached(ctx, input)
}

func (resolver classifyPlaceResolver) ResolveProvider(ctx context.Context, input place.Input) place.ResolveResult {
	if resolver.resolveProvider == nil {
		return place.ResolveResult{CacheStatus: "disabled", ProviderError: "place provider disabled"}
	}
	return resolver.resolveProvider(ctx, input)
}

func (resolver classifyPlaceResolver) Sleep(ctx context.Context, duration time.Duration) error {
	if resolver.sleep == nil {
		return sleepClassifyPlace(ctx, duration)
	}
	return resolver.sleep(ctx, duration)
}

func classifyPlaceInput(input classifyInput) place.Input {
	return place.Input{
		AssetID: input.AssetID,
		TakenAt: input.CreationDate,
		Location: place.Coordinate{
			Latitude:  input.Latitude,
			Longitude: input.Longitude,
		},
		AccuracyMeters: input.AccuracyMeters,
	}
}

func classifyPlaceThrottleBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 2 * time.Second
	case 2:
		return 10 * time.Second
	default:
		return 60 * time.Second
	}
}

func sleepClassifyPlace(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
