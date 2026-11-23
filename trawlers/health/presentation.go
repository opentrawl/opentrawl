package health

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const healthDetailAnchorIdentifier = "health-details"

// healthWorkoutActivityDisplayName turns a HealthKit case name such as
// "traditionalStrengthTraining" into "Traditional strength training".
func healthWorkoutActivityDisplayName(caseName string) string {
	caseName = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(caseName), "HKWorkoutActivityType"))
	if caseName == "" {
		return "Workout"
	}
	var words strings.Builder
	for index, character := range caseName {
		if index > 0 && unicode.IsUpper(character) {
			words.WriteRune(' ')
		}
		words.WriteRune(unicode.ToLower(character))
	}
	text := words.String()
	return strings.ToUpper(text[:1]) + text[1:]
}

func healthSleepStageDisplayName(stage contract.HealthSleepStage) string {
	switch stage {
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_IN_BED:
		return "In bed"
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_AWAKE:
		return "Awake"
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_CORE:
		return "Core sleep"
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_DEEP:
		return "Deep sleep"
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_REM:
		return "REM sleep"
	case contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_UNSPECIFIED,
		contract.HealthSleepStage_HEALTH_SLEEP_STAGE_UNSPECIFIED:
		return "Asleep"
	default:
		return "Asleep"
	}
}

func healthDurationText(duration time.Duration) string {
	duration = duration.Round(time.Minute)
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	if hours == 0 {
		return strconv.Itoa(minutes) + " min"
	}
	return strconv.Itoa(hours) + " h " + strconv.Itoa(minutes) + " min"
}

func formatHealthDecimal(value float64, decimals int) string {
	return strconv.FormatFloat(value, 'f', decimals, 64)
}

// healthMeasurement is one labelled value of a record, such as Distance 7.30 km.
type healthMeasurement struct {
	label string
	text  string
}

func healthWorkoutMeasurements(workout *contract.HealthWorkout) []healthMeasurement {
	measurements := []healthMeasurement{
		{label: "Duration", text: healthDurationText(time.Duration(workout.GetDurationSeconds() * float64(time.Second)))},
	}
	if workout.TotalDistanceMeters != nil {
		measurements = append(measurements, healthMeasurement{label: "Distance", text: formatHealthDecimal(workout.GetTotalDistanceMeters()/1000, 2) + " km"})
	}
	if workout.TotalEnergyBurnedKilocalories != nil {
		measurements = append(measurements, healthMeasurement{label: "Energy", text: formatHealthDecimal(workout.GetTotalEnergyBurnedKilocalories(), 0) + " kcal"})
	}
	if workout.AverageHeartRateBeatsPerMinute != nil {
		hrText := formatHealthDecimal(workout.GetAverageHeartRateBeatsPerMinute(), 0) + " bpm avg"
		if workout.MaxHeartRateBeatsPerMinute != nil {
			hrText += " (max " + formatHealthDecimal(workout.GetMaxHeartRateBeatsPerMinute(), 0) + " bpm)"
		}
		measurements = append(measurements, healthMeasurement{label: "Heart rate", text: hrText})
	} else if workout.MaxHeartRateBeatsPerMinute != nil {
		measurements = append(measurements, healthMeasurement{label: "Max heart rate", text: formatHealthDecimal(workout.GetMaxHeartRateBeatsPerMinute(), 0) + " bpm"})
	}
	if workout.AveragePowerWatts != nil {
		measurements = append(measurements, healthMeasurement{label: "Power", text: formatHealthDecimal(workout.GetAveragePowerWatts(), 0) + " W avg"})
	}
	if workout.TotalAscentMeters != nil && workout.GetTotalAscentMeters() > 0 {
		measurements = append(measurements, healthMeasurement{label: "Elevation gain", text: formatHealthDecimal(workout.GetTotalAscentMeters(), 0) + " m"})
	}
	if workout.AverageCadence != nil && workout.GetAverageCadence() > 0 {
		measurements = append(measurements, healthMeasurement{label: "Cadence", text: formatHealthDecimal(workout.GetAverageCadence(), 0) + " rpm"})
	}
	if workout.AverageSpeedMetersPerSecond != nil && workout.GetAverageSpeedMetersPerSecond() > 0 {
		kmh := workout.GetAverageSpeedMetersPerSecond() * 3.6
		measurements = append(measurements, healthMeasurement{label: "Speed", text: formatHealthDecimal(kmh, 1) + " km/h"})
	}
	if route := workout.GetRouteSummary(); route != nil && route.GetPointCount() > 0 {
		routeText := fmt.Sprintf("GPS route (%d points)", route.GetPointCount())
		if route.StartLatitude != nil && route.StartLongitude != nil {
			routeText = fmt.Sprintf("GPS %s,%s (%d pts)",
				formatHealthDecimal(route.GetStartLatitude(), 4),
				formatHealthDecimal(route.GetStartLongitude(), 4),
				route.GetPointCount())
		}
		measurements = append(measurements, healthMeasurement{label: "Route", text: routeText})
	}
	return measurements
}

