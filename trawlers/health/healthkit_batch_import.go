package health

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"github.com/opentrawl/opentrawl/trawlkit"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// HealthImportsDirectoryForStateRoot is where devices or users drop immutable
// HealthKit batches, Apple Health exports, or HealthFit .fit files.
func HealthImportsDirectoryForStateRoot(stateRoot string) string {
	return filepath.Join(stateRoot, "health", "imports")
}

// StageHealthKitExportBatchImport validates batch and writes it as one new
// immutable import directory under importsDirectory. The directory appears
// complete or not at all: it is written under a dot name and then renamed.
func StageHealthKitExportBatchImport(importsDirectory string, batch *contract.HealthKitExportBatch) (string, error) {
	if err := validateHealthKitExportBatch(batch); err != nil {
		return "", err
	}
	if err := os.MkdirAll(importsDirectory, 0o700); err != nil {
		return "", err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	importName := batch.GetExportedAt().AsTime().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix)
	staging := filepath.Join(importsDirectory, ".incoming-"+importName)
	if err := os.Mkdir(staging, 0o700); err != nil {
		return "", err
	}
	encoded, err := prototext.MarshalOptions{Multiline: true}.Marshal(batch)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, healthImportPrototextFileName), encoded, 0o600); err != nil {
		return "", err
	}
	return importName, os.Rename(staging, filepath.Join(importsDirectory, importName))
}

func validateHealthKitExportBatch(batch *contract.HealthKitExportBatch) error {
	if batch == nil {
		return errors.New("health export batch is empty")
	}
	if batch.GetExportedAt() == nil || batch.GetExportedAt().CheckValid() != nil {
		return errors.New("exported_at is required")
	}
	if strings.TrimSpace(batch.GetDeviceName()) == "" {
		return errors.New("device_name is required")
	}
	for index, workout := range batch.GetWorkouts() {
		if strings.TrimSpace(workout.GetHealthkitSampleUuid()) == "" || workout.GetStartTime() == nil || workout.GetEndTime() == nil {
			return fmt.Errorf("workouts[%d] needs healthkit_sample_uuid, start_time and end_time", index)
		}
	}
	for index, session := range batch.GetSleepSessions() {
		if strings.TrimSpace(session.GetHealthkitSampleUuid()) == "" || session.GetStartTime() == nil || session.GetEndTime() == nil {
			return fmt.Errorf("sleep_sessions[%d] needs healthkit_sample_uuid, start_time and end_time", index)
		}
	}
	for index, day := range batch.GetDailyMetrics() {
		if !healthCalendarDatePattern.MatchString(day.GetCalendarDate()) {
			return fmt.Errorf("daily_metrics[%d] needs calendar_date as YYYY-MM-DD", index)
		}
	}
	for index, sample := range batch.GetSamples() {
		if strings.TrimSpace(sample.GetHealthkitTypeIdentifier()) == "" || sample.GetStartTime() == nil {
			return fmt.Errorf("samples[%d] needs healthkit_type_identifier and start_time", index)
		}
	}
	for index, summary := range batch.GetActivitySummaries() {
		if !healthCalendarDatePattern.MatchString(summary.GetCalendarDate()) {
			return fmt.Errorf("activity_summaries[%d] needs calendar_date as YYYY-MM-DD", index)
		}
	}
	return nil
}

func decodeHealthKitBatchFile(filePath string) (*contract.HealthKitExportBatch, error) {
	encoded, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	batch := &contract.HealthKitExportBatch{}
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		if err := protojson.Unmarshal(encoded, batch); err != nil {
			return nil, err
		}
	default:
		if err := prototext.Unmarshal(encoded, batch); err != nil {
			// Fallback to protojson in case a JSON payload was saved without .json suffix.
			if jsonErr := protojson.Unmarshal(encoded, batch); jsonErr != nil {
				return nil, err
			}
		}
	}
	if err := validateHealthKitExportBatch(batch); err != nil {
		return nil, err
	}
	return batch, nil
}

