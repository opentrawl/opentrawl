package health

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"google.golang.org/protobuf/proto"
)

const (
	workoutSourceKindHealthKitBatch = "healthkit_batch"
	workoutSourceKindAppleHealthXML = "apple_health_xml"
	workoutSourceKindHealthFitFIT   = "healthfit_fit"

	workoutStartToleranceSeconds = int64(120)
)

// normalizeWorkoutSportFamily maps Apple Health HKWorkoutActivityType names,
// HealthKit camelCase names, and FIT sport names onto one canonical comparison
// family so overlapping workouts from Apple Health and HealthFit match cleanly.
func normalizeWorkoutSportFamily(activityName string) string {
	raw := strings.TrimSpace(activityName)
	raw = strings.TrimPrefix(raw, "HKWorkoutActivityType")
	lower := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(raw))
	switch {
	case lower == "" || lower == "other" || lower == "generic" || lower == "workout":
		return "other"
	case strings.Contains(lower, "cycl") || strings.Contains(lower, "bik") || lower == "handcycling":
		return "cycling"
	case strings.Contains(lower, "run") || lower == "jogging":
		return "running"
	case strings.Contains(lower, "walk"):
		return "walking"
	case strings.Contains(lower, "swim"):
		return "swimming"
	case strings.Contains(lower, "hik") || strings.Contains(lower, "mountaineer"):
		return "hiking"
	case strings.Contains(lower, "row"):
		return "rowing"
	case strings.Contains(lower, "strength") || strings.Contains(lower, "weight") || lower == "training" || lower == "fitnessequipment":
		return "strength"
	case strings.Contains(lower, "yoga"):
		return "yoga"
	case strings.Contains(lower, "pilates"):
		return "pilates"
	case strings.Contains(lower, "hiit") || strings.Contains(lower, "highintensityinterval"):
		return "hiit"
	case strings.Contains(lower, "elliptical"):
		return "elliptical"
	case strings.Contains(lower, "stair"):
		return "stairs"
	case strings.Contains(lower, "ski") || strings.Contains(lower, "snowboard"):
		return "winter"
	case strings.Contains(lower, "tennis") || strings.Contains(lower, "badminton") || strings.Contains(lower, "squash") || strings.Contains(lower, "pickleball") || strings.Contains(lower, "padel"):
		return "racket"
	case strings.Contains(lower, "golf"):
		return "golf"
	case strings.Contains(lower, "climb"):
		return "climbing"
	case strings.Contains(lower, "paddle") || strings.Contains(lower, "kayak") || strings.Contains(lower, "surf"):
		return "watersport"
	case strings.Contains(lower, "core"):
		return "core"
	case strings.Contains(lower, "flexibility") || strings.Contains(lower, "cooldown") || strings.Contains(lower, "stretch"):
		return "mobility"
	default:
		return lower
	}
}

func workoutSportsAreCompatible(leftActivity, rightActivity string) bool {
	left := normalizeWorkoutSportFamily(leftActivity)
	right := normalizeWorkoutSportFamily(rightActivity)
	if left == right {
		return true
	}
	return left == "other" || right == "other"
}