func healthDailyMetricMeasurements(day *contract.HealthDailyMetrics) []healthMeasurement {
	var measurements []healthMeasurement
	if day.StepCount != nil {
		measurements = append(measurements, healthMeasurement{label: "Steps", text: strconv.FormatInt(day.GetStepCount(), 10) + " steps"})
	}
	if day.WalkingRunningDistanceMeters != nil {
		measurements = append(measurements, healthMeasurement{label: "Walked or run", text: formatHealthDecimal(day.GetWalkingRunningDistanceMeters()/1000, 2) + " km"})
	}
	if day.ActiveEnergyBurnedKilocalories != nil {
		measurements = append(measurements, healthMeasurement{label: "Active energy", text: formatHealthDecimal(day.GetActiveEnergyBurnedKilocalories(), 0) + " kcal"})
	}
	if day.ExerciseMinutes != nil {
		measurements = append(measurements, healthMeasurement{label: "Exercise", text: formatHealthDecimal(day.GetExerciseMinutes(), 0) + " min"})
	}
	if day.FlightsClimbed != nil {
		measurements = append(measurements, healthMeasurement{label: "Flights climbed", text: strconv.FormatInt(day.GetFlightsClimbed(), 10) + " flights"})
	}
	if day.RestingHeartRateBeatsPerMinute != nil {
		measurements = append(measurements, healthMeasurement{label: "Resting heart rate", text: formatHealthDecimal(day.GetRestingHeartRateBeatsPerMinute(), 0) + " bpm"})
	}
	if day.HeartRateVariabilitySdnnMilliseconds != nil {
		measurements = append(measurements, healthMeasurement{label: "Heart rate variability", text: formatHealthDecimal(day.GetHeartRateVariabilitySdnnMilliseconds(), 0) + " ms HRV"})
	}
	return measurements
}

func healthActivitySummaryMeasurements(summary *contract.HealthActivitySummary) []healthMeasurement {
	var measurements []healthMeasurement
	if summary.ActiveEnergyBurnedKilocalories != nil {
		text := formatHealthDecimal(summary.GetActiveEnergyBurnedKilocalories(), 0)
		if summary.ActiveEnergyBurnedGoalKilocalories != nil && summary.GetActiveEnergyBurnedGoalKilocalories() > 0 {
			text += "/" + formatHealthDecimal(summary.GetActiveEnergyBurnedGoalKilocalories(), 0)
		}
		measurements = append(measurements, healthMeasurement{label: "Move ring", text: text + " kcal"})
	}
	if summary.AppleExerciseTimeMinutes != nil {
		text := formatHealthDecimal(summary.GetAppleExerciseTimeMinutes(), 0)
		if summary.AppleExerciseTimeGoalMinutes != nil && summary.GetAppleExerciseTimeGoalMinutes() > 0 {
			text += "/" + formatHealthDecimal(summary.GetAppleExerciseTimeGoalMinutes(), 0)
		}
		measurements = append(measurements, healthMeasurement{label: "Exercise ring", text: text + " min"})
	}
	if summary.AppleStandHours != nil {
		text := formatHealthDecimal(summary.GetAppleStandHours(), 0)
		if summary.AppleStandHoursGoal != nil && summary.GetAppleStandHoursGoal() > 0 {
			text += "/" + formatHealthDecimal(summary.GetAppleStandHoursGoal(), 0)
		}
		measurements = append(measurements, healthMeasurement{label: "Stand ring", text: text + " h"})
	}
	return measurements
}

func healthCorrelationMeasurements(corr *contract.HealthCorrelation) []healthMeasurement {
	if corr.GetCorrelationTypeIdentifier() == "HKCorrelationTypeIdentifierBloodPressure" {
		var sys, dia, unit string
		for _, comp := range corr.GetComponentSamples() {
			switch comp.GetHealthkitTypeIdentifier() {
			case "HKQuantityTypeIdentifierBloodPressureSystolic":
				sys = comp.GetRawValue()
				unit = comp.GetUnit()
			case "HKQuantityTypeIdentifierBloodPressureDiastolic":
				dia = comp.GetRawValue()
			}
		}
		if sys != "" && dia != "" {
			if unit == "" {
				unit = "mmHg"
			}
			return []healthMeasurement{{label: "Blood pressure", text: sys + "/" + dia + " " + unit}}
		}
	}
	var measurements []healthMeasurement
	for _, comp := range corr.GetComponentSamples() {
		label := describeHealthMetricType(comp.GetHealthkitTypeIdentifier()).displayName
		val := strings.TrimSpace(comp.GetRawValue())
		if unit := normalizeDisplayUnit(comp.GetUnit()); unit != "" {
			val += " " + unit
		}
		measurements = append(measurements, healthMeasurement{label: label, text: val})
	}
	return measurements
}

func healthECGMeasurements(ecg *contract.HealthElectrocardiogram) []healthMeasurement {
	var measurements []healthMeasurement
	if cls := strings.TrimSpace(ecg.GetClassification()); cls != "" {
		measurements = append(measurements, healthMeasurement{label: "Classification", text: cls})
	}
	if ecg.AverageHeartRateBeatsPerMinute != nil {
		measurements = append(measurements, healthMeasurement{label: "Average heart rate", text: formatHealthDecimal(ecg.GetAverageHeartRateBeatsPerMinute(), 0) + " bpm"})
	}
	if ecg.SamplingFrequencyHertz != nil {
		measurements = append(measurements, healthMeasurement{label: "Sample rate", text: formatHealthDecimal(ecg.GetSamplingFrequencyHertz(), 0) + " Hz"})
	}
	if ecg.GetVoltageMeasurementCount() > 0 {
		measurements = append(measurements, healthMeasurement{label: "Waveform samples", text: strconv.Itoa(int(ecg.GetVoltageMeasurementCount())) + " measurements"})
	}
	if sym := strings.TrimSpace(ecg.GetSymptomsStatus()); sym != "" && !strings.EqualFold(sym, "None") {
		measurements = append(measurements, healthMeasurement{label: "Symptoms", text: sym})
	}
	return measurements
}

