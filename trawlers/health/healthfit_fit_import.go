package health

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/kit/semicircles"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/filedef"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const healthFitImportKind = "healthfit_fit"

// importFITFile reads and imports one Garmin/HealthFit .fit file into health.db,
// deduplicating against overlapping Apple Health workouts while preserving
// provenance and richer FIT telemetry (GPS route, power, cadence, ascent, HR).
func importFITFile(ctx context.Context, db *sql.DB, fitPath string) (healthImportStatistics, error) {
	rawBytes, err := os.ReadFile(fitPath)
	if err != nil {
		return healthImportStatistics{}, fmt.Errorf("read FIT file %s: %w", filepath.Base(fitPath), err)
	}
	return importFITBytes(ctx, db, rawBytes, filepath.Base(fitPath), fitPath)
}

func importFITBytes(ctx context.Context, db *sql.DB, rawBytes []byte, fileName string, sourcePath string) (healthImportStatistics, error) {
	var stats healthImportStatistics
	sum := sha256.Sum256(rawBytes)
	digest := hex.EncodeToString(sum[:])

	alreadyImported, err := healthImportDigestAlreadyApplied(ctx, db, healthFitImportKind, digest)
	if err != nil {
		return stats, err
	}
	if alreadyImported {
		stats.filesSkipped++
		return stats, nil
	}

	listener := filedef.NewListener()
	defer listener.Close()

	dec := decoder.New(
		bytes.NewReader(rawBytes),
		decoder.WithMesgListener(listener),
		decoder.WithBroadcastOnly(),
		decoder.WithIgnoreChecksum(),
	)
	if _, err := dec.Decode(); err != nil {
		return stats, fmt.Errorf("decode FIT file %s: %w", fileName, err)
	}

	activity, ok := listener.File().(*filedef.Activity)
	if !ok || activity == nil {
		// Non-activity FIT file (e.g. settings/course/monitoring); skip cleanly.
		stats.filesSkipped++
		return stats, nil
	}

	sourceLabel, deviceLabel := describeFITSource(activity)
	routeSummary := extractFITRouteSummary(activity.Records, fileName)
	lapEvents := extractFITLapEvents(activity.Laps)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer func() { _ = tx.Rollback() }()

	if len(activity.Sessions) == 0 {
		// Fallback: synthesize one workout when a FIT activity has Records but no Session message.
		if fallbackWorkout := synthesizeFITWorkoutFromRecords(activity.Records, sourceLabel, deviceLabel, digest, routeSummary); fallbackWorkout != nil {
			prov := &contract.HealthWorkoutSourceProvenance{
				SourceKind:       workoutSourceKindHealthFitFIT,
				SourceName:       sourceLabel,
				SourceIdentifier: "fit:" + digest[:16],
				ImportedFileName: fileName,
			}
			_, added, updated, dedup, wErr := upsertDeduplicatedWorkout(ctx, tx, fallbackWorkout, prov, deviceLabel, "")
			if wErr != nil {
				return stats, wErr
			}
			if added {
				stats.workoutsAdded++
			}
			if updated {
				stats.workoutsUpdated++
			}
			if dedup {
				stats.workoutsDeduplicated++
			}
		}
	} else {
		for idx, session := range activity.Sessions {
			workout := buildWorkoutFromFITSession(session, idx, digest, sourceLabel, deviceLabel, routeSummary, lapEvents)
			if workout == nil {
				continue
			}
			prov := &contract.HealthWorkoutSourceProvenance{
				SourceKind:       workoutSourceKindHealthFitFIT,
				SourceName:       sourceLabel,
				SourceIdentifier: fmt.Sprintf("fit:%s:%d", digest[:16], idx),
				ImportedFileName: fileName,
			}
			_, added, updated, dedup, wErr := upsertDeduplicatedWorkout(ctx, tx, workout, prov, deviceLabel, "")
			if wErr != nil {
				return stats, wErr
			}
			if added {
				stats.workoutsAdded++
			}
			if updated {
				stats.workoutsUpdated++
			}
			if dedup {
				stats.workoutsDeduplicated++
			}
		}
	}

	importName := "fit:" + digest[:24]
	if err := recordAppliedHealthImport(ctx, tx, importName, healthFitImportKind, sourcePath, digest, stats.workoutsAdded, stats.workoutsUpdated); err != nil {
		return stats, err
	}
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	stats.filesProcessed++
	return stats, nil
}

