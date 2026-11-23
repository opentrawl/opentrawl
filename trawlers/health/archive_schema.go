// Package health archives Apple Health exports, HealthFit .fit workout files,
// and incremental HealthKit device batches into one local SQLite archive and
// emits bounded high-signal records into the unified OpenTrawl search index.
package health

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const healthArchiveSchema = `
CREATE TABLE IF NOT EXISTS health_samples (
  content_hash TEXT PRIMARY KEY,
  sample_uuid TEXT NOT NULL DEFAULT '',
  healthkit_type TEXT NOT NULL,
  calendar_date TEXT NOT NULL,
  start_unix INTEGER NOT NULL,
  end_unix INTEGER NOT NULL,
  creation_unix INTEGER NOT NULL DEFAULT 0,
  utc_offset_seconds INTEGER NOT NULL DEFAULT 0,
  unit TEXT NOT NULL DEFAULT '',
  raw_value TEXT NOT NULL DEFAULT '',
  numeric_value REAL,
  source_name TEXT NOT NULL DEFAULT '',
  source_version TEXT NOT NULL DEFAULT '',
  device TEXT NOT NULL DEFAULT '',
  metadata_json TEXT NOT NULL DEFAULT '',
  import_origin TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_health_samples_type_start ON health_samples(healthkit_type, start_unix DESC);
CREATE INDEX IF NOT EXISTS idx_health_samples_type_date ON health_samples(healthkit_type, calendar_date DESC);
CREATE INDEX IF NOT EXISTS idx_health_samples_date ON health_samples(calendar_date DESC);

CREATE TABLE IF NOT EXISTS health_records (
  reference TEXT PRIMARY KEY,
  record_kind TEXT NOT NULL,
  start_unix INTEGER NOT NULL,
  end_unix INTEGER NOT NULL,
  utc_offset_seconds INTEGER NOT NULL DEFAULT 0,
  calendar_date TEXT NOT NULL DEFAULT '',
  record BLOB NOT NULL,
  device_name TEXT NOT NULL DEFAULT '',
  exported_at TEXT NOT NULL DEFAULT '',
  source_provenance TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_health_records_kind_start ON health_records(record_kind, start_unix DESC);
CREATE INDEX IF NOT EXISTS idx_health_records_kind_date ON health_records(record_kind, calendar_date DESC);

CREATE TABLE IF NOT EXISTS health_workout_aliases (
  alias_key TEXT PRIMARY KEY,
  workout_reference TEXT NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS health_search USING fts5(reference UNINDEXED, body);

CREATE TABLE IF NOT EXISTS health_imports (
  name TEXT PRIMARY KEY,
  import_kind TEXT NOT NULL DEFAULT '',
  source_path TEXT NOT NULL DEFAULT '',
  content_digest TEXT NOT NULL DEFAULT '',
  imported_at TEXT NOT NULL DEFAULT '',
  records_added INTEGER NOT NULL DEFAULT 0,
  records_updated INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS health_update_completion (
  singleton INTEGER PRIMARY KEY,
  completed_time TEXT NOT NULL
);
`

// healthRecordKind identifies one typed protobuf record category in health_records.
type healthRecordKind string

const (
	healthRecordKindWorkout         healthRecordKind = "workout"
	healthRecordKindSleepSession    healthRecordKind = "sleep"
	healthRecordKindDailyMetrics    healthRecordKind = "day"
	healthRecordKindCorrelation     healthRecordKind = "correlation"
	healthRecordKindActivitySummary healthRecordKind = "activity_summary"
	healthRecordKindECG             healthRecordKind = "ecg"
	healthRecordKindAudiogram       healthRecordKind = "audiogram"
	healthRecordKindClinical        healthRecordKind = "clinical"

	// Derived canonical reference prefixes resolved dynamically on open/index.
	healthReferencePrefixSleepNight = "health:sleep-night/"
	healthReferencePrefixEvent      = "health:event/"

	healthImportPrototextFileName = "import.txtpb"
	healthImportJSONFileName      = "import.json"
)

var healthCalendarDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// healthImportStatistics summarizes one import or replay operation.
type healthImportStatistics struct {
	samplesAdded           uint64
	workoutsAdded          uint64
	workoutsUpdated        uint64
	workoutsDeduplicated   uint64
	sleepStagesAdded       uint64
	dailyMetricsAdded      uint64
	dailyMetricsUpdated    uint64
	correlationsAdded      uint64
	activitySummariesAdded uint64
	ecgsAdded              uint64
	audiogramsAdded        uint64
	clinicalRecordsAdded   uint64
	filesProcessed         uint64
	filesSkipped           uint64
}

func (s healthImportStatistics) totalAdded() uint64 {
	return s.samplesAdded + s.workoutsAdded + s.sleepStagesAdded +
		s.dailyMetricsAdded + s.correlationsAdded + s.activitySummariesAdded +
		s.ecgsAdded + s.audiogramsAdded + s.clinicalRecordsAdded
}

func (s healthImportStatistics) totalUpdated() uint64 {
	return s.workoutsUpdated + s.dailyMetricsUpdated
}

func (s *healthImportStatistics) merge(other healthImportStatistics) {
	s.samplesAdded += other.samplesAdded
	s.workoutsAdded += other.workoutsAdded
	s.workoutsUpdated += other.workoutsUpdated
	s.workoutsDeduplicated += other.workoutsDeduplicated
	s.sleepStagesAdded += other.sleepStagesAdded
	s.dailyMetricsAdded += other.dailyMetricsAdded
	s.dailyMetricsUpdated += other.dailyMetricsUpdated
	s.correlationsAdded += other.correlationsAdded
	s.activitySummariesAdded += other.activitySummariesAdded
	s.ecgsAdded += other.ecgsAdded
	s.audiogramsAdded += other.audiogramsAdded
	s.clinicalRecordsAdded += other.clinicalRecordsAdded
	s.filesProcessed += other.filesProcessed
	s.filesSkipped += other.filesSkipped
}

func ensureHealthArchiveSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, healthArchiveSchema)
	return err
}

// healthArchiveTablesExist reports whether the health archive schema has been
// initialised, without issuing write DDL so read-only queries work inside a
// read-only SQLite connection and Seatbelt sandbox.
func healthArchiveTablesExist(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('health_samples', 'health_records', 'health_update_completion')",
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 3, nil
}

func healthRecordReference(kind healthRecordKind, identifier string) string {
	return "health:" + string(kind) + "/" + strings.ToLower(strings.TrimSpace(identifier))
}

func healthSleepNightReference(calendarDate string) string {
	return healthReferencePrefixSleepNight + strings.TrimSpace(calendarDate)
}

func healthNotableEventReference(contentHash string) string {
	return healthReferencePrefixEvent + strings.ToLower(strings.TrimSpace(contentHash))
}

func deterministicHealthHash(parts ...string) string {
	hasher := sha256.New()
	for _, part := range parts {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil))[:24]
}

func healthSampleContentHash(sample *contract.HealthSample) string {
	if uuid := strings.TrimSpace(sample.GetHealthkitSampleUuid()); uuid != "" {
		return "uuid:" + strings.ToLower(uuid)
	}
	return deterministicHealthHash(
		sample.GetHealthkitTypeIdentifier(),
		formatUnixForHash(sample.GetStartTime()),
		formatUnixForHash(sample.GetEndTime()),
		formatUnixForHash(sample.GetCreationTime()),
		sample.GetUnit(),
		sample.GetRawValue(),
		sample.GetSourceName(),
		sample.GetSourceVersion(),
		sample.GetDevice(),
		canonicalMetadataJSON(sample.GetMetadataEntries()),
	)
}

func formatUnixForHash(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return "0"
	}
	return fmt.Sprintf("%d.%09d", ts.GetSeconds(), ts.GetNanos())
}

type serializedMetadataPair struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