func healthAudiogramMeasurements(audiogram *contract.HealthAudiogram) []healthMeasurement {
	var measurements []healthMeasurement
	for _, pt := range audiogram.GetSensitivityPoints() {
		var ears []string
		if pt.LeftEarDecibelsHl != nil {
			ears = append(ears, "L "+formatHealthDecimal(pt.GetLeftEarDecibelsHl(), 0)+" dB")
		}
		if pt.RightEarDecibelsHl != nil {
			ears = append(ears, "R "+formatHealthDecimal(pt.GetRightEarDecibelsHl(), 0)+" dB")
		}
		if len(ears) > 0 {
			measurements = append(measurements, healthMeasurement{
				label: formatHealthDecimal(pt.GetFrequencyHertz(), 0) + " Hz",
				text:  strings.Join(ears, ", "),
			})
		}
	}
	return measurements
}

func healthClinicalMeasurements(clinical *contract.HealthClinicalRecord) []healthMeasurement {
	var measurements []healthMeasurement
	if typ := strings.TrimSpace(clinical.GetClinicalTypeIdentifier()); typ != "" {
		measurements = append(measurements, healthMeasurement{label: "Type", text: humanizeHealthKitIdentifier(typ)})
	}
	if fhir := strings.TrimSpace(clinical.GetFhirVersion()); fhir != "" {
		measurements = append(measurements, healthMeasurement{label: "FHIR", text: fhir})
	}
	if summary := conciseFHIRSummary(clinical.GetFhirResourceJson()); summary != "" {
		measurements = append(measurements, healthMeasurement{label: "Details", text: summary})
	}
	return measurements
}

type fhirQuickSummary struct {
	ResourceType  string `json:"resourceType"`
	Status        string `json:"status"`
	ValueString   string `json:"valueString"`
	ValueQuantity *struct {
		Value float64 `json:"value"`
		Unit  string  `json:"unit"`
	} `json:"valueQuantity"`
	ClinicalStatus *struct {
		Text string `json:"text"`
	} `json:"clinicalStatus"`
}

func conciseFHIRSummary(rawJSON string) string {
	if strings.TrimSpace(rawJSON) == "" {
		return ""
	}
	var parsed fhirQuickSummary
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return ""
	}
	var parts []string
	if parsed.ResourceType != "" {
		parts = append(parts, parsed.ResourceType)
	}
	if parsed.Status != "" {
		parts = append(parts, parsed.Status)
	}
	if parsed.ClinicalStatus != nil && parsed.ClinicalStatus.Text != "" {
		parts = append(parts, parsed.ClinicalStatus.Text)
	}
	if parsed.ValueQuantity != nil {
		parts = append(parts, strings.TrimSpace(strconv.FormatFloat(parsed.ValueQuantity.Value, 'f', -1, 64)+" "+parsed.ValueQuantity.Unit))
	} else if parsed.ValueString != "" {
		parts = append(parts, parsed.ValueString)
	}
	return strings.Join(parts, " · ")
}

func healthRecordMeasurements(record proto.Message) []healthMeasurement {
	switch typed := record.(type) {
	case *contract.HealthWorkout:
		return healthWorkoutMeasurements(typed)
	case *contract.HealthSleepSession:
		return []healthMeasurement{{label: "Duration", text: healthDurationText(typed.GetEndTime().AsTime().Sub(typed.GetStartTime().AsTime()))}}
	case *contract.HealthDailyMetrics:
		return healthDailyMetricMeasurements(typed)
	case *contract.HealthActivitySummary:
		return healthActivitySummaryMeasurements(typed)
	case *contract.HealthCorrelation:
		return healthCorrelationMeasurements(typed)
	case *contract.HealthElectrocardiogram:
		return healthECGMeasurements(typed)
	case *contract.HealthAudiogram:
		return healthAudiogramMeasurements(typed)
	case *contract.HealthClinicalRecord:
		return healthClinicalMeasurements(typed)
	default:
		return nil
	}
}

// healthRecordTitleAndFacts is the single wording of a stored health record,
// shared by search, lists, open, and the unified index.
func healthRecordTitleAndFacts(record proto.Message) (string, []string) {
	var facts []string
	for _, measurement := range healthRecordMeasurements(record) {
		facts = append(facts, measurement.text)
	}
	switch typed := record.(type) {
	case *contract.HealthWorkout:
		title := healthWorkoutActivityDisplayName(typed.GetHealthkitWorkoutActivityTypeName()) + " workout"
		if sub := strings.TrimSpace(typed.GetSportSubType()); sub != "" {
			title = healthWorkoutActivityDisplayName(typed.GetHealthkitWorkoutActivityTypeName()) + " (" + strings.ReplaceAll(sub, "_", " ") + ")"
		}
		return title, facts
	case *contract.HealthSleepSession:
		return healthSleepStageDisplayName(typed.GetSleepStage()), facts
	case *contract.HealthDailyMetrics:
		return "Daily health " + typed.GetCalendarDate(), facts
	case *contract.HealthActivitySummary:
		return "Activity rings " + typed.GetCalendarDate(), facts
	case *contract.HealthCorrelation:
		return humanizeHealthKitIdentifier(typed.GetCorrelationTypeIdentifier()), facts
	case *contract.HealthElectrocardiogram:
		if cls := strings.TrimSpace(typed.GetClassification()); cls != "" {
			return "ECG — " + cls, facts
		}
		return "Electrocardiogram", facts
	case *contract.HealthAudiogram:
		var freqFacts []string
		for _, measurement := range healthAudiogramMeasurements(typed) {
			freqFacts = append(freqFacts, measurement.label+": "+measurement.text)
		}
		return fmt.Sprintf("Audiogram (%d frequencies)", len(typed.GetSensitivityPoints())), freqFacts
	case *contract.HealthClinicalRecord:
		if name := strings.TrimSpace(typed.GetDisplayName()); name != "" {
			return name, facts
		}
		return humanizeHealthKitIdentifier(typed.GetClinicalTypeIdentifier()), facts
	default:
		return "Health record", facts
	}
}