func describeFITSource(activity *filedef.Activity) (sourceName string, deviceName string) {
	manufacturer := strings.TrimSpace(activity.FileId.Manufacturer.String())
	if manufacturer == "" || strings.EqualFold(manufacturer, "invalid") || strings.HasPrefix(strings.ToLower(manufacturer), "manufacturer(") {
		manufacturer = ""
	}
	productName := strings.TrimSpace(activity.FileId.ProductName)
	for _, dev := range activity.DeviceInfos {
		if dev == nil {
			continue
		}
		if productName == "" && strings.TrimSpace(dev.ProductName) != "" {
			productName = strings.TrimSpace(dev.ProductName)
		}
		if manufacturer == "" {
			m := strings.TrimSpace(dev.Manufacturer.String())
			if m != "" && !strings.EqualFold(m, "invalid") && !strings.HasPrefix(strings.ToLower(m), "manufacturer(") {
				manufacturer = m
			}
		}
	}
	if manufacturer != "" {
		manufacturer = strings.ToUpper(manufacturer[:1]) + manufacturer[1:]
	}
	switch {
	case manufacturer != "" && productName != "":
		if strings.Contains(strings.ToLower(productName), strings.ToLower(manufacturer)) {
			deviceName = productName
		} else {
			deviceName = manufacturer + " " + productName
		}
	case productName != "":
		deviceName = productName
	case manufacturer != "":
		deviceName = manufacturer
	default:
		deviceName = "HealthFit"
	}
	sourceName = deviceName
	return sourceName, deviceName
}

func mapFITSportToHealthKitActivity(sport typedef.Sport, subSport typedef.SubSport) (activityName string, subSportName string) {
	subRaw := strings.TrimSpace(subSport.String())
	if subRaw != "" && !strings.EqualFold(subRaw, "generic") && !strings.EqualFold(subRaw, "invalid") && !strings.HasPrefix(strings.ToLower(subRaw), "subsport(") {
		subSportName = subRaw
	}
	switch sport {
	case typedef.SportRunning:
		return "running", subSportName
	case typedef.SportCycling:
		return "cycling", subSportName
	case typedef.SportWalking:
		return "walking", subSportName
	case typedef.SportSwimming:
		return "swimming", subSportName
	case typedef.SportHiking:
		return "hiking", subSportName
	case typedef.SportRowing:
		return "rowing", subSportName
	case typedef.SportTraining:
		if subSport == typedef.SubSportStrengthTraining {
			return "traditionalStrengthTraining", subSportName
		}
		if subSport == typedef.SubSportYoga {
			return "yoga", subSportName
		}
		if subSport == typedef.SubSportPilates {
			return "pilates", subSportName
		}
		if subSport == typedef.SubSportCardioTraining {
			return "mixedCardio", subSportName
		}
		if subSport == typedef.SubSportFlexibilityTraining {
			return "flexibility", subSportName
		}
		return "functionalStrengthTraining", subSportName
	case typedef.SportFitnessEquipment:
		switch subSport {
		case typedef.SubSportIndoorCycling:
			return "cycling", subSportName
		case typedef.SubSportTreadmill:
			return "running", subSportName
		case typedef.SubSportElliptical:
			return "elliptical", subSportName
		case typedef.SubSportStairClimbing:
			return "stairClimbing", subSportName
		case typedef.SubSportIndoorRowing:
			return "rowing", subSportName
		default:
			return "mixedCardio", subSportName
		}
	case typedef.SportAlpineSkiing:
		return "downhillSkiing", subSportName
	case typedef.SportCrossCountrySkiing:
		return "crossCountrySkiing", subSportName
	case typedef.SportSnowboarding:
		return "snowboarding", subSportName
	case typedef.SportTennis:
		return "tennis", subSportName
	case typedef.SportGolf:
		return "golf", subSportName
	case typedef.SportStandUpPaddleboarding, typedef.SportKayaking, typedef.SportPaddling:
		return "paddleSports", subSportName
	case typedef.SportSurfing:
		return "surfingSports", subSportName
	case typedef.SportRockClimbing, typedef.SportMountaineering:
		return "climbing", subSportName
	case typedef.SportHiit:
		return "highIntensityIntervalTraining", subSportName
	case typedef.SportSoccer:
		return "soccer", subSportName
	case typedef.SportBasketball:
		return "basketball", subSportName
	case typedef.SportBoxing:
		return "boxing", subSportName
	default:
		name := strings.TrimSpace(sport.String())
		if name == "" || strings.EqualFold(name, "generic") || strings.EqualFold(name, "invalid") || strings.HasPrefix(strings.ToLower(name), "sport(") {
			return "other", subSportName
		}
		return fitSnakeToCamel(name), subSportName
	}
}