func canonicalMetadataJSON(entries []*contract.HealthMetadataEntry) string {
	if len(entries) == 0 {
		return ""
	}
	pairs := make([]serializedMetadataPair, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		key := strings.TrimSpace(entry.GetKey())
		if key == "" {
			continue
		}
		pairs = append(pairs, serializedMetadataPair{
			Key:   key,
			Value: strings.TrimSpace(entry.GetValue()),
		})
	}
	if len(pairs) == 0 {
		return ""
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Key != pairs[j].Key {
			return pairs[i].Key < pairs[j].Key
		}
		return pairs[i].Value < pairs[j].Value
	})
	encoded, err := json.Marshal(pairs)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func parseMetadataJSON(raw string) []*contract.HealthMetadataEntry {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var pairs []serializedMetadataPair
	if err := json.Unmarshal([]byte(trimmed), &pairs); err != nil {
		return nil
	}
	entries := make([]*contract.HealthMetadataEntry, 0, len(pairs))
	for _, pair := range pairs {
		entries = append(entries, &contract.HealthMetadataEntry{Key: pair.Key, Value: pair.Value})
	}
	return entries
}

// localCalendarDateForUnixAndOffset returns YYYY-MM-DD in the sample's observed
// timezone offset (falling back to time.Local when offset is zero and no
// explicit calendar date was recorded).
func localCalendarDateForUnixAndOffset(unixSeconds int64, utcOffsetSeconds int32, hasOffset bool) string {
	if hasOffset {
		loc := time.FixedZone("", int(utcOffsetSeconds))
		return time.Unix(unixSeconds, 0).In(loc).Format("2006-01-02")
	}
	return time.Unix(unixSeconds, 0).In(time.Local).Format("2006-01-02")
}

// parseAppleHealthTimestamp parses Apple Health export.xml / ECG CSV timestamps
// such as "2025-07-14 08:23:11 +0200" or RFC 3339 strings, preserving the exact
// UTC offset and local calendar date.
func parseAppleHealthTimestamp(raw string) (parsed time.Time, utcOffsetSeconds int32, calendarDate string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, 0, "", false
	}
	layouts := []string{
		"2006-01-02 15:04:05 -0700",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, trimmed)
		if err == nil {
			_, offset := t.Zone()
			return t, int32(offset), t.Format("2006-01-02"), true
		}
	}
	return time.Time{}, 0, "", false
}

// storedHealthRecord is one typed row read back from health_records.
type storedHealthRecord struct {
	reference        string
	kind             healthRecordKind
	startUnix        int64
	endUnix          int64
	utcOffsetSeconds int32
	calendarDate     string
	record           proto.Message
	deviceName       string
	sourceProvenance string
}

type sqlQueryRunner interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func unmarshalHealthRecordProto(kind healthRecordKind, encoded []byte, reference string) (proto.Message, error) {
	var msg proto.Message
	switch kind {
	case healthRecordKindWorkout:
		msg = &contract.HealthWorkout{}
	case healthRecordKindSleepSession:
		msg = &contract.HealthSleepSession{}
	case healthRecordKindDailyMetrics:
		msg = &contract.HealthDailyMetrics{}
	case healthRecordKindCorrelation:
		msg = &contract.HealthCorrelation{}
	case healthRecordKindActivitySummary:
		msg = &contract.HealthActivitySummary{}
	case healthRecordKindECG:
		msg = &contract.HealthElectrocardiogram{}
	case healthRecordKindAudiogram:
		msg = &contract.HealthAudiogram{}
	case healthRecordKindClinical:
		msg = &contract.HealthClinicalRecord{}
	default:
		return nil, fmt.Errorf("health record %s has unknown kind %q", reference, kind)
	}
	if err := proto.Unmarshal(encoded, msg); err != nil {
		return nil, fmt.Errorf("decode health record %s: %w", reference, err)
	}
	return msg, nil
}