func healthRecordSourceName(record proto.Message) string {
	switch typed := record.(type) {
	case *contract.HealthWorkout:
		if len(typed.GetContributingSources()) > 0 {
			return summarizeWorkoutProvenance(typed.GetContributingSources())
		}
		return typed.GetSourceName()
	case *contract.HealthSleepSession:
		return typed.GetSourceName()
	case *contract.HealthCorrelation:
		return typed.GetSourceName()
	case *contract.HealthElectrocardiogram:
		return typed.GetSourceName()
	case *contract.HealthAudiogram:
		return typed.GetSourceName()
	case *contract.HealthClinicalRecord:
		return typed.GetSourceName()
	default:
		return ""
	}
}

func healthRecordSearchableText(record proto.Message) string {
	title, facts := healthRecordTitleAndFacts(record)
	var extra []string
	switch typed := record.(type) {
	case *contract.HealthWorkout:
		extra = append(extra, "workout exercise", typed.GetHealthkitWorkoutActivityTypeName(), typed.GetSportSubType(), typed.GetDevice())
		if normalizeWorkoutSportFamily(typed.GetHealthkitWorkoutActivityTypeName()) == "cycling" {
			extra = append(extra, "cycling ride bike bicycle")
		}
		if normalizeWorkoutSportFamily(typed.GetHealthkitWorkoutActivityTypeName()) == "running" {
			extra = append(extra, "running run jog")
		}
	case *contract.HealthSleepSession:
		extra = append(extra, "sleep")
	case *contract.HealthDailyMetrics:
		extra = append(extra, "steps activity health daily metrics", typed.GetCalendarDate())
	case *contract.HealthActivitySummary:
		extra = append(extra, "activity rings move exercise stand", typed.GetCalendarDate())
	case *contract.HealthCorrelation:
		extra = append(extra, "correlation", typed.GetCorrelationTypeIdentifier())
		if typed.GetCorrelationTypeIdentifier() == "HKCorrelationTypeIdentifierBloodPressure" {
			extra = append(extra, "blood pressure systolic diastolic")
		}
	case *contract.HealthElectrocardiogram:
		extra = append(extra, "ecg ekg electrocardiogram heart rhythm", typed.GetClassification(), typed.GetSymptomsStatus(), typed.GetDevice())
	case *contract.HealthAudiogram:
		extra = append(extra, "audiogram hearing test decibels")
	case *contract.HealthClinicalRecord:
		extra = append(extra, "clinical medical record fhir", typed.GetClinicalTypeIdentifier(), typed.GetDisplayName())
	}
	parts := append([]string{title, healthRecordSourceName(record)}, extra...)
	parts = append(parts, facts...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func formatSleepNightBreakdown(night sleepNightRollup) string {
	var parts []string
	if night.coreDuration > 0 {
		parts = append(parts, "Core "+healthDurationText(night.coreDuration))
	}
	if night.deepDuration > 0 {
		parts = append(parts, "Deep "+healthDurationText(night.deepDuration))
	}
	if night.remDuration > 0 {
		parts = append(parts, "REM "+healthDurationText(night.remDuration))
	}
	if night.awakeDuration > 0 {
		parts = append(parts, "Awake "+healthDurationText(night.awakeDuration))
	}
	if len(parts) == 0 && night.inBedDuration > 0 {
		parts = append(parts, "In bed "+healthDurationText(night.inBedDuration))
	}
	return strings.Join(parts, " · ")
}

func formatSleepNightWindow(night sleepNightRollup) string {
	loc := time.Local
	if night.utcOffset != 0 {
		loc = time.FixedZone("", int(night.utcOffset))
	}
	start := time.Unix(night.bedtimeUnix, 0).In(loc).Format("15:04")
	end := time.Unix(night.wakeUnix, 0).In(loc).Format("15:04")
	return start + "–" + end
}

func formatDailyRollupFacts(day dailyHealthDayRollup) (activityFacts []string, vitalFacts []string, allFacts []string, metricKeywords []string) {
	seenLabel := map[string]struct{}{}
	addFact := func(target *[]string, label, text string) {
		if text == "" {
			return
		}
		key := strings.ToLower(label)
		if _, dup := seenLabel[key]; dup {
			return
		}
		seenLabel[key] = struct{}{}
		*target = append(*target, text)
		allFacts = append(allFacts, text)
		metricKeywords = append(metricKeywords, label)
	}

	// Activity summary rings first.
	if day.activitySummary != nil {
		for _, m := range healthActivitySummaryMeasurements(day.activitySummary) {
			addFact(&activityFacts, m.label, m.label+" "+m.text)
		}
	}
	// Explicit HealthDailyMetrics from iOS batches.
	if day.dailyMetrics != nil {
		for _, m := range healthDailyMetricMeasurements(day.dailyMetrics) {
			if strings.Contains(strings.ToLower(m.label), "heart") {
				addFact(&vitalFacts, m.label, m.text)
			} else {
				addFact(&activityFacts, m.label, m.text)
			}
		}
	}
	// Sample daily aggregates from health_samples (richer Apple Health metrics).
	for _, agg := range day.sampleMetrics {
		desc := describeHealthMetricType(agg.healthkitType)
		formatted := formatDailyMetricAggregateValue(agg)
		label := desc.displayName
		metricKeywords = append(metricKeywords, label, agg.healthkitType)
		switch agg.healthkitType {
		case "HKQuantityTypeIdentifierStepCount":
			addFact(&activityFacts, "Steps", formatted+" steps")
		case "HKQuantityTypeIdentifierDistanceWalkingRunning":
			addFact(&activityFacts, "Walked or run", formatted)
		case "HKQuantityTypeIdentifierDistanceCycling":
			addFact(&activityFacts, "Cycling distance", formatted+" cycling")
		case "HKQuantityTypeIdentifierActiveEnergyBurned":
			addFact(&activityFacts, "Active energy", formatted+" active")
		case "HKQuantityTypeIdentifierAppleExerciseTime":
			addFact(&activityFacts, "Exercise", formatted+" exercise")
		case "HKQuantityTypeIdentifierFlightsClimbed":
			addFact(&activityFacts, "Flights climbed", formatted+" flights")
		case "HKQuantityTypeIdentifierRestingHeartRate":
			addFact(&vitalFacts, "Resting heart rate", formatted+" resting HR")
		case "HKQuantityTypeIdentifierHeartRateVariabilitySDNN":
			addFact(&vitalFacts, "Heart rate variability", formatted+" HRV")
		case "HKQuantityTypeIdentifierHeartRate":
			addFact(&vitalFacts, "Heart rate", "HR "+formatted)
		case "HKQuantityTypeIdentifierVO2Max":
			addFact(&vitalFacts, "VO2 max", "VO2 max "+formatted)
		case "HKQuantityTypeIdentifierBodyMass":
			addFact(&vitalFacts, "Weight", formatted)
		case "HKQuantityTypeIdentifierBodyFatPercentage":
			addFact(&vitalFacts, "Body fat", formatted+" body fat")
		case "HKQuantityTypeIdentifierOxygenSaturation":
			addFact(&vitalFacts, "Blood oxygen", "SpO2 "+formatted)
		case "HKQuantityTypeIdentifierRespiratoryRate":
			addFact(&vitalFacts, "Respiratory rate", formatted+" resp")
		default:
			if desc.summaryRank <= 25 {
				addFact(&vitalFacts, label, label+" "+formatted)
			}
		}
	}
	return activityFacts, vitalFacts, allFacts, metricKeywords
}

func textValue(value string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: value}}
}