type pendingHealthBatchDrop struct {
	name          string
	directoryPath string
	retained      bool
	batch         *contract.HealthKitExportBatch
}

type pendingHealthFileDrop struct {
	name     string
	filePath string
	retained bool
}

// Update replays all pending immutable HealthKit batches and dropped export
// files from <stateRoot>/health/imports/ into health.db, moving newly processed
// drops to imports/applied/.
func (app *App) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	db := req.OpenedTrawlerArchiveStore.DB()
	if err := ensureHealthArchiveSchema(ctx, db); err != nil {
		return nil, err
	}
	importsDirectory := filepath.Join(filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath), "imports")
	if err := os.MkdirAll(filepath.Join(importsDirectory, "applied"), 0o700); err != nil {
		return nil, err
	}

	batchDrops, fileDrops, failures, err := readPendingHealthDrops(ctx, db, importsDirectory)
	if err != nil {
		return nil, err
	}

	var totalStats healthImportStatistics
	for _, pending := range batchDrops {
		batchStats, applyErr := applyHealthKitExportBatch(ctx, db, pending.name, pending.batch)
		if applyErr != nil {
			failures = append(failures, fmt.Errorf("apply health import %s: %w", pending.name, applyErr))
			continue
		}
		totalStats.merge(batchStats)
		if pending.retained {
			continue
		}
		if err := os.Rename(pending.directoryPath, filepath.Join(importsDirectory, "applied", pending.name)); err != nil {
			failures = append(failures, err)
		}
	}

	progressFn := func(phase string, done int64, message string) {
		if req.ReportTrawlerCommandProgress != nil {
			req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: phase, Done: done, Message: message})
		}
	}
	for _, drop := range fileDrops {
		fileStats, importErr := importHealthSourcePath(ctx, db, drop.filePath, progressFn)
		if importErr != nil {
			failures = append(failures, fmt.Errorf("import %s: %w", drop.name, importErr))
			continue
		}
		totalStats.merge(fileStats)
		if drop.retained {
			continue
		}
		if err := os.Rename(drop.filePath, filepath.Join(importsDirectory, "applied", drop.name)); err != nil {
			failures = append(failures, err)
		}
	}

	return &update.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(totalStats.totalAdded()),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(totalStats.totalUpdated()),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(0),
	}, errors.Join(failures...)
}

func readPendingHealthDrops(
	ctx context.Context,
	db *sql.DB,
	importsDirectory string,
) ([]pendingHealthBatchDrop, []pendingHealthFileDrop, []error, error) {
	var batches []pendingHealthBatchDrop
	var files []pendingHealthFileDrop
	var failures []error

	for _, base := range []string{importsDirectory, filepath.Join(importsDirectory, "applied")} {
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, nil, nil, err
		}
		retained := base != importsDirectory
		for _, entry := range entries {
			name := entry.Name()
			if name == "applied" || strings.HasPrefix(name, ".") {
				continue
			}
			fullPath := filepath.Join(base, name)
			if entry.IsDir() {
				if retained {
					applied, err := healthImportWasApplied(ctx, db, name)
					if err != nil {
						return nil, nil, nil, err
					}
					if applied {
						continue
					}
				}
				batchFile := filepath.Join(fullPath, healthImportPrototextFileName)
				if _, statErr := os.Stat(batchFile); errors.Is(statErr, os.ErrNotExist) {
					batchFile = filepath.Join(fullPath, healthImportJSONFileName)
				}
				batch, decErr := decodeHealthKitBatchFile(batchFile)
				if decErr != nil {
					failures = append(failures, fmt.Errorf("read health import %s: %w", name, decErr))
					continue
				}
				batches = append(batches, pendingHealthBatchDrop{
					name:          name,
					directoryPath: fullPath,
					retained:      retained,
					batch:         batch,
				})
				continue
			}

			// Direct file dropped into imports/ (.zip, .xml, .fit, .txtpb, .json).
			ext := strings.ToLower(filepath.Ext(name))
			switch ext {
			case ".txtpb", ".json":
				if retained {
					applied, err := healthImportWasApplied(ctx, db, name)
					if err != nil {
						return nil, nil, nil, err
					}
					if applied {
						continue
					}
				}
				batch, decErr := decodeHealthKitBatchFile(fullPath)
				if decErr != nil {
					failures = append(failures, fmt.Errorf("read health import %s: %w", name, decErr))
					continue
				}
				batches = append(batches, pendingHealthBatchDrop{
					name:          name,
					directoryPath: fullPath,
					retained:      retained,
					batch:         batch,
				})
			case ".zip", ".xml", ".fit":
				if retained {
					applied, err := healthImportWasApplied(ctx, db, "drop:"+name)
					if err != nil {
						return nil, nil, nil, err
					}
					if applied {
						continue
					}
				}
				files = append(files, pendingHealthFileDrop{
					name:     name,
					filePath: fullPath,
					retained: retained,
				})
			}
		}
	}

	sort.Slice(batches, func(i, j int) bool {
		left, right := batches[i].batch.GetExportedAt().AsTime(), batches[j].batch.GetExportedAt().AsTime()
		if left.Equal(right) {
			return batches[i].name < batches[j].name
		}
		return left.Before(right)
	})
	sort.Slice(files, func(i, j int) bool {
		return files[i].name < files[j].name
	})
	return batches, files, failures, nil
}