func readStoredHealthRecords(ctx context.Context, runner sqlQueryRunner, whereClause string, arguments ...any) ([]storedHealthRecord, error) {
	rows, err := runner.QueryContext(ctx,
		"SELECT reference, record_kind, start_unix, end_unix, utc_offset_seconds, calendar_date, record, device_name, source_provenance FROM health_records "+whereClause,
		arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []storedHealthRecord
	for rows.Next() {
		var stored storedHealthRecord
		var kind string
		var encoded []byte
		if err := rows.Scan(
			&stored.reference,
			&kind,
			&stored.startUnix,
			&stored.endUnix,
			&stored.utcOffsetSeconds,
			&stored.calendarDate,
			&encoded,
			&stored.deviceName,
			&stored.sourceProvenance,
		); err != nil {
			return nil, err
		}
		stored.kind = healthRecordKind(kind)
		msg, err := unmarshalHealthRecordProto(stored.kind, encoded, stored.reference)
		if err != nil {
			return nil, err
		}
		stored.record = msg
		records = append(records, stored)
	}
	return records, rows.Err()
}

func upsertStoredHealthRecord(
	ctx context.Context,
	runner sqlQueryRunner,
	reference string,
	kind healthRecordKind,
	startUnix int64,
	endUnix int64,
	utcOffsetSeconds int32,
	calendarDate string,
	record proto.Message,
	deviceName string,
	exportedAt string,
	sourceProvenance string,
) (added bool, changed bool, err error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(record)
	if err != nil {
		return false, false, err
	}
	var previousEncoded []byte
	scanErr := runner.QueryRowContext(ctx, "SELECT record FROM health_records WHERE reference=?", reference).Scan(&previousEncoded)
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		added = true
	case scanErr != nil:
		return false, false, scanErr
	default:
		previous, unmarshalErr := unmarshalHealthRecordProto(kind, previousEncoded, reference)
		if unmarshalErr != nil {
			return false, false, unmarshalErr
		}
		if !proto.Equal(previous, record) {
			changed = true
		}
	}
	if _, err := runner.ExecContext(ctx, `
		INSERT INTO health_records(reference, record_kind, start_unix, end_unix, utc_offset_seconds, calendar_date, record, device_name, exported_at, source_provenance)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(reference) DO UPDATE SET
			start_unix=excluded.start_unix,
			end_unix=excluded.end_unix,
			utc_offset_seconds=excluded.utc_offset_seconds,
			calendar_date=excluded.calendar_date,
			record=excluded.record,
			device_name=CASE WHEN excluded.device_name != '' THEN excluded.device_name ELSE health_records.device_name END,
			exported_at=CASE WHEN excluded.exported_at != '' THEN excluded.exported_at ELSE health_records.exported_at END,
			source_provenance=CASE WHEN excluded.source_provenance != '' THEN excluded.source_provenance ELSE health_records.source_provenance END`,
		reference, string(kind), startUnix, endUnix, utcOffsetSeconds, calendarDate, encoded, deviceName, exportedAt, sourceProvenance); err != nil {
		return false, false, err
	}
	if _, err := runner.ExecContext(ctx, "DELETE FROM health_search WHERE reference=?", reference); err != nil {
		return false, false, err
	}
	if _, err := runner.ExecContext(ctx, "INSERT INTO health_search(reference, body) VALUES(?,?)", reference, healthRecordSearchableText(record)); err != nil {
		return false, false, err
	}
	return added, changed, nil
}