func exactTimeValue(unixSeconds int64) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: healthAssociatedTime(unixSeconds, false, ""),
		},
	}
}

func calendarDateValue(calendarDate string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTimeForDisplay: healthAssociatedTime(0, true, calendarDate),
		},
	}
}

func healthAssociatedTime(unixSeconds int64, calendarDateOnly bool, calendarDate string) *presentation.ArchiveRecordAssociatedTimeForDisplay {
	if calendarDateOnly {
		parsed, err := time.Parse("2006-01-02", calendarDate)
		if err == nil {
			return &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
				CalendarDate: &presentation.CalendarDate{CalendarYear: int32(parsed.Year()), CalendarMonthNumber: int32(parsed.Month()), CalendarDayOfMonth: int32(parsed.Day())},
			}}
		}
	}
	return &presentation.ArchiveRecordAssociatedTimeForDisplay{ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(time.Unix(unixSeconds, 0))}}
}

func refValue(reference string) *presentation.TrawlerSpecificCommandPresentationValue {
	return &presentation.TrawlerSpecificCommandPresentationValue{
		TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_CanonicalRecordReference{CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(reference)},
	}
}

func healthListResponse(emptyText string, columns []string, rows []*presentation.TrawlerSpecificCommandListPresentationRow, footnotes []string) *command.TrawlerCommandResponse {
	list := &presentation.TrawlerSpecificCommandListPresentation{ConciseTextShownWhenListIsEmpty: emptyText, FootnotesInDisplayOrder: footnotes}
	if len(rows) > 0 {
		list.SectionsInDisplayOrder = []*presentation.TrawlerSpecificCommandListSection{{ColumnDisplayNamesInOrder: columns, RowsInDisplayOrder: rows}}
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{TrawlerSpecificCommandListPresentation: list},
			},
		},
	}
}

func buildHealthWorkoutsPresentation(records []storedHealthRecord) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(records))
	for _, stored := range records {
		title, facts := healthRecordTitleAndFacts(stored.record)
		source := healthRecordSourceName(stored.record)
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				exactTimeValue(stored.startUnix),
				textValue(title),
				textValue(strings.Join(facts, " · ")),
				textValue(source),
				refValue(stored.reference),
			},
		})
	}
	return healthListResponse("No workouts are archived yet.", []string{"when", "workout", "details", "source", "link"}, rows, nil)
}

func buildHealthSleepNightsPresentation(nights []sleepNightRollup) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(nights))
	for _, night := range nights {
		asleepText := healthDurationText(night.totalAsleep)
		if night.totalAsleep == 0 && night.inBedDuration > 0 {
			asleepText = healthDurationText(night.inBedDuration) + " in bed"
		}
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				calendarDateValue(night.calendarDate),
				textValue(asleepText),
				textValue(formatSleepNightBreakdown(night)),
				textValue(formatSleepNightWindow(night)),
				refValue(healthSleepNightReference(night.calendarDate)),
			},
		})
	}
	return healthListResponse("No sleep is archived yet.", []string{"night", "asleep", "stages", "window", "link"}, rows,
		[]string{"Use --stages to inspect individual sleep stage intervals."})
}

