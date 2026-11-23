package health

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
)

// healthMetricAggregationKind determines whether samples on one day should be
// summed (cumulative totals like steps/energy/distance) or averaged with min/max
// (discrete observations like heart rate, HRV, weight, blood oxygen).
type healthMetricAggregationKind int

const (
	healthMetricAggregationDiscrete healthMetricAggregationKind = iota
	healthMetricAggregationCumulative
	healthMetricAggregationCategory
)

type healthMetricDescriptor struct {
	displayName string
	aggregation healthMetricAggregationKind
	decimals    int
	summaryRank int // lower rank appears earlier in daily rollups (0 = unranked)
}

var knownHealthMetricDescriptors = map[string]healthMetricDescriptor{
	"HKQuantityTypeIdentifierStepCount":                       {displayName: "Steps", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 1},
	"HKQuantityTypeIdentifierDistanceWalkingRunning":          {displayName: "Walking and running distance", aggregation: healthMetricAggregationCumulative, decimals: 2, summaryRank: 2},
	"HKQuantityTypeIdentifierDistanceCycling":                 {displayName: "Cycling distance", aggregation: healthMetricAggregationCumulative, decimals: 2, summaryRank: 3},
	"HKQuantityTypeIdentifierDistanceSwimming":                {displayName: "Swimming distance", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 4},
	"HKQuantityTypeIdentifierActiveEnergyBurned":              {displayName: "Active energy", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 5},
	"HKQuantityTypeIdentifierBasalEnergyBurned":               {displayName: "Resting energy", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 18},
	"HKQuantityTypeIdentifierAppleExerciseTime":               {displayName: "Exercise time", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 6},
	"HKQuantityTypeIdentifierAppleStandTime":                  {displayName: "Stand time", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 19},
	"HKQuantityTypeIdentifierFlightsClimbed":                  {displayName: "Flights climbed", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 7},
	"HKQuantityTypeIdentifierRestingHeartRate":                {displayName: "Resting heart rate", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 8},
	"HKQuantityTypeIdentifierHeartRateVariabilitySDNN":        {displayName: "Heart rate variability (SDNN)", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 9},
	"HKQuantityTypeIdentifierHeartRate":                       {displayName: "Heart rate", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 10},
	"HKQuantityTypeIdentifierWalkingHeartRateAverage":         {displayName: "Walking heart rate average", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 16},
	"HKQuantityTypeIdentifierVO2Max":                          {displayName: "VO2 max", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 11},
	"HKQuantityTypeIdentifierBodyMass":                        {displayName: "Weight", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 12},
	"HKQuantityTypeIdentifierBodyFatPercentage":               {displayName: "Body fat", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 13},
	"HKQuantityTypeIdentifierBodyMassIndex":                   {displayName: "Body mass index", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 20},
	"HKQuantityTypeIdentifierOxygenSaturation":                {displayName: "Blood oxygen (SpO2)", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 14},
	"HKQuantityTypeIdentifierRespiratoryRate":                 {displayName: "Respiratory rate", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 15},
	"HKQuantityTypeIdentifierBloodPressureSystolic":           {displayName: "Blood pressure systolic", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 17},
	"HKQuantityTypeIdentifierBloodPressureDiastolic":          {displayName: "Blood pressure diastolic", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 17},
	"HKQuantityTypeIdentifierBloodGlucose":                    {displayName: "Blood glucose", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 17},
	"HKQuantityTypeIdentifierAppleSleepingWristTemperature":   {displayName: "Sleeping wrist temperature", aggregation: healthMetricAggregationDiscrete, decimals: 2, summaryRank: 21},
	"HKQuantityTypeIdentifierEnvironmentalAudioExposure":      {displayName: "Environmental audio exposure", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 25},
	"HKQuantityTypeIdentifierHeadphoneAudioExposure":          {displayName: "Headphone audio exposure", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 26},
	"HKQuantityTypeIdentifierCyclingPower":                    {displayName: "Cycling power", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 22},
	"HKQuantityTypeIdentifierCyclingCadence":                  {displayName: "Cycling cadence", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 23},
	"HKQuantityTypeIdentifierCyclingSpeed":                    {displayName: "Cycling speed", aggregation: healthMetricAggregationDiscrete, decimals: 1, summaryRank: 24},
	"HKQuantityTypeIdentifierRunningPower":                    {displayName: "Running power", aggregation: healthMetricAggregationDiscrete, decimals: 0, summaryRank: 24},
	"HKQuantityTypeIdentifierDietaryEnergyConsumed":           {displayName: "Dietary energy", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 27},
	"HKQuantityTypeIdentifierDietaryProtein":                  {displayName: "Dietary protein", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 28},
	"HKQuantityTypeIdentifierDietaryCarbohydrates":            {displayName: "Dietary carbohydrates", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 29},
	"HKQuantityTypeIdentifierDietaryFatTotal":                 {displayName: "Dietary fat", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 30},
	"HKQuantityTypeIdentifierDietaryWater":                    {displayName: "Water intake", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 31},
	"HKQuantityTypeIdentifierDietaryCaffeine":                 {displayName: "Caffeine intake", aggregation: healthMetricAggregationCumulative, decimals: 0, summaryRank: 32},
	"HKCategoryTypeIdentifierSleepAnalysis":                   {displayName: "Sleep analysis", aggregation: healthMetricAggregationCategory, summaryRank: 40},
	"HKCategoryTypeIdentifierAppleStandHour":                  {displayName: "Stand hours", aggregation: healthMetricAggregationCategory, summaryRank: 41},
	"HKCategoryTypeIdentifierMindfulSession":                  {displayName: "Mindful session", aggregation: healthMetricAggregationCategory, summaryRank: 42},
	"HKCategoryTypeIdentifierHighHeartRateEvent":              {displayName: "High heart rate event", aggregation: healthMetricAggregationCategory, summaryRank: 43},
	"HKCategoryTypeIdentifierLowHeartRateEvent":               {displayName: "Low heart rate event", aggregation: healthMetricAggregationCategory, summaryRank: 44},
	"HKCategoryTypeIdentifierIrregularHeartRhythmEvent":       {displayName: "Irregular heart rhythm event", aggregation: healthMetricAggregationCategory, summaryRank: 45},
	"HKCategoryTypeIdentifierEnvironmentalAudioExposureEvent": {displayName: "Loud environment event", aggregation: healthMetricAggregationCategory, summaryRank: 46},
	"HKCategoryTypeIdentifierHeadphoneAudioExposureEvent":     {displayName: "Loud headphone audio event", aggregation: healthMetricAggregationCategory, summaryRank: 47},
	"HKCategoryTypeIdentifierAppleWalkingSteadinessEvent":     {displayName: "Walking steadiness event", aggregation: healthMetricAggregationCategory, summaryRank: 48},
}