func workoutsOverlapAndMatch(existing *contract.HealthWorkout, incoming *contract.HealthWorkout) bool {
	if existing == nil || incoming == nil {
		return false
	}
	if !workoutSportsAreCompatible(existing.GetHealthkitWorkoutActivityTypeName(), incoming.GetHealthkitWorkoutActivityTypeName()) {
		return false
	}
	existingStart := existing.GetStartTime().GetSeconds()
	incomingStart := incoming.GetStartTime().GetSeconds()
	startDelta := existingStart - incomingStart
	if startDelta < 0 {
		startDelta = -startDelta
	}
	if startDelta > workoutStartToleranceSeconds {
		return false
	}

	existingDuration := existing.GetDurationSeconds()
	if existingDuration <= 0 {
		existingDuration = float64(existing.GetEndTime().GetSeconds() - existingStart)
	}
	incomingDuration := incoming.GetDurationSeconds()
	if incomingDuration <= 0 {
		incomingDuration = float64(incoming.GetEndTime().GetSeconds() - incomingStart)
	}
	maxDuration := math.Max(existingDuration, incomingDuration)
	minDuration := math.Min(existingDuration, incomingDuration)
	if maxDuration <= 0 {
		return true
	}
	durationTolerance := math.Max(120.0, maxDuration*0.10)
	if math.Abs(existingDuration-incomingDuration) <= durationTolerance {
		return true
	}

	existingEnd := existing.GetEndTime().GetSeconds()
	incomingEnd := incoming.GetEndTime().GetSeconds()
	overlapStart := max(existingStart, incomingStart)
	overlapEnd := min(existingEnd, incomingEnd)
	if overlapEnd > overlapStart && minDuration > 0 {
		overlapRatio := float64(overlapEnd-overlapStart) / minDuration
		if overlapRatio >= 0.80 {
			return true
		}
	}
	return false
}

// upsertDeduplicatedWorkout inserts or merges one workout from any source
// (Apple Health XML, iPhone HealthKit batch, or HealthFit .fit), preserving
// provenance of every contributing source without creating duplicates.
func upsertDeduplicatedWorkout(
	ctx context.Context,
	runner sqlQueryRunner,
	incoming *contract.HealthWorkout,
	provenance *contract.HealthWorkoutSourceProvenance,
	deviceName string,
	exportedAt string,
) (reference string, added bool, updated bool, deduplicated bool, err error) {
	if incoming == nil || incoming.GetStartTime() == nil {
		return "", false, false, false, nil
	}
	normalizedIncoming := proto.Clone(incoming).(*contract.HealthWorkout)
	if normalizedIncoming.GetEndTime() == nil {
		normalizedIncoming.EndTime = normalizedIncoming.GetStartTime()
	}
	if provenance != nil {
		normalizedIncoming.ContributingSources = mergeWorkoutProvenanceList(normalizedIncoming.GetContributingSources(), []*contract.HealthWorkoutSourceProvenance{provenance})
	}

	aliasKey := workoutAliasKey(normalizedIncoming, provenance)
	var targetRef string
	var existingRecord *storedHealthRecord

	// 1. Check alias table for exact prior import (instant O(1) idempotency).
	if aliasKey != "" {
		var mappedRef string
		scanErr := runner.QueryRowContext(ctx, "SELECT workout_reference FROM health_workout_aliases WHERE alias_key=?", aliasKey).Scan(&mappedRef)
		if scanErr == nil && mappedRef != "" {
			matches, readErr := readStoredHealthRecords(ctx, runner, "WHERE reference=?", mappedRef)
			if readErr != nil {
				return "", false, false, false, readErr
			}
			if len(matches) == 1 {
				targetRef = matches[0].reference
				existingRecord = &matches[0]
			}
		} else if scanErr != nil && !errors.Is(scanErr, sql.ErrNoRows) {
			return "", false, false, false, scanErr
		}
	}

	// 2. Check exact canonical reference when not already mapped via alias.
	candidateRef := healthRecordReference(healthRecordKindWorkout, normalizedIncoming.GetHealthkitSampleUuid())
	if existingRecord == nil && strings.TrimSpace(normalizedIncoming.GetHealthkitSampleUuid()) != "" {
		matches, readErr := readStoredHealthRecords(ctx, runner, "WHERE reference=?", candidateRef)
		if readErr != nil {
			return "", false, false, false, readErr
		}
		if len(matches) == 1 {
			targetRef = matches[0].reference
			existingRecord = &matches[0]
		}
	}

	// 3. Check overlapping workouts within ±120s start window for cross-source deduplication.
	if existingRecord == nil {
		startUnix := normalizedIncoming.GetStartTime().GetSeconds()
		candidates, readErr := readStoredHealthRecords(ctx, runner,
			"WHERE record_kind=? AND start_unix BETWEEN ? AND ? ORDER BY start_unix",
			string(healthRecordKindWorkout),
			startUnix-workoutStartToleranceSeconds,
			startUnix+workoutStartToleranceSeconds,
		)
		if readErr != nil {
			return "", false, false, false, readErr
		}
		for i := range candidates {
			existingWorkout, ok := candidates[i].record.(*contract.HealthWorkout)
			if !ok {
				continue
			}
			if workoutsOverlapAndMatch(existingWorkout, normalizedIncoming) {
				targetRef = candidates[i].reference
				existingRecord = &candidates[i]
				deduplicated = true
				break
			}
		}
	}

	var merged *contract.HealthWorkout
	if existingRecord != nil {
		existingWorkout := existingRecord.record.(*contract.HealthWorkout)
		merged = mergeWorkoutRecords(existingWorkout, normalizedIncoming)
		if !deduplicated && !proto.Equal(existingWorkout, merged) && provenance != nil && workoutHasDifferentSourceKind(existingWorkout, provenance.GetSourceKind()) {
			deduplicated = true
		}
	} else {
		targetRef = candidateRef
		merged = normalizedIncoming
	}

	startUnix := merged.GetStartTime().GetSeconds()
	endUnix := merged.GetEndTime().GetSeconds()
	offset := merged.GetUtcOffsetSeconds()
	calDate := localCalendarDateForUnixAndOffset(startUnix, offset, offset != 0)
	provenanceSummary := summarizeWorkoutProvenance(merged.GetContributingSources())
	if deviceName == "" {
		deviceName = merged.GetDevice()
	}

	wasAdded, wasChanged, upsertErr := upsertStoredHealthRecord(
		ctx, runner, targetRef, healthRecordKindWorkout,
		startUnix, endUnix, offset, calDate,
		merged, deviceName, exportedAt, provenanceSummary,
	)
	if upsertErr != nil {
		return "", false, false, false, upsertErr
	}

	if aliasKey != "" {
		if _, err := runner.ExecContext(ctx,
			"INSERT INTO health_workout_aliases(alias_key, workout_reference) VALUES(?,?) ON CONFLICT(alias_key) DO UPDATE SET workout_reference=excluded.workout_reference",
			aliasKey, targetRef); err != nil {
			return "", false, false, false, err
		}
	}
	if uuid := strings.TrimSpace(normalizedIncoming.GetHealthkitSampleUuid()); uuid != "" {
		uuidAlias := "uuid:" + strings.ToLower(uuid)
		if _, err := runner.ExecContext(ctx,
			"INSERT INTO health_workout_aliases(alias_key, workout_reference) VALUES(?,?) ON CONFLICT(alias_key) DO UPDATE SET workout_reference=excluded.workout_reference",
			uuidAlias, targetRef); err != nil {
			return "", false, false, false, err
		}
	}

	return targetRef, wasAdded, wasChanged, deduplicated, nil
}