func applyHealthKitExportBatch(ctx context.Context, db *sql.DB, name string, batch *contract.HealthKitExportBatch) (healthImportStatistics, error) {
	var stats healthImportStatistics
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	defer func() { _ = tx.Rollback() }()

	alreadyApplied, err := healthImportWasApplied(ctx, tx, name)
	if err != nil || alreadyApplied {
		return stats, err
	}

	sampleStmt, err := tx.PrepareContext(ctx, insertHealthSampleSQL)
	if err != nil {
		return stats, err
	}
	defer func() { _ = sampleStmt.Close() }()

	exportedAt := prototext.Format(batch.GetExportedAt())
	deviceName := batch.GetDeviceName()

	// 1. Workouts (deduplicated against Apple Health XML and HealthFit .fit).
	for _, workout := range batch.GetWorkouts() {
		prov := &contract.HealthWorkoutSourceProvenance{
			SourceKind:       workoutSourceKindHealthKitBatch,
			SourceName:       workout.GetSourceName(),
			SourceIdentifier: workout.GetHealthkitSampleUuid(),
			ImportedFileName: name,
		}
		_, added, updated, dedup, wErr := upsertDeduplicatedWorkout(ctx, tx, workout, prov, deviceName, exportedAt)
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

	// 2. Sleep sessions.
	for _, session := range batch.GetSleepSessions() {
		ref := healthRecordReference(healthRecordKindSleepSession, session.GetHealthkitSampleUuid())
		endUnix := session.GetEndTime().GetSeconds()
		calDate := localCalendarDateForUnixAndOffset(endUnix, session.GetUtcOffsetSeconds(), session.GetUtcOffsetSeconds() != 0)
		added, _, sErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindSleepSession,
			session.GetStartTime().GetSeconds(), endUnix, session.GetUtcOffsetSeconds(), calDate,
			session, deviceName, exportedAt, workoutSourceKindHealthKitBatch)
		if sErr != nil {
			return stats, sErr
		}
		if added {
			stats.sleepStagesAdded++
		}
	}

	// 3. Daily metrics (merge set fields with any existing daily metrics row).
	for _, day := range batch.GetDailyMetrics() {
		ref := healthRecordReference(healthRecordKindDailyMetrics, day.GetCalendarDate())
		mergedDay := proto.Clone(day).(*contract.HealthDailyMetrics)
		var previousEncoded []byte
		scanErr := tx.QueryRowContext(ctx, "SELECT record FROM health_records WHERE reference=?", ref).Scan(&previousEncoded)
		if scanErr == nil {
			previous := &contract.HealthDailyMetrics{}
			if err := proto.Unmarshal(previousEncoded, previous); err != nil {
				return stats, err
			}
			mergedDay.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
				previous.ProtoReflect().Set(field, value)
				return true
			})
			mergedDay = previous
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return stats, scanErr
		}
		dayStart, _ := time.ParseInLocation("2006-01-02", mergedDay.GetCalendarDate(), time.Local)
		added, changed, dErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindDailyMetrics,
			dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()-1, 0, mergedDay.GetCalendarDate(),
			mergedDay, deviceName, exportedAt, workoutSourceKindHealthKitBatch)
		if dErr != nil {
			return stats, dErr
		}
		if added {
			stats.dailyMetricsAdded++
		} else if changed {
			stats.dailyMetricsUpdated++
		}
	}

	// 4. Generalized HealthKit quantity & category samples.
	for _, sample := range batch.GetSamples() {
		added, insErr := insertHealthSampleRow(ctx, sampleStmt, sample, name)
		if insErr != nil {
			return stats, insErr
		}
		if added {
			stats.samplesAdded++
		}
		if sample.GetHealthkitTypeIdentifier() == "HKCategoryTypeIdentifierSleepAnalysis" {
			sleepAdded, sErr := upsertSleepFromSample(ctx, tx, sample, name)
			if sErr != nil {
				return stats, sErr
			}
			if sleepAdded {
				stats.sleepStagesAdded++
			}
		}
	}

	// 5. Generalized correlations.
	for _, corr := range batch.GetCorrelations() {
		id := strings.TrimSpace(corr.GetReferenceIdentifier())
		if id == "" {
			id = deterministicHealthHash("correlation", corr.GetCorrelationTypeIdentifier(), formatUnixForHash(corr.GetStartTime()), corr.GetSourceName())
			corr.ReferenceIdentifier = id
		}
		ref := healthRecordReference(healthRecordKindCorrelation, id)
		startUnix := corr.GetStartTime().GetSeconds()
		endUnix := startUnix
		if corr.GetEndTime() != nil {
			endUnix = corr.GetEndTime().GetSeconds()
		}
		calDate := localCalendarDateForUnixAndOffset(startUnix, corr.GetUtcOffsetSeconds(), corr.GetUtcOffsetSeconds() != 0)
		added, _, cErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindCorrelation,
			startUnix, endUnix, corr.GetUtcOffsetSeconds(), calDate, corr, deviceName, exportedAt, name)
		if cErr != nil {
			return stats, cErr
		}
		if added {
			stats.correlationsAdded++
		}
		for _, comp := range corr.GetComponentSamples() {
			compAdded, insErr := insertHealthSampleRow(ctx, sampleStmt, comp, name)
			if insErr != nil {
				return stats, insErr
			}
			if compAdded {
				stats.samplesAdded++
			}
		}
	}

	// 6. Generalized ActivitySummaries.
	for _, summary := range batch.GetActivitySummaries() {
		dayStart, _ := time.ParseInLocation("2006-01-02", summary.GetCalendarDate(), time.Local)
		ref := healthRecordReference(healthRecordKindActivitySummary, summary.GetCalendarDate())
		added, _, aErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindActivitySummary,
			dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()-1, 0, summary.GetCalendarDate(),
			summary, deviceName, exportedAt, name)
		if aErr != nil {
			return stats, aErr
		}
		if added {
			stats.activitySummariesAdded++
		}
	}

	// 7. Generalized ECGs.
	for _, ecg := range batch.GetElectrocardiograms() {
		id := strings.TrimSpace(ecg.GetReferenceIdentifier())
		if id == "" {
			id = deterministicHealthHash("ecg", formatUnixForHash(ecg.GetRecordedTime()), ecg.GetClassification())
			ecg.ReferenceIdentifier = id
		}
		ref := healthRecordReference(healthRecordKindECG, id)
		recUnix := int64(0)
		if ecg.GetRecordedTime() != nil {
			recUnix = ecg.GetRecordedTime().GetSeconds()
		}
		calDate := localCalendarDateForUnixAndOffset(recUnix, ecg.GetUtcOffsetSeconds(), ecg.GetUtcOffsetSeconds() != 0)
		added, _, eErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindECG,
			recUnix, recUnix, ecg.GetUtcOffsetSeconds(), calDate, ecg, deviceName, exportedAt, name)
		if eErr != nil {
			return stats, eErr
		}
		if added {
			stats.ecgsAdded++
		}
	}

	// 8. Generalized Audiograms.
	for _, audiogram := range batch.GetAudiograms() {
		id := strings.TrimSpace(audiogram.GetReferenceIdentifier())
		if id == "" {
			id = deterministicHealthHash("audiogram", formatUnixForHash(audiogram.GetStartTime()), audiogram.GetSourceName())
			audiogram.ReferenceIdentifier = id
		}
		ref := healthRecordReference(healthRecordKindAudiogram, id)
		startUnix := int64(0)
		if audiogram.GetStartTime() != nil {
			startUnix = audiogram.GetStartTime().GetSeconds()
		}
		endUnix := startUnix
		if audiogram.GetEndTime() != nil {
			endUnix = audiogram.GetEndTime().GetSeconds()
		}
		calDate := localCalendarDateForUnixAndOffset(startUnix, audiogram.GetUtcOffsetSeconds(), audiogram.GetUtcOffsetSeconds() != 0)
		added, _, auErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindAudiogram,
			startUnix, endUnix, audiogram.GetUtcOffsetSeconds(), calDate, audiogram, deviceName, exportedAt, name)
		if auErr != nil {
			return stats, auErr
		}
		if added {
			stats.audiogramsAdded++
		}
	}

	// 9. Generalized ClinicalRecords.
	for _, clinical := range batch.GetClinicalRecords() {
		id := strings.TrimSpace(clinical.GetReferenceIdentifier())
		if id == "" {
			id = deterministicHealthHash("clinical", clinical.GetClinicalTypeIdentifier(), clinical.GetDisplayName(), clinical.GetSourceName())
			clinical.ReferenceIdentifier = id
		}
		ref := healthRecordReference(healthRecordKindClinical, id)
		recUnix := int64(0)
		if clinical.GetRecordedTime() != nil {
			recUnix = clinical.GetRecordedTime().GetSeconds()
		}
		calDate := ""
		if recUnix > 0 {
			calDate = localCalendarDateForUnixAndOffset(recUnix, clinical.GetUtcOffsetSeconds(), clinical.GetUtcOffsetSeconds() != 0)
		}
		added, _, clErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindClinical,
			recUnix, recUnix, clinical.GetUtcOffsetSeconds(), calDate, clinical, deviceName, exportedAt, name)
		if clErr != nil {
			return stats, clErr
		}
		if added {
			stats.clinicalRecordsAdded++
		}
	}

	if err := recordAppliedHealthImport(ctx, tx, name, "healthkit_batch", name, "", stats.totalAdded(), stats.totalUpdated()); err != nil {
		return stats, err
	}
	if err := sampleStmt.Close(); err != nil {
		return stats, err
	}
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	stats.filesProcessed++
	return stats, nil
}