func fitSnakeToCamel(snake string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(snake)), "_")
	var b strings.Builder
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			b.WriteString(part)
		} else {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	if b.Len() == 0 {
		return "other"
	}
	return b.String()
}

func buildWorkoutFromFITSession(
	session *mesgdef.Session,
	sessionIndex int,
	fileDigest string,
	sourceLabel string,
	deviceLabel string,
	routeSummary *contract.HealthWorkoutRouteSummary,
	lapEvents []*contract.HealthWorkoutEvent,
) *contract.HealthWorkout {
	if session == nil {
		return nil
	}
	startTime := session.StartTime
	if startTime.IsZero() {
		startTime = session.Timestamp
	}
	if startTime.IsZero() {
		return nil
	}
	durationSeconds := validPositiveFloat(session.TotalTimerTimeScaled())
	if durationSeconds == 0 {
		durationSeconds = validPositiveFloat(session.TotalElapsedTimeScaled())
	}
	endTime := session.Timestamp
	if endTime.IsZero() || endTime.Before(startTime) {
		endTime = startTime.Add(time.Duration(durationSeconds * float64(time.Second)))
	}
	if durationSeconds == 0 {
		durationSeconds = endTime.Sub(startTime).Seconds()
	}

	activityName, subSportName := mapFITSportToHealthKitActivity(session.Sport, session.SubSport)
	workout := &contract.HealthWorkout{
		HealthkitSampleUuid:              fmt.Sprintf("fit-%s-%d", fileDigest[:16], sessionIndex),
		HealthkitWorkoutActivityTypeName: activityName,
		SportSubType:                     subSportName,
		StartTime:                        timestamppb.New(startTime.UTC()),
		EndTime:                          timestamppb.New(endTime.UTC()),
		DurationSeconds:                  durationSeconds,
		SourceName:                       sourceLabel,
		Device:                           deviceLabel,
	}

	if dist := validPositiveFloat(session.TotalDistanceScaled()); dist > 0 {
		workout.TotalDistanceMeters = proto.Float64(dist)
	}
	if session.TotalCalories != basetype.Uint16Invalid && session.TotalCalories > 0 {
		workout.TotalEnergyBurnedKilocalories = proto.Float64(float64(session.TotalCalories))
	}
	if session.AvgHeartRate != basetype.Uint8Invalid && session.AvgHeartRate > 0 {
		workout.AverageHeartRateBeatsPerMinute = proto.Float64(float64(session.AvgHeartRate))
	}
	if session.MaxHeartRate != basetype.Uint8Invalid && session.MaxHeartRate > 0 {
		workout.MaxHeartRateBeatsPerMinute = proto.Float64(float64(session.MaxHeartRate))
	}
	if session.TotalAscent != basetype.Uint16Invalid && session.TotalAscent > 0 {
		workout.TotalAscentMeters = proto.Float64(float64(session.TotalAscent))
	}
	if session.AvgPower != basetype.Uint16Invalid && session.AvgPower > 0 {
		workout.AveragePowerWatts = proto.Float64(float64(session.AvgPower))
	}
	if session.AvgCadence != basetype.Uint8Invalid && session.AvgCadence > 0 {
		workout.AverageCadence = proto.Float64(float64(session.AvgCadence))
	}
	avgSpeed := validPositiveFloat(session.EnhancedAvgSpeedScaled())
	if avgSpeed == 0 {
		avgSpeed = validPositiveFloat(session.AvgSpeedScaled())
	}
	if avgSpeed > 0 {
		workout.AverageSpeedMetersPerSecond = proto.Float64(avgSpeed)
	}

	// Preserve extended FIT telemetry cleanly as typed statistics/metadata.
	if session.MaxPower != basetype.Uint16Invalid && session.MaxPower > 0 {
		workout.Statistics = append(workout.Statistics, &contract.HealthWorkoutStatistic{
			QuantityTypeIdentifier: "FITMaxPower",
			Unit:                   "W",
			MaximumValue:           proto.Float64(float64(session.MaxPower)),
		})
	}
	if session.NormalizedPower != basetype.Uint16Invalid && session.NormalizedPower > 0 {
		workout.Statistics = append(workout.Statistics, &contract.HealthWorkoutStatistic{
			QuantityTypeIdentifier: "FITNormalizedPower",
			Unit:                   "W",
			AverageValue:           proto.Float64(float64(session.NormalizedPower)),
		})
	}
	if tss := validPositiveFloat(session.TrainingStressScoreScaled()); tss > 0 {
		workout.MetadataEntries = append(workout.MetadataEntries, &contract.HealthMetadataEntry{
			Key:   "FITTrainingStressScore",
			Value: strconv.FormatFloat(tss, 'f', 1, 64),
		})
	}
	if intensityFactor := validPositiveFloat(session.IntensityFactorScaled()); intensityFactor > 0 {
		workout.MetadataEntries = append(workout.MetadataEntries, &contract.HealthMetadataEntry{
			Key:   "FITIntensityFactor",
			Value: strconv.FormatFloat(intensityFactor, 'f', 2, 64),
		})
	}
	if session.TotalDescent != basetype.Uint16Invalid && session.TotalDescent > 0 {
		workout.MetadataEntries = append(workout.MetadataEntries, &contract.HealthMetadataEntry{
			Key:   "FITTotalDescentMeters",
			Value: strconv.Itoa(int(session.TotalDescent)),
		})
	}

	if sessionIndex == 0 && routeSummary != nil {
		workout.RouteSummary = routeSummary
	}
	if sessionIndex == 0 && len(lapEvents) > 0 {
		workout.Events = lapEvents
	}
	return workout
}