func workoutAliasKey(workout *contract.HealthWorkout, provenance *contract.HealthWorkoutSourceProvenance) string {
	if provenance != nil && strings.TrimSpace(provenance.GetSourceIdentifier()) != "" {
		return strings.ToLower(strings.TrimSpace(provenance.GetSourceKind())) + ":" + strings.ToLower(strings.TrimSpace(provenance.GetSourceIdentifier()))
	}
	if uuid := strings.TrimSpace(workout.GetHealthkitSampleUuid()); uuid != "" {
		return "uuid:" + strings.ToLower(uuid)
	}
	return ""
}

func workoutHasDifferentSourceKind(existing *contract.HealthWorkout, incomingKind string) bool {
	if incomingKind == "" {
		return false
	}
	for _, src := range existing.GetContributingSources() {
		if src.GetSourceKind() != "" && src.GetSourceKind() != incomingKind {
			return true
		}
	}
	return false
}

// mergeWorkoutRecords combines two representations of the same physical workout
// (for example, an Apple Health XML summary and a detailed HealthFit .fit file)
// without discarding any observed field or provenance from either source.
func mergeWorkoutRecords(existing *contract.HealthWorkout, incoming *contract.HealthWorkout) *contract.HealthWorkout {
	merged := proto.Clone(existing).(*contract.HealthWorkout)

	// Prefer a specific sport name over generic "other"/"workout".
	if normalizeWorkoutSportFamily(merged.GetHealthkitWorkoutActivityTypeName()) == "other" &&
		normalizeWorkoutSportFamily(incoming.GetHealthkitWorkoutActivityTypeName()) != "other" {
		merged.HealthkitWorkoutActivityTypeName = incoming.GetHealthkitWorkoutActivityTypeName()
	}
	if strings.TrimSpace(merged.GetSportSubType()) == "" && strings.TrimSpace(incoming.GetSportSubType()) != "" {
		merged.SportSubType = incoming.GetSportSubType()
	}
	if merged.GetDurationSeconds() <= 0 && incoming.GetDurationSeconds() > 0 {
		merged.DurationSeconds = incoming.GetDurationSeconds()
	}
	if merged.TotalEnergyBurnedKilocalories == nil && incoming.TotalEnergyBurnedKilocalories != nil {
		merged.TotalEnergyBurnedKilocalories = proto.Float64(incoming.GetTotalEnergyBurnedKilocalories())
	}
	if merged.TotalDistanceMeters == nil && incoming.TotalDistanceMeters != nil {
		merged.TotalDistanceMeters = proto.Float64(incoming.GetTotalDistanceMeters())
	}
	if merged.AverageHeartRateBeatsPerMinute == nil && incoming.AverageHeartRateBeatsPerMinute != nil {
		merged.AverageHeartRateBeatsPerMinute = proto.Float64(incoming.GetAverageHeartRateBeatsPerMinute())
	}
	if merged.MaxHeartRateBeatsPerMinute == nil && incoming.MaxHeartRateBeatsPerMinute != nil {
		merged.MaxHeartRateBeatsPerMinute = proto.Float64(incoming.GetMaxHeartRateBeatsPerMinute())
	}
	if merged.TotalAscentMeters == nil && incoming.TotalAscentMeters != nil {
		merged.TotalAscentMeters = proto.Float64(incoming.GetTotalAscentMeters())
	}
	if merged.AveragePowerWatts == nil && incoming.AveragePowerWatts != nil {
		merged.AveragePowerWatts = proto.Float64(incoming.GetAveragePowerWatts())
	}
	if merged.AverageCadence == nil && incoming.AverageCadence != nil {
		merged.AverageCadence = proto.Float64(incoming.GetAverageCadence())
	}
	if merged.AverageSpeedMetersPerSecond == nil && incoming.AverageSpeedMetersPerSecond != nil {
		merged.AverageSpeedMetersPerSecond = proto.Float64(incoming.GetAverageSpeedMetersPerSecond())
	}
	if strings.TrimSpace(merged.GetSourceName()) == "" && strings.TrimSpace(incoming.GetSourceName()) != "" {
		merged.SourceName = incoming.GetSourceName()
	}
	if strings.TrimSpace(merged.GetSourceVersion()) == "" && strings.TrimSpace(incoming.GetSourceVersion()) != "" {
		merged.SourceVersion = incoming.GetSourceVersion()
	}
	if strings.TrimSpace(merged.GetDevice()) == "" && strings.TrimSpace(incoming.GetDevice()) != "" {
		merged.Device = incoming.GetDevice()
	}
	if merged.GetCreationTime() == nil && incoming.GetCreationTime() != nil {
		merged.CreationTime = incoming.GetCreationTime()
	}
	if merged.GetUtcOffsetSeconds() == 0 && incoming.GetUtcOffsetSeconds() != 0 {
		merged.UtcOffsetSeconds = incoming.GetUtcOffsetSeconds()
	}
	if merged.GetRouteSummary() == nil && incoming.GetRouteSummary() != nil {
		merged.RouteSummary = proto.Clone(incoming.GetRouteSummary()).(*contract.HealthWorkoutRouteSummary)
	} else if merged.GetRouteSummary() != nil && incoming.GetRouteSummary() != nil &&
		incoming.GetRouteSummary().GetPointCount() > merged.GetRouteSummary().GetPointCount() {
		merged.RouteSummary = proto.Clone(incoming.GetRouteSummary()).(*contract.HealthWorkoutRouteSummary)
	}

	merged.MetadataEntries = mergeMetadataEntryLists(merged.GetMetadataEntries(), incoming.GetMetadataEntries())
	merged.Statistics = mergeWorkoutStatisticLists(merged.GetStatistics(), incoming.GetStatistics())
	if len(merged.GetEvents()) == 0 && len(incoming.GetEvents()) > 0 {
		merged.Events = incoming.GetEvents()
	}
	merged.ContributingSources = mergeWorkoutProvenanceList(merged.GetContributingSources(), incoming.GetContributingSources())
	return merged
}