func insertHealthSampleRow(ctx context.Context, stmt *sql.Stmt, sample *contract.HealthSample, importOrigin string) (bool, error) {
	if sample == nil || strings.TrimSpace(sample.GetHealthkitTypeIdentifier()) == "" {
		return false, nil
	}
	startUnix := int64(0)
	if sample.GetStartTime() != nil {
		startUnix = sample.GetStartTime().GetSeconds()
	}
	endUnix := startUnix
	if sample.GetEndTime() != nil {
		endUnix = sample.GetEndTime().GetSeconds()
	}
	creationUnix := int64(0)
	if sample.GetCreationTime() != nil {
		creationUnix = sample.GetCreationTime().GetSeconds()
	}
	calendarDate := strings.TrimSpace(sample.GetCalendarDate())
	if calendarDate == "" && startUnix > 0 {
		calendarDate = localCalendarDateForUnixAndOffset(startUnix, sample.GetUtcOffsetSeconds(), sample.GetUtcOffsetSeconds() != 0)
	}
	var numericValue sql.NullFloat64
	if sample.NumericValue != nil && !math.IsNaN(sample.GetNumericValue()) && !math.IsInf(sample.GetNumericValue(), 0) {
		numericValue = sql.NullFloat64{Float64: sample.GetNumericValue(), Valid: true}
	}
	hash := healthSampleContentHash(sample)
	result, err := stmt.ExecContext(ctx,
		hash,
		strings.TrimSpace(sample.GetHealthkitSampleUuid()),
		strings.TrimSpace(sample.GetHealthkitTypeIdentifier()),
		calendarDate,
		startUnix,
		endUnix,
		creationUnix,
		sample.GetUtcOffsetSeconds(),
		strings.TrimSpace(sample.GetUnit()),
		strings.TrimSpace(sample.GetRawValue()),
		numericValue,
		strings.TrimSpace(sample.GetSourceName()),
		strings.TrimSpace(sample.GetSourceVersion()),
		strings.TrimSpace(sample.GetDevice()),
		canonicalMetadataJSON(sample.GetMetadataEntries()),
		importOrigin,
	)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

const insertHealthSampleSQL = `
INSERT OR IGNORE INTO health_samples(
  content_hash, sample_uuid, healthkit_type, calendar_date,
  start_unix, end_unix, creation_unix, utc_offset_seconds,
  unit, raw_value, numeric_value,
  source_name, source_version, device, metadata_json, import_origin
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func recordHealthUpdateCompletion(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx,
		"INSERT INTO health_update_completion VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET completed_time=excluded.completed_time",
		prototext.Format(timestamppb.Now()))
	return err
}

func recordAppliedHealthImport(
	ctx context.Context,
	runner sqlQueryRunner,
	name string,
	importKind string,
	sourcePath string,
	contentDigest string,
	added uint64,
	updated uint64,
) error {
	_, err := runner.ExecContext(ctx, `
		INSERT INTO health_imports(name, import_kind, source_path, content_digest, imported_at, records_added, records_updated)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			import_kind=excluded.import_kind,
			source_path=excluded.source_path,
			content_digest=excluded.content_digest,
			imported_at=excluded.imported_at,
			records_added=excluded.records_added,
			records_updated=excluded.records_updated`,
		name, importKind, sourcePath, contentDigest, time.Now().UTC().Format(time.RFC3339), int64(added), int64(updated))
	return err
}

func healthImportWasApplied(ctx context.Context, runner interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, name string) (bool, error) {
	var applied int
	err := runner.QueryRowContext(ctx, "SELECT 1 FROM health_imports WHERE name=?", name).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func healthImportDigestAlreadyApplied(ctx context.Context, runner interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, importKind string, contentDigest string) (bool, error) {
	if strings.TrimSpace(contentDigest) == "" {
		return false, nil
	}
	var applied int
	err := runner.QueryRowContext(ctx,
		"SELECT 1 FROM health_imports WHERE import_kind=? AND content_digest=? LIMIT 1",
		importKind, contentDigest).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// healthArchiveCursor returns a deterministic revision cursor reflecting all
// archived records, samples, and imports so core index.db knows when to refresh.
func healthArchiveCursor(ctx context.Context, db *sql.DB) (string, error) {
	var sampleCount, recordCount, importCount, workoutAliasCount int64
	var completedTime string
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM health_samples),
			(SELECT count(*) FROM health_records),
			(SELECT count(*) FROM health_imports),
			(SELECT count(*) FROM health_workout_aliases),
			COALESCE((SELECT completed_time FROM health_update_completion WHERE singleton=1), '')`).Scan(
		&sampleCount, &recordCount, &importCount, &workoutAliasCount, &completedTime,
	); err != nil {
		return "", fmt.Errorf("read health archive cursor: %w", err)
	}
	digest := deterministicHealthHash(completedTime)
	return fmt.Sprintf("health:s%d:r%d:i%d:w%d:%s", sampleCount, recordCount, importCount, workoutAliasCount, digest[:8]), nil
}