func describeHealthMetricType(healthkitType string) healthMetricDescriptor {
	if desc, ok := knownHealthMetricDescriptors[healthkitType]; ok {
		return desc
	}
	if strings.HasPrefix(healthkitType, "HKCategoryTypeIdentifier") {
		return healthMetricDescriptor{
			displayName: humanizeHealthKitIdentifier(healthkitType),
			aggregation: healthMetricAggregationCategory,
			summaryRank: 90,
		}
	}
	agg := healthMetricAggregationDiscrete
	lower := strings.ToLower(healthkitType)
	if strings.Contains(lower, "count") ||
		strings.Contains(lower, "distance") ||
		strings.Contains(lower, "energy") ||
		strings.Contains(lower, "dietary") ||
		strings.Contains(lower, "flights") ||
		strings.Contains(lower, "time") ||
		strings.Contains(lower, "strokes") {
		agg = healthMetricAggregationCumulative
	}
	return healthMetricDescriptor{
		displayName: humanizeHealthKitIdentifier(healthkitType),
		aggregation: agg,
		decimals:    1,
		summaryRank: 80,
	}
}

// humanizeHealthKitIdentifier converts identifiers such as
// "HKQuantityTypeIdentifierHeartRateVariabilitySDNN" or
// "HKCategoryValueSleepAnalysisAsleepCore" into readable English.
func humanizeHealthKitIdentifier(identifier string) string {
	trimmed := strings.TrimSpace(identifier)
	for _, prefix := range []string{
		"HKQuantityTypeIdentifier",
		"HKCategoryTypeIdentifier",
		"HKCorrelationTypeIdentifier",
		"HKClinicalTypeIdentifier",
		"HKWorkoutActivityType",
		"HKWorkoutEventType",
		"HKCategoryValueSleepAnalysis",
		"HKCategoryValueAppleStandHour",
		"HKCategoryValueSeverity",
		"HKCategoryValue",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = strings.TrimPrefix(trimmed, prefix)
			break
		}
	}
	if trimmed == "" {
		return identifier
	}
	runes := []rune(trimmed)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 {
			prev := runes[i-1]
			nextIsLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsUpper(r) && (unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextIsLower)) {
				b.WriteRune(' ')
			}
		}
		if i == 0 {
			b.WriteRune(unicode.ToUpper(r))
		} else if unicode.IsUpper(r) && ((i+1 < len(runes) && unicode.IsUpper(runes[i+1])) || (i > 0 && unicode.IsUpper(runes[i-1]) && (i+1 == len(runes) || !unicode.IsLower(runes[i+1])))) {
			b.WriteRune(r)
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// healthMetricCatalogRow describes one metric type present in health_samples.
type healthMetricCatalogRow struct {
	healthkitType string
	displayName   string
	unit          string
	sampleCount   uint64
	firstDate     string
	lastDate      string
	latestRaw     string
	latestNumeric sql.NullFloat64
}

func readHealthMetricCatalog(ctx context.Context, db *sql.DB) ([]healthMetricCatalogRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			s.healthkit_type,
			COUNT(*) AS sample_count,
			MIN(s.calendar_date) AS first_date,
			MAX(s.calendar_date) AS last_date,
			COALESCE((SELECT s2.unit FROM health_samples s2 WHERE s2.healthkit_type = s.healthkit_type ORDER BY s2.start_unix DESC LIMIT 1), '') AS latest_unit,
			COALESCE((SELECT s2.raw_value FROM health_samples s2 WHERE s2.healthkit_type = s.healthkit_type ORDER BY s2.start_unix DESC LIMIT 1), '') AS latest_raw,
			(SELECT s2.numeric_value FROM health_samples s2 WHERE s2.healthkit_type = s.healthkit_type ORDER BY s2.start_unix DESC LIMIT 1) AS latest_numeric
		FROM health_samples s
		GROUP BY s.healthkit_type
		ORDER BY sample_count DESC, s.healthkit_type ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var catalog []healthMetricCatalogRow
	for rows.Next() {
		var row healthMetricCatalogRow
		if err := rows.Scan(
			&row.healthkitType,
			&row.sampleCount,
			&row.firstDate,
			&row.lastDate,
			&row.unit,
			&row.latestRaw,
			&row.latestNumeric,
		); err != nil {
			return nil, err
		}
		row.displayName = describeHealthMetricType(row.healthkitType).displayName
		catalog = append(catalog, row)
	}
	return catalog, rows.Err()
}

// resolveHealthMetricTypeQuery matches a user's --type flag (exact HK identifier,
// short suffix, or human phrase like "resting heart rate" / "steps" / "hrv")
// against the metric types actually archived in health_samples.
func resolveHealthMetricTypeQuery(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}
	catalog, err := readHealthMetricCatalog(ctx, db)
	if err != nil {
		return nil, err
	}
	lowerQuery := strings.ToLower(trimmed)
	compactQuery := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(lowerQuery)

	var exactMatches []string
	var substringMatches []string
	for _, entry := range catalog {
		hkLower := strings.ToLower(entry.healthkitType)
		dispLower := strings.ToLower(entry.displayName)
		shortLower := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(entry.healthkitType, "HKQuantityTypeIdentifier"), "HKCategoryTypeIdentifier"))
		if hkLower == lowerQuery || dispLower == lowerQuery || shortLower == compactQuery {
			exactMatches = append(exactMatches, entry.healthkitType)
			continue
		}
		if strings.Contains(hkLower, compactQuery) || strings.Contains(dispLower, lowerQuery) {
			substringMatches = append(substringMatches, entry.healthkitType)
		}
	}
	if len(exactMatches) > 0 {
		return exactMatches, nil
	}
	return substringMatches, nil
}