func mergeMetadataEntryLists(existing, incoming []*contract.HealthMetadataEntry) []*contract.HealthMetadataEntry {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]*contract.HealthMetadataEntry, 0, len(existing)+len(incoming))
	for _, list := range [][]*contract.HealthMetadataEntry{existing, incoming} {
		for _, entry := range list {
			if entry == nil || strings.TrimSpace(entry.GetKey()) == "" {
				continue
			}
			sig := entry.GetKey() + "\x00" + entry.GetValue()
			if _, dup := seen[sig]; dup {
				continue
			}
			seen[sig] = struct{}{}
			out = append(out, entry)
		}
	}
	return out
}

func mergeWorkoutStatisticLists(existing, incoming []*contract.HealthWorkoutStatistic) []*contract.HealthWorkoutStatistic {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]*contract.HealthWorkoutStatistic, 0, len(existing)+len(incoming))
	for _, list := range [][]*contract.HealthWorkoutStatistic{existing, incoming} {
		for _, stat := range list {
			if stat == nil || strings.TrimSpace(stat.GetQuantityTypeIdentifier()) == "" {
				continue
			}
			sig := stat.GetQuantityTypeIdentifier() + "\x00" + stat.GetUnit()
			if _, dup := seen[sig]; dup {
				continue
			}
			seen[sig] = struct{}{}
			out = append(out, stat)
		}
	}
	return out
}