func synthesizeFITWorkoutFromRecords(
	records []*mesgdef.Record,
	sourceLabel string,
	deviceLabel string,
	fileDigest string,
	routeSummary *contract.HealthWorkoutRouteSummary,
) *contract.HealthWorkout {
	var firstTime, lastTime time.Time
	var maxDist float64
	var hrSum float64
	var hrCount int
	var maxHR uint8
	for _, rec := range records {
		if rec == nil || rec.Timestamp.IsZero() {
			continue
		}
		if firstTime.IsZero() || rec.Timestamp.Before(firstTime) {
			firstTime = rec.Timestamp
		}
		if lastTime.IsZero() || rec.Timestamp.After(lastTime) {
			lastTime = rec.Timestamp
		}
		if d := validPositiveFloat(rec.DistanceScaled()); d > maxDist {
			maxDist = d
		}
		if rec.HeartRate != basetype.Uint8Invalid && rec.HeartRate > 0 {
			hrSum += float64(rec.HeartRate)
			hrCount++
			if rec.HeartRate > maxHR {
				maxHR = rec.HeartRate
			}
		}
	}
	if firstTime.IsZero() {
		return nil
	}
	workout := &contract.HealthWorkout{
		HealthkitSampleUuid:              fmt.Sprintf("fit-%s-rec", fileDigest[:16]),
		HealthkitWorkoutActivityTypeName: "other",
		StartTime:                        timestamppb.New(firstTime.UTC()),
		EndTime:                          timestamppb.New(lastTime.UTC()),
		DurationSeconds:                  max(lastTime.Sub(firstTime).Seconds(), 0),
		SourceName:                       sourceLabel,
		Device:                           deviceLabel,
		RouteSummary:                     routeSummary,
	}
	if maxDist > 0 {
		workout.TotalDistanceMeters = proto.Float64(maxDist)
	}
	if hrCount > 0 {
		workout.AverageHeartRateBeatsPerMinute = proto.Float64(hrSum / float64(hrCount))
	}
	if maxHR > 0 {
		workout.MaxHeartRateBeatsPerMinute = proto.Float64(float64(maxHR))
	}
	return workout
}