func buildHealthSleepStagesPresentation(records []storedHealthRecord) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(records))
	for _, stored := range records {
		title, facts := healthRecordTitleAndFacts(stored.record)
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				exactTimeValue(stored.startUnix),
				textValue(title),
				textValue(strings.Join(facts, " · ")),
				textValue(healthRecordSourceName(stored.record)),
				refValue(stored.reference),
			},
		})
	}
	return healthListResponse("No sleep stages are archived yet.", []string{"when", "stage", "duration", "source", "link"}, rows, nil)
}

func buildHealthMetricsCatalogPresentation(catalog []healthMetricCatalogRow) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(catalog))
	for _, entry := range catalog {
		dateSpan := entry.firstDate
		if entry.lastDate != "" && entry.lastDate != entry.firstDate {
			dateSpan = entry.firstDate + " to " + entry.lastDate
		}
		latestText := strings.TrimSpace(entry.latestRaw)
		if entry.latestNumeric.Valid {
			desc := describeHealthMetricType(entry.healthkitType)
			if entry.healthkitType == "HKQuantityTypeIdentifierOxygenSaturation" || entry.healthkitType == "HKQuantityTypeIdentifierBodyFatPercentage" {
				latestText = formatPercentageSampleValue(entry.latestNumeric.Float64, entry.unit, desc.decimals)
			} else {
				latestText = formatMetricNumber(entry.latestNumeric.Float64, desc.decimals)
				if u := normalizeMetricDisplayUnit(entry.healthkitType, entry.unit); u != "" {
					latestText += " " + u
				}
			}
		} else if strings.HasPrefix(latestText, "HKCategoryValue") {
			latestText = humanizeHealthKitIdentifier(latestText)
		}
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				textValue(entry.displayName),
				textValue(strconv.FormatUint(entry.sampleCount, 10)),
				textValue(dateSpan),
				textValue(latestText),
			},
		})
	}
	return healthListResponse(
		"No Apple Health metric samples are archived yet. Import an Apple Health export.zip with: trawl health import PATH",
		[]string{"metric", "samples", "range", "latest"},
		rows,
		[]string{"Run trawl health metrics --type NAME to inspect daily rollups for one metric."},
	)
}

func buildHealthMetricTrendPresentation(metricQuery string, aggs []dailySampleMetricAggregate) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(aggs))
	for _, agg := range aggs {
		desc := describeHealthMetricType(agg.healthkitType)
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				calendarDateValue(agg.calendarDate),
				textValue(desc.displayName),
				textValue(formatDailyMetricAggregateValue(agg)),
				textValue(strconv.FormatInt(agg.sampleCount, 10)),
				refValue(healthRecordReference(healthRecordKindDailyMetrics, agg.calendarDate)),
			},
		})
	}
	return healthListResponse(
		fmt.Sprintf("No archived metric samples matched %q.", metricQuery),
		[]string{"day", "metric", "value", "samples", "link"},
		rows,
		nil,
	)
}

func buildHealthSummaryPresentation(days []dailyHealthDayRollup) *command.TrawlerCommandResponse {
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(days))
	for _, day := range days {
		activityFacts, vitalFacts, _, _ := formatDailyRollupFacts(day)
		sleepText := ""
		if day.sleepNight != nil && day.sleepNight.totalAsleep > 0 {
			sleepText = healthDurationText(day.sleepNight.totalAsleep)
		}
		workoutNames := make([]string, 0, len(day.workouts))
		for _, w := range day.workouts {
			title, _ := healthRecordTitleAndFacts(w.record)
			workoutNames = append(workoutNames, title)
		}
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				calendarDateValue(day.calendarDate),
				textValue(strings.Join(activityFacts, " · ")),
				textValue(strings.Join(vitalFacts, " · ")),
				textValue(sleepText),
				textValue(strings.Join(workoutNames, ", ")),
				refValue(healthRecordReference(healthRecordKindDailyMetrics, day.calendarDate)),
			},
		})
	}
	return healthListResponse(
		"No daily health history is archived yet.",
		[]string{"day", "activity", "vitals", "sleep", "workouts", "link"},
		rows,
		[]string{"Sleep counts stages that ended on that day."},
	)
}