func mergeWorkoutProvenanceList(existing, incoming []*contract.HealthWorkoutSourceProvenance) []*contract.HealthWorkoutSourceProvenance {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]*contract.HealthWorkoutSourceProvenance, 0, len(existing)+len(incoming))
	for _, list := range [][]*contract.HealthWorkoutSourceProvenance{existing, incoming} {
		for _, src := range list {
			if src == nil {
				continue
			}
			sig := src.GetSourceKind() + "\x00" + src.GetSourceName() + "\x00" + src.GetSourceIdentifier() + "\x00" + src.GetImportedFileName()
			if _, dup := seen[sig]; dup {
				continue
			}
			seen[sig] = struct{}{}
			out = append(out, src)
		}
	}
	return out
}

func summarizeWorkoutProvenance(sources []*contract.HealthWorkoutSourceProvenance) string {
	if len(sources) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(sources))
	labels := make([]string, 0, len(sources))
	for _, src := range sources {
		label := formatWorkoutSourceProvenanceLabel(src)
		if label == "" {
			continue
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	return strings.Join(labels, " + ")
}

func formatWorkoutSourceProvenanceLabel(src *contract.HealthWorkoutSourceProvenance) string {
	if src == nil {
		return ""
	}
	name := strings.TrimSpace(src.GetSourceName())
	kindLabel := ""
	switch src.GetSourceKind() {
	case workoutSourceKindHealthFitFIT:
		kindLabel = "HealthFit .fit"
	case workoutSourceKindAppleHealthXML:
		kindLabel = "Apple Health"
	case workoutSourceKindHealthKitBatch:
		kindLabel = "HealthKit"
	default:
		kindLabel = src.GetSourceKind()
	}
	if name == "" {
		return kindLabel
	}
	if kindLabel == "" || strings.EqualFold(name, kindLabel) {
		return name
	}
	return name + " (" + kindLabel + ")"
}