func extractFITRouteSummary(records []*mesgdef.Record, fileRef string) *contract.HealthWorkoutRouteSummary {
	summary := &contract.HealthWorkoutRouteSummary{
		RouteFileReference: fileRef,
	}
	var (
		pointCount int32
		minLat     = math.MaxFloat64
		maxLat     = -math.MaxFloat64
		minLon     = math.MaxFloat64
		maxLon     = -math.MaxFloat64
		prevAlt    float64
		hasPrevAlt bool
		elevGain   float64
		elevLoss   float64
	)
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if rec.PositionLat != basetype.Sint32Invalid && rec.PositionLong != basetype.Sint32Invalid {
			lat := semicircles.ToDegrees(rec.PositionLat)
			lon := semicircles.ToDegrees(rec.PositionLong)
			if !math.IsNaN(lat) && !math.IsNaN(lon) && math.Abs(lat) <= 90 && math.Abs(lon) <= 180 {
				if pointCount == 0 {
					summary.StartLatitude = proto.Float64(lat)
					summary.StartLongitude = proto.Float64(lon)
				}
				summary.EndLatitude = proto.Float64(lat)
				summary.EndLongitude = proto.Float64(lon)
				if lat < minLat {
					minLat = lat
				}
				if lat > maxLat {
					maxLat = lat
				}
				if lon < minLon {
					minLon = lon
				}
				if lon > maxLon {
					maxLon = lon
				}
				pointCount++
			}
		}
		alt := rec.EnhancedAltitudeScaled()
		if math.IsNaN(alt) {
			alt = rec.AltitudeScaled()
		}
		if !math.IsNaN(alt) {
			if hasPrevAlt {
				delta := alt - prevAlt
				if delta > 0 {
					elevGain += delta
				} else if delta < 0 {
					elevLoss += -delta
				}
			}
			prevAlt = alt
			hasPrevAlt = true
		}
	}
	if pointCount == 0 {
		return nil
	}
	summary.PointCount = pointCount
	summary.MinLatitude = proto.Float64(minLat)
	summary.MaxLatitude = proto.Float64(maxLat)
	summary.MinLongitude = proto.Float64(minLon)
	summary.MaxLongitude = proto.Float64(maxLon)
	if hasPrevAlt {
		summary.TotalElevationGainMeters = proto.Float64(elevGain)
		summary.TotalElevationLossMeters = proto.Float64(elevLoss)
	}
	return summary
}

func extractFITLapEvents(laps []*mesgdef.Lap) []*contract.HealthWorkoutEvent {
	if len(laps) == 0 {
		return nil
	}
	events := make([]*contract.HealthWorkoutEvent, 0, len(laps))
	for i, lap := range laps {
		if lap == nil {
			continue
		}
		evTime := lap.StartTime
		if evTime.IsZero() {
			evTime = lap.Timestamp
		}
		if evTime.IsZero() {
			continue
		}
		ev := &contract.HealthWorkoutEvent{
			EventType: "HKWorkoutEventTypeLap",
			EventTime: timestamppb.New(evTime.UTC()),
		}
		if dur := validPositiveFloat(lap.TotalElapsedTimeScaled()); dur > 0 {
			ev.DurationSeconds = proto.Float64(dur)
		}
		ev.MetadataEntries = append(ev.MetadataEntries, &contract.HealthMetadataEntry{
			Key:   "LapNumber",
			Value: strconv.Itoa(i + 1),
		})
		if dist := validPositiveFloat(lap.TotalDistanceScaled()); dist > 0 {
			ev.MetadataEntries = append(ev.MetadataEntries, &contract.HealthMetadataEntry{
				Key:   "DistanceMeters",
				Value: strconv.FormatFloat(dist, 'f', 1, 64),
			})
		}
		if lap.AvgHeartRate != basetype.Uint8Invalid && lap.AvgHeartRate > 0 {
			ev.MetadataEntries = append(ev.MetadataEntries, &contract.HealthMetadataEntry{
				Key:   "AvgHeartRateBPM",
				Value: strconv.Itoa(int(lap.AvgHeartRate)),
			})
		}
		if lap.AvgPower != basetype.Uint16Invalid && lap.AvgPower > 0 {
			ev.MetadataEntries = append(ev.MetadataEntries, &contract.HealthMetadataEntry{
				Key:   "AvgPowerWatts",
				Value: strconv.Itoa(int(lap.AvgPower)),
			})
		}
		events = append(events, ev)
	}
	return events
}

func validPositiveFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return v
}