// importHealthSourcePath imports any supported local file or directory:
// Apple Health export.zip, export.xml, extracted Apple Health export folder,
// HealthFit .fit file or directory of .fit files, or HealthKit batch .txtpb/.json.
func importHealthSourcePath(
	ctx context.Context,
	db *sql.DB,
	inputPath string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(inputPath))
	if err != nil {
		return healthImportStatistics{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return healthImportStatistics{}, fmt.Errorf("cannot read %s: %w", inputPath, err)
	}
	if info.IsDir() {
		return importHealthDirectory(ctx, db, absPath, reportProgress)
	}
	ext := strings.ToLower(filepath.Ext(absPath))
	switch ext {
	case ".zip":
		stats, err := importAppleHealthZip(ctx, db, absPath, reportProgress)
		if err != nil {
			return stats, err
		}
		_ = recordAppliedHealthImport(ctx, db, "zip:"+deterministicHealthHash(absPath, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().Unix())),
			"apple_health_zip", absPath, "", stats.totalAdded(), stats.totalUpdated())
		return stats, nil
	case ".xml":
		stats, err := importAppleHealthXMLFile(ctx, db, absPath, reportProgress)
		if err != nil {
			return stats, err
		}
		_ = recordAppliedHealthImport(ctx, db, "xml:"+deterministicHealthHash(absPath, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().Unix())),
			"apple_health_xml", absPath, "", stats.totalAdded(), stats.totalUpdated())
		return stats, nil
	case ".fit":
		return importFITFile(ctx, db, absPath)
	case ".txtpb", ".json":
		batch, decErr := decodeHealthKitBatchFile(absPath)
		if decErr != nil {
			return healthImportStatistics{}, fmt.Errorf("parse HealthKit batch %s: %w", filepath.Base(absPath), decErr)
		}
		batchName := "batch:" + deterministicHealthHash(absPath, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().Unix()))
		return applyHealthKitExportBatch(ctx, db, batchName, batch)
	default:
		return healthImportStatistics{}, fmt.Errorf("unsupported health import format %q (expected .zip, .xml, .fit, .txtpb, .json, or directory)", ext)
	}
}