// dailySampleMetricAggregate holds one metric's daily SQL aggregate.
type dailySampleMetricAggregate struct {
	calendarDate  string
	healthkitType string
	unit          string
	sampleCount   int64
	sumValue      sql.NullFloat64
	avgValue      sql.NullFloat64
	minValue      sql.NullFloat64
	maxValue      sql.NullFloat64
	totalDuration int64
}

func readDailyAggregatesForMetricTypes(
	ctx context.Context,
	db *sql.DB,
	healthkitTypes []string,
	afterDate string,
	beforeDate string,
	limit int,
) ([]dailySampleMetricAggregate, error) {
	var where []string
	var args []any
	if len(healthkitTypes) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(healthkitTypes)), ",")
		where = append(where, "healthkit_type IN ("+placeholders+")")
		for _, t := range healthkitTypes {
			args = append(args, t)
		}
	}
	if afterDate != "" {
		where = append(where, "calendar_date >= ?")
		args = append(args, afterDate)
	}
	if beforeDate != "" {
		where = append(where, "calendar_date <= ?")
		args = append(args, beforeDate)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}
	query := `
		SELECT
			calendar_date,
			healthkit_type,
			MAX(unit) AS unit,
			COUNT(*) AS sample_count,
			SUM(numeric_value) AS sum_val,
			AVG(numeric_value) AS avg_val,
			MIN(numeric_value) AS min_val,
			MAX(numeric_value) AS max_val,
			SUM(CASE WHEN end_unix > start_unix THEN end_unix - start_unix ELSE 0 END) AS total_dur
		FROM health_samples
		` + clause + `
		GROUP BY calendar_date, healthkit_type
		ORDER BY calendar_date DESC, healthkit_type ASC` + limitClause

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []dailySampleMetricAggregate
	for rows.Next() {
		var row dailySampleMetricAggregate
		if err := rows.Scan(
			&row.calendarDate,
			&row.healthkitType,
			&row.unit,
			&row.sampleCount,
			&row.sumValue,
			&row.avgValue,
			&row.minValue,
			&row.maxValue,
			&row.totalDuration,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func formatDailyMetricAggregateValue(agg dailySampleMetricAggregate) string {
	desc := describeHealthMetricType(agg.healthkitType)
	unit := normalizeMetricDisplayUnit(agg.healthkitType, agg.unit)

	switch desc.aggregation {
	case healthMetricAggregationCumulative:
		if !agg.sumValue.Valid {
			return fmt.Sprintf("%d samples", agg.sampleCount)
		}
		val := agg.sumValue.Float64
		if agg.healthkitType == "HKQuantityTypeIdentifierOxygenSaturation" || agg.healthkitType == "HKQuantityTypeIdentifierBodyFatPercentage" {
			return formatPercentageSampleValue(val, agg.unit, desc.decimals)
		}
		if unit == "" || unit == "count" {
			return formatMetricNumber(val, desc.decimals)
		}
		return formatMetricNumber(val, desc.decimals) + " " + unit

	case healthMetricAggregationDiscrete:
		if !agg.avgValue.Valid {
			return fmt.Sprintf("%d samples", agg.sampleCount)
		}
		if agg.healthkitType == "HKQuantityTypeIdentifierOxygenSaturation" || agg.healthkitType == "HKQuantityTypeIdentifierBodyFatPercentage" {
			avgText := formatPercentageSampleValue(agg.avgValue.Float64, agg.unit, desc.decimals)
			if agg.sampleCount > 1 && agg.minValue.Valid && agg.maxValue.Valid && math.Abs(agg.maxValue.Float64-agg.minValue.Float64) > 0.001 {
				return fmt.Sprintf("%s avg (%s–%s)",
					avgText,
					formatPercentageSampleValue(agg.minValue.Float64, agg.unit, desc.decimals),
					formatPercentageSampleValue(agg.maxValue.Float64, agg.unit, desc.decimals))
			}
			return avgText
		}
		unitSuffix := ""
		if unit != "" && unit != "count" {
			unitSuffix = " " + unit
		}
		if agg.sampleCount == 1 || !agg.minValue.Valid || !agg.maxValue.Valid || math.Abs(agg.maxValue.Float64-agg.minValue.Float64) < 0.05 {
			return formatMetricNumber(agg.avgValue.Float64, desc.decimals) + unitSuffix
		}
		return fmt.Sprintf("%s%s avg (%s–%s)",
			formatMetricNumber(agg.avgValue.Float64, desc.decimals),
			unitSuffix,
			formatMetricNumber(agg.minValue.Float64, desc.decimals),
			formatMetricNumber(agg.maxValue.Float64, desc.decimals),
		)

	case healthMetricAggregationCategory:
		if agg.totalDuration > 0 {
			return healthDurationText(time.Duration(agg.totalDuration)*time.Second) + fmt.Sprintf(" (%d events)", agg.sampleCount)
		}
		return fmt.Sprintf("%d events", agg.sampleCount)
	default:
		return fmt.Sprintf("%d samples", agg.sampleCount)
	}
}

func formatPercentageSampleValue(val float64, unit string, decimals int) string {
	// Apple Health stores OxygenSaturation and BodyFatPercentage as fractions
	// (0.98 for 98%) with unit "%".
	if strings.TrimSpace(unit) == "%" && val > 0 && val <= 1.0 {
		val *= 100
	}
	return formatMetricNumber(val, decimals) + "%"
}

func normalizeMetricDisplayUnit(healthkitType, unit string) string {
	trimmed := strings.TrimSpace(unit)
	if trimmed == "count/min" {
		if healthkitType == "HKQuantityTypeIdentifierRespiratoryRate" {
			return "breaths/min"
		}
		return "bpm"
	}
	return normalizeDisplayUnit(trimmed)
}

func normalizeDisplayUnit(unit string) string {
	trimmed := strings.TrimSpace(unit)
	switch trimmed {
	case "count/min":
		return "bpm"
	case "Cal":
		return "kcal"
	case "count":
		return ""
	default:
		return trimmed
	}
}

func formatMetricNumber(val float64, decimals int) string {
	if decimals == 0 {
		return strconv.FormatInt(int64(math.Round(val)), 10)
	}
	return strconv.FormatFloat(val, 'f', decimals, 64)
}

// sleepNightRollup aggregates all sleep stage samples ending on one local night.
type sleepNightRollup struct {
	calendarDate  string
	bedtimeUnix   int64
	wakeUnix      int64
	utcOffset     int32
	totalAsleep   time.Duration
	coreDuration  time.Duration
	deepDuration  time.Duration
	remDuration   time.Duration
	awakeDuration time.Duration
	inBedDuration time.Duration
	stageCount    int
	sources       []string
	stages        []storedHealthRecord
}

func buildSleepNightRollups(ctx context.Context, db *sql.DB, afterDate, beforeDate string) ([]sleepNightRollup, error) {
	stages, err := readStoredHealthRecords(ctx, db, "WHERE record_kind=? ORDER BY start_unix ASC", string(healthRecordKindSleepSession))
	if err != nil {
		return nil, err
	}
	byNight := make(map[string]*sleepNightRollup)
	sourceSeen := make(map[string]map[string]struct{})
	for _, stored := range stages {
		session, ok := stored.record.(*contract.HealthSleepSession)
		if !ok {
			continue
		}
		// Assign each sleep stage to the local calendar date on which it ends
		// (matching Apple Health's nightly sleep grouping).
		nightDate := localCalendarDateForUnixAndOffset(stored.endUnix, stored.utcOffsetSeconds, stored.utcOffsetSeconds != 0)
		if afterDate != "" && nightDate < afterDate {
			continue
		}
		if beforeDate != "" && nightDate > beforeDate {
			continue
		}
		night := byNight[nightDate]
		if night == nil {
			night = &sleepNightRollup{
				calendarDate: nightDate,
				bedtimeUnix:  stored.startUnix,
				wakeUnix:     stored.endUnix,
				utcOffset:    stored.utcOffsetSeconds,
			}
			byNight[nightDate] = night
			sourceSeen[nightDate] = map[string]struct{}{}
		}
		if stored.startUnix < night.bedtimeUnix {
			night.bedtimeUnix = stored.startUnix
		}
		if stored.endUnix > night.wakeUnix {
			night.wakeUnix = stored.endUnix
		}
		dur := time.Duration(max(stored.endUnix-stored.startUnix, 0)) * time.Second
		switch session.GetSleepStage() {
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_IN_BED:
			night.inBedDuration += dur
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_AWAKE:
			night.awakeDuration += dur
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_CORE:
			night.coreDuration += dur
			night.totalAsleep += dur
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_DEEP:
			night.deepDuration += dur
			night.totalAsleep += dur
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_REM:
			night.remDuration += dur
			night.totalAsleep += dur
		case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_UNSPECIFIED,
			contract.HealthSleepStage_HEALTH_SLEEP_STAGE_UNSPECIFIED:
			night.totalAsleep += dur
		}
		night.stageCount++
		night.stages = append(night.stages, stored)
		if src := strings.TrimSpace(session.GetSourceName()); src != "" {
			if _, dup := sourceSeen[nightDate][src]; !dup {
				sourceSeen[nightDate][src] = struct{}{}
				night.sources = append(night.sources, src)
			}
		}
	}
	nights := make([]sleepNightRollup, 0, len(byNight))
	for _, night := range byNight {
		sort.Strings(night.sources)
		nights = append(nights, *night)
	}
	sort.Slice(nights, func(i, j int) bool {
		return nights[i].calendarDate > nights[j].calendarDate
	})
	return nights, nil
}

// dailyHealthDayRollup merges all health signals for one local calendar date:
// HealthDailyMetrics (iOS batches), HealthActivitySummary (Apple Watch rings),
// SQL daily rollups from health_samples, sleep night total, and workouts.
type dailyHealthDayRollup struct {
	calendarDate    string
	dailyMetrics    *contract.HealthDailyMetrics
	activitySummary *contract.HealthActivitySummary
	sampleMetrics   []dailySampleMetricAggregate
	sleepNight      *sleepNightRollup
	workouts        []storedHealthRecord
}

func buildDailyHealthRollups(ctx context.Context, db *sql.DB, afterDate, beforeDate string) ([]dailyHealthDayRollup, error) {
	byDate := make(map[string]*dailyHealthDayRollup)
	ensureDay := func(date string) *dailyHealthDayRollup {
		if !healthCalendarDatePattern.MatchString(date) {
			return nil
		}
		if afterDate != "" && date < afterDate {
			return nil
		}
		if beforeDate != "" && date > beforeDate {
			return nil
		}
		day := byDate[date]
		if day == nil {
			day = &dailyHealthDayRollup{calendarDate: date}
			byDate[date] = day
		}
		return day
	}

	// 1. Explicit iOS HealthDailyMetrics records.
	dailyMetricRecords, err := readStoredHealthRecords(ctx, db, "WHERE record_kind=?", string(healthRecordKindDailyMetrics))
	if err != nil {
		return nil, err
	}
	for _, stored := range dailyMetricRecords {
		metrics, ok := stored.record.(*contract.HealthDailyMetrics)
		if !ok {
			continue
		}
		if day := ensureDay(metrics.GetCalendarDate()); day != nil {
			day.dailyMetrics = metrics
		}
	}

	// 2. Apple Watch ActivitySummary ring records.
	activityRecords, err := readStoredHealthRecords(ctx, db, "WHERE record_kind=?", string(healthRecordKindActivitySummary))
	if err != nil {
		return nil, err
	}
	for _, stored := range activityRecords {
		summary, ok := stored.record.(*contract.HealthActivitySummary)
		if !ok {
			continue
		}
		if day := ensureDay(summary.GetCalendarDate()); day != nil {
			day.activitySummary = summary
		}
	}

	// 3. Daily sample aggregates from health_samples (excluding sleep/stand hour noise).
	sampleAggs, err := readDailyAggregatesForMetricTypes(ctx, db, nil, afterDate, beforeDate, 0)
	if err != nil {
		return nil, err
	}
	for _, agg := range sampleAggs {
		if agg.healthkitType == "HKCategoryTypeIdentifierSleepAnalysis" || agg.healthkitType == "HKCategoryTypeIdentifierAppleStandHour" {
			continue
		}
		if day := ensureDay(agg.calendarDate); day != nil {
			day.sampleMetrics = append(day.sampleMetrics, agg)
		}
	}

	// 4. Sleep nights.
	sleepNights, err := buildSleepNightRollups(ctx, db, afterDate, beforeDate)
	if err != nil {
		return nil, err
	}
	for i := range sleepNights {
		night := sleepNights[i]
		if day := ensureDay(night.calendarDate); day != nil {
			nightCopy := night
			day.sleepNight = &nightCopy
		}
	}

	// 5. Workouts.
	workouts, err := readStoredHealthRecords(ctx, db, "WHERE record_kind=? ORDER BY start_unix ASC", string(healthRecordKindWorkout))
	if err != nil {
		return nil, err
	}
	for _, workout := range workouts {
		date := workout.calendarDate
		if date == "" {
			date = localCalendarDateForUnixAndOffset(workout.startUnix, workout.utcOffsetSeconds, workout.utcOffsetSeconds != 0)
		}
		if day := ensureDay(date); day != nil {
			day.workouts = append(day.workouts, workout)
		}
	}

	days := make([]dailyHealthDayRollup, 0, len(byDate))
	for _, day := range byDate {
		sort.Slice(day.sampleMetrics, func(i, j int) bool {
			leftRank := describeHealthMetricType(day.sampleMetrics[i].healthkitType).summaryRank
			rightRank := describeHealthMetricType(day.sampleMetrics[j].healthkitType).summaryRank
			if leftRank != rightRank {
				return leftRank < rightRank
			}
			return day.sampleMetrics[i].healthkitType < day.sampleMetrics[j].healthkitType
		})
		days = append(days, *day)
	}
	sort.Slice(days, func(i, j int) bool {
		return days[i].calendarDate > days[j].calendarDate
	})
	return days, nil
}

// storedNotableCategoryEvent represents one rare discrete HKCategoryType sample
// read from health_samples for indexing and opening.
type storedNotableCategoryEvent struct {
	contentHash      string
	healthkitType    string
	calendarDate     string
	startUnix        int64
	endUnix          int64
	utcOffsetSeconds int32
	rawValue         string
	sourceName       string
	sourceVersion    string
	device           string
	metadataJSON     string
}

func readNotableCategoryEvents(ctx context.Context, db *sql.DB, contentHashFilter string) ([]storedNotableCategoryEvent, error) {
	query := `
		SELECT content_hash, healthkit_type, calendar_date, start_unix, end_unix, utc_offset_seconds,
		       raw_value, source_name, source_version, device, metadata_json
		FROM health_samples
		WHERE healthkit_type LIKE 'HKCategoryTypeIdentifier%'
		  AND healthkit_type NOT IN (
		    'HKCategoryTypeIdentifierSleepAnalysis',
		    'HKCategoryTypeIdentifierAppleStandHour',
		    'HKCategoryTypeIdentifierHandwashingEvent',
		    'HKCategoryTypeIdentifierToothbrushingEvent'
		  )`
	var args []any
	if contentHashFilter != "" {
		query += " AND content_hash = ?"
		args = append(args, contentHashFilter)
	}
	query += " ORDER BY start_unix ASC"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []storedNotableCategoryEvent
	for rows.Next() {
		var event storedNotableCategoryEvent
		if err := rows.Scan(
			&event.contentHash,
			&event.healthkitType,
			&event.calendarDate,
			&event.startUnix,
			&event.endUnix,
			&event.utcOffsetSeconds,
			&event.rawValue,
			&event.sourceName,
			&event.sourceVersion,
			&event.device,
			&event.metadataJSON,
		); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