func buildHealthImportReportPresentation(sourcePath string, stats healthImportStatistics) *command.TrawlerCommandResponse {
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
		{FieldDisplayName: "Source", FieldValue: textValue(sourcePath)},
		{FieldDisplayName: "Files processed", FieldValue: textValue(strconv.FormatUint(stats.filesProcessed, 10))},
	}
	if stats.filesSkipped > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Already imported",
			FieldValue:       textValue(strconv.FormatUint(stats.filesSkipped, 10) + " files skipped"),
		})
	}
	if stats.samplesAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Samples added",
			FieldValue:       textValue(strconv.FormatUint(stats.samplesAdded, 10)),
		})
	}
	if stats.workoutsAdded > 0 || stats.workoutsUpdated > 0 || stats.workoutsDeduplicated > 0 {
		text := fmt.Sprintf("%d added, %d updated", stats.workoutsAdded, stats.workoutsUpdated)
		if stats.workoutsDeduplicated > 0 {
			text += fmt.Sprintf(" (%d deduplicated across sources)", stats.workoutsDeduplicated)
		}
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Workouts",
			FieldValue:       textValue(text),
		})
	}
	if stats.sleepStagesAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Sleep stages added",
			FieldValue:       textValue(strconv.FormatUint(stats.sleepStagesAdded, 10)),
		})
	}
	if stats.activitySummariesAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Activity ring days",
			FieldValue:       textValue(strconv.FormatUint(stats.activitySummariesAdded, 10)),
		})
	}
	if stats.correlationsAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Correlations added",
			FieldValue:       textValue(strconv.FormatUint(stats.correlationsAdded, 10)),
		})
	}
	if stats.ecgsAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "ECGs added",
			FieldValue:       textValue(strconv.FormatUint(stats.ecgsAdded, 10)),
		})
	}
	if stats.audiogramsAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Audiograms added",
			FieldValue:       textValue(strconv.FormatUint(stats.audiogramsAdded, 10)),
		})
	}
	if stats.clinicalRecordsAdded > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Clinical records",
			FieldValue:       textValue(strconv.FormatUint(stats.clinicalRecordsAdded, 10)),
		})
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandDetailPresentation{
					TrawlerSpecificCommandDetailPresentation: &presentation.TrawlerSpecificCommandDetailPresentation{
						DetailDisplayName:       "Health import completed",
						DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
						FieldsInDisplayOrder:    fields,
					},
				},
			},
		},
	}
}

func buildOpenedHealthRecordPresentation(stored storedHealthRecord) *open.TrawlerSpecificOpenedRecordPresentation {
	title, _ := healthRecordTitleAndFacts(stored.record)
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{}
	if stored.kind == healthRecordKindDailyMetrics || stored.kind == healthRecordKindActivitySummary {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Day", FieldValue: calendarDateValue(stored.calendarDate)})
	} else {
		if stored.startUnix > 0 {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Start", FieldValue: exactTimeValue(stored.startUnix)})
		}
		if stored.endUnix > 0 && stored.endUnix != stored.startUnix {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "End", FieldValue: exactTimeValue(stored.endUnix)})
		}
	}
	for _, measurement := range healthRecordMeasurements(stored.record) {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: measurement.label, FieldValue: textValue(measurement.text)})
	}
	var bodyLines []string
	switch typed := stored.record.(type) {
	case *contract.HealthWorkout:
		if route := typed.GetRouteSummary(); route != nil && route.GetPointCount() > 0 {
			if route.GetRouteFileReference() != "" {
				fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Route file", FieldValue: textValue(route.GetRouteFileReference())})
			}
			if route.MinLatitude != nil && route.MaxLatitude != nil && route.MinLongitude != nil && route.MaxLongitude != nil {
				bounds := fmt.Sprintf("lat %s..%s, lon %s..%s",
					formatHealthDecimal(route.GetMinLatitude(), 4),
					formatHealthDecimal(route.GetMaxLatitude(), 4),
					formatHealthDecimal(route.GetMinLongitude(), 4),
					formatHealthDecimal(route.GetMaxLongitude(), 4))
				fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Route bounds", FieldValue: textValue(bounds)})
			}
		}
		if len(typed.GetEvents()) > 0 {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Events / laps", FieldValue: textValue(strconv.Itoa(len(typed.GetEvents())))})
		}
		for _, stat := range typed.GetStatistics() {
			label := humanizeHealthKitIdentifier(stat.GetQuantityTypeIdentifier())
			var vals []string
			if stat.SumValue != nil {
				vals = append(vals, "sum "+formatHealthDecimal(stat.GetSumValue(), 1))
			}
			if stat.AverageValue != nil {
				vals = append(vals, "avg "+formatHealthDecimal(stat.GetAverageValue(), 1))
			}
			if stat.MinimumValue != nil {
				vals = append(vals, "min "+formatHealthDecimal(stat.GetMinimumValue(), 1))
			}
			if stat.MaximumValue != nil {
				vals = append(vals, "max "+formatHealthDecimal(stat.GetMaximumValue(), 1))
			}
			if len(vals) > 0 {
				bodyLines = append(bodyLines, label+": "+strings.Join(vals, ", ")+" "+normalizeDisplayUnit(stat.GetUnit()))
			}
		}
	case *contract.HealthElectrocardiogram:
		if typed.GetCsvFileReference() != "" {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Waveform CSV", FieldValue: textValue(typed.GetCsvFileReference())})
		}
	case *contract.HealthClinicalRecord:
		if typed.GetResourceFileReference() != "" {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Resource file", FieldValue: textValue(typed.GetResourceFileReference())})
		}
		if raw := strings.TrimSpace(typed.GetFhirResourceJson()); raw != "" {
			if len(raw) > 2000 {
				raw = raw[:2000] + "\n..."
			}
			bodyLines = append(bodyLines, raw)
		}
	}
	source := healthRecordSourceName(stored.record)
	if source != "" {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Recorded by", FieldValue: textValue(source)})
	}
	if device := strings.TrimSpace(stored.deviceName); device != "" && device != source {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Device", FieldValue: textValue(device)})
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{
		DetailPresentation: healthDetailPresentation(title, fields, strings.Join(bodyLines, "\n")),
	}
}

func healthDetailPresentation(
	title string,
	fields []*presentation.TrawlerSpecificCommandDetailPresentationField,
	bodyText string,
) *presentation.TrawlerSpecificCommandDetailPresentation {
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:       title,
		DetailDisplayNameAnchor: trawlkit.NewRecordAnchorIdentifier(healthDetailAnchorIdentifier),
		FieldsInDisplayOrder:    fields,
	}
	if strings.TrimSpace(bodyText) != "" {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: bodyText}
	}
	return detail
}