func importHealthDirectory(
	ctx context.Context,
	db *sql.DB,
	dirPath string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	var stats healthImportStatistics

	// 1. Check for Apple Health export.xml inside this directory or apple_health_export/.
	for _, candidate := range []string{
		filepath.Join(dirPath, "export.xml"),
		filepath.Join(dirPath, "apple_health_export", "export.xml"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			xmlStats, xmlErr := importAppleHealthXMLFile(ctx, db, candidate, reportProgress)
			stats.merge(xmlStats)
			if xmlErr != nil {
				return stats, xmlErr
			}
			break
		}
	}

	// 2. Collect all .fit, .zip, and .txtpb files under the directory.
	var fitFiles []string
	var zipFiles []string
	var batchFiles []string
	if err := filepath.WalkDir(dirPath, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && p != dirPath {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".fit":
			fitFiles = append(fitFiles, p)
		case ".zip":
			zipFiles = append(zipFiles, p)
		case ".txtpb":
			batchFiles = append(batchFiles, p)
		}
		return nil
	}); err != nil {
		return stats, err
	}

	sort.Strings(zipFiles)
	for _, zp := range zipFiles {
		zipStats, zipErr := importAppleHealthZip(ctx, db, zp, reportProgress)
		stats.merge(zipStats)
		if zipErr != nil {
			return stats, zipErr
		}
	}

	sort.Strings(fitFiles)
	var fitFailures []error
	for i, fp := range fitFiles {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		fitStats, fitErr := importFITFile(ctx, db, fp)
		if fitErr != nil {
			fitFailures = append(fitFailures, fitErr)
			continue
		}
		stats.merge(fitStats)
		if reportProgress != nil && (i+1)%50 == 0 {
			reportProgress("import_healthfit_fit", int64(i+1), fmt.Sprintf("Imported %d of %d .fit files", i+1, len(fitFiles)))
		}
	}

	sort.Strings(batchFiles)
	for _, bp := range batchFiles {
		batch, decErr := decodeHealthKitBatchFile(bp)
		if decErr != nil {
			fitFailures = append(fitFailures, fmt.Errorf("parse %s: %w", filepath.Base(bp), decErr))
			continue
		}
		batchStats, applyErr := applyHealthKitExportBatch(ctx, db, "file:"+filepath.Base(bp)+":"+deterministicHealthHash(bp), batch)
		if applyErr != nil {
			fitFailures = append(fitFailures, applyErr)
			continue
		}
		stats.merge(batchStats)
	}

	if stats.filesProcessed == 0 && stats.filesSkipped == 0 && len(fitFailures) == 0 {
		return stats, fmt.Errorf("no Apple Health export.xml, .zip, .fit, or HealthKit batch files found in %s", dirPath)
	}
	return stats, errors.Join(fitFailures...)
}