func buildOpenedDailyHealthPresentation(day dailyHealthDayRollup) *open.TrawlerSpecificOpenedRecordPresentation {
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
		{FieldDisplayName: "Day", FieldValue: calendarDateValue(day.calendarDate)},
	}
	if day.activitySummary != nil {
		for _, m := range healthActivitySummaryMeasurements(day.activitySummary) {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: m.label, FieldValue: textValue(m.text)})
		}
	}
	if day.dailyMetrics != nil {
		for _, m := range healthDailyMetricMeasurements(day.dailyMetrics) {
			fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: m.label, FieldValue: textValue(m.text)})
		}
	}
	if day.sleepNight != nil && day.sleepNight.totalAsleep > 0 {
		sleepDetail := healthDurationText(day.sleepNight.totalAsleep)
		if breakdown := formatSleepNightBreakdown(*day.sleepNight); breakdown != "" {
			sleepDetail += " (" + breakdown + ")"
		}
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Sleep", FieldValue: textValue(sleepDetail)})
	}
	for _, w := range day.workouts {
		title, facts := healthRecordTitleAndFacts(w.record)
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{
			FieldDisplayName: "Workout",
			FieldValue:       textValue(title + " — " + strings.Join(facts, " · ")),
		})
	}
	var metricLines []string
	for _, agg := range day.sampleMetrics {
		desc := describeHealthMetricType(agg.healthkitType)
		sampleWord := "samples"
		if agg.sampleCount == 1 {
			sampleWord = "sample"
		}
		metricLines = append(metricLines, fmt.Sprintf("%s: %s (%d %s)", desc.displayName, formatDailyMetricAggregateValue(agg), agg.sampleCount, sampleWord))
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{
		DetailPresentation: healthDetailPresentation("Daily health "+day.calendarDate, fields, strings.Join(metricLines, "\n")),
	}
}

func buildOpenedSleepNightPresentation(night sleepNightRollup) *open.TrawlerSpecificOpenedRecordPresentation {
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
		{FieldDisplayName: "Night", FieldValue: calendarDateValue(night.calendarDate)},
		{FieldDisplayName: "Bedtime", FieldValue: exactTimeValue(night.bedtimeUnix)},
		{FieldDisplayName: "Wake time", FieldValue: exactTimeValue(night.wakeUnix)},
		{FieldDisplayName: "Total asleep", FieldValue: textValue(healthDurationText(night.totalAsleep))},
	}
	if night.coreDuration > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Core sleep", FieldValue: textValue(healthDurationText(night.coreDuration))})
	}
	if night.deepDuration > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Deep sleep", FieldValue: textValue(healthDurationText(night.deepDuration))})
	}
	if night.remDuration > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "REM sleep", FieldValue: textValue(healthDurationText(night.remDuration))})
	}
	if night.awakeDuration > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Awake", FieldValue: textValue(healthDurationText(night.awakeDuration))})
	}
	if night.inBedDuration > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "In bed", FieldValue: textValue(healthDurationText(night.inBedDuration))})
	}
	if len(night.sources) > 0 {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Recorded by", FieldValue: textValue(strings.Join(night.sources, ", "))})
	}
	loc := time.Local
	if night.utcOffset != 0 {
		loc = time.FixedZone("", int(night.utcOffset))
	}
	var stageLines []string
	for _, st := range night.stages {
		sess := st.record.(*contract.HealthSleepSession)
		dur := time.Duration(max(st.endUnix-st.startUnix, 0)) * time.Second
		stageLines = append(stageLines, fmt.Sprintf("%s–%s  %s (%s)",
			time.Unix(st.startUnix, 0).In(loc).Format("15:04"),
			time.Unix(st.endUnix, 0).In(loc).Format("15:04"),
			healthSleepStageDisplayName(sess.GetSleepStage()),
			healthDurationText(dur),
		))
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{
		DetailPresentation: healthDetailPresentation("Sleep "+night.calendarDate, fields, strings.Join(stageLines, "\n")),
	}
}

func buildOpenedNotableEventPresentation(event storedNotableCategoryEvent) *open.TrawlerSpecificOpenedRecordPresentation {
	title := describeHealthMetricType(event.healthkitType).displayName
	fields := []*presentation.TrawlerSpecificCommandDetailPresentationField{
		{FieldDisplayName: "Start", FieldValue: exactTimeValue(event.startUnix)},
	}
	if event.endUnix > event.startUnix {
		fields = append(fields,
			&presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "End", FieldValue: exactTimeValue(event.endUnix)},
			&presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Duration", FieldValue: textValue(healthDurationText(time.Duration(event.endUnix-event.startUnix) * time.Second))},
		)
	}
	if raw := strings.TrimSpace(event.rawValue); raw != "" {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Value", FieldValue: textValue(humanizeHealthKitIdentifier(raw))})
	}
	if event.sourceName != "" {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Recorded by", FieldValue: textValue(event.sourceName)})
	}
	if event.device != "" {
		fields = append(fields, &presentation.TrawlerSpecificCommandDetailPresentationField{FieldDisplayName: "Device", FieldValue: textValue(event.device)})
	}
	var metaLines []string
	for _, entry := range parseMetadataJSON(event.metadataJSON) {
		metaLines = append(metaLines, entry.GetKey()+": "+entry.GetValue())
	}
	return &open.TrawlerSpecificOpenedRecordPresentation{
		DetailPresentation: healthDetailPresentation(title, fields, strings.Join(metaLines, "\n")),
	}
}
