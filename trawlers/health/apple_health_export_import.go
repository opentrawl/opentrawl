package health

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	contract "github.com/opentrawl/opentrawl/trawlers/health/proto/trawl/health"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const appleHealthBatchCommitSize = 10000

// appleHealthAuxiliaryOpener resolves auxiliary files (GPX routes, ECG CSVs,
// clinical FHIR JSONs) inside either a zip archive or an extracted directory.
type appleHealthAuxiliaryOpener interface {
	openRelative(relPath string) (io.ReadCloser, string, error)
	listBySuffix(suffix string) []string
}

type zipAuxiliaryOpener struct {
	byLowerPath map[string]*zip.File
	allPaths    []string
}

func newZipAuxiliaryOpener(reader *zip.ReadCloser) *zipAuxiliaryOpener {
	opener := &zipAuxiliaryOpener{
		byLowerPath: make(map[string]*zip.File, len(reader.File)),
		allPaths:    make([]string, 0, len(reader.File)),
	}
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "/"))
		if strings.HasPrefix(clean, "__MACOSX/") || strings.HasPrefix(path.Base(clean), "._") {
			continue
		}
		lower := strings.ToLower(clean)
		opener.byLowerPath[lower] = f
		// Also register without leading "apple_health_export/" prefix so
		// FileReference paths like "/workout-routes/route_....gpx" match directly.
		if trimmed, ok := strings.CutPrefix(lower, "apple_health_export/"); ok {
			opener.byLowerPath[trimmed] = f
		}
		opener.allPaths = append(opener.allPaths, clean)
	}
	return opener
}

func (z *zipAuxiliaryOpener) openRelative(relPath string) (io.ReadCloser, string, error) {
	norm := strings.ToLower(path.Clean(strings.TrimPrefix(strings.ReplaceAll(relPath, "\\", "/"), "/")))
	if f, ok := z.byLowerPath[norm]; ok {
		rc, err := f.Open()
		return rc, f.Name, err
	}
	if trimmed, ok := strings.CutPrefix(norm, "apple_health_export/"); ok {
		if f, ok := z.byLowerPath[trimmed]; ok {
			rc, err := f.Open()
			return rc, f.Name, err
		}
	}
	// Fallback match by basename inside matching directory suffix.
	base := path.Base(norm)
	for candidatePath, f := range z.byLowerPath {
		if path.Base(candidatePath) == base {
			rc, err := f.Open()
			return rc, f.Name, err
		}
	}
	return nil, "", os.ErrNotExist
}

func (z *zipAuxiliaryOpener) listBySuffix(suffix string) []string {
	lowerSuffix := strings.ToLower(suffix)
	var matches []string
	for _, p := range z.allPaths {
		if strings.HasSuffix(strings.ToLower(p), lowerSuffix) {
			matches = append(matches, p)
		}
	}
	return matches
}

type dirAuxiliaryOpener struct {
	baseDir string
}

func (d *dirAuxiliaryOpener) openRelative(relPath string) (io.ReadCloser, string, error) {
	if d.baseDir == "" {
		return nil, "", os.ErrNotExist
	}
	cleanRel := filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(relPath, "\\", "/"), "/"))
	candidates := []string{
		filepath.Join(d.baseDir, cleanRel),
		filepath.Join(d.baseDir, "apple_health_export", cleanRel),
	}
	if trimmed, ok := strings.CutPrefix(cleanRel, "apple_health_export"+string(filepath.Separator)); ok {
		candidates = append(candidates, filepath.Join(d.baseDir, trimmed))
	}
	for _, candidate := range candidates {
		// Prevent directory traversal outside baseDir.
		relCheck, err := filepath.Rel(d.baseDir, candidate)
		if err != nil || strings.HasPrefix(relCheck, "..") {
			continue
		}
		f, err := os.Open(candidate)
		if err == nil {
			return f, relCheck, nil
		}
	}
	return nil, "", os.ErrNotExist
}

func (d *dirAuxiliaryOpener) listBySuffix(suffix string) []string {
	if d.baseDir == "" {
		return nil
	}
	lowerSuffix := strings.ToLower(suffix)
	var matches []string
	_ = filepath.WalkDir(d.baseDir, func(p string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && p != d.baseDir {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), lowerSuffix) {
			if rel, err := filepath.Rel(d.baseDir, p); err == nil {
				matches = append(matches, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	return matches
}

// importAppleHealthZip streams export.xml, workout-routes/*.gpx,
// electrocardiograms/*.csv, and clinical-records/*.json directly from an Apple
// Health export.zip without extracting multi-GB files to disk. Also imports any
// .fit files if the zip happens to contain HealthFit exports.
func importAppleHealthZip(
	ctx context.Context,
	db *sql.DB,
	zipPath string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return healthImportStatistics{}, fmt.Errorf("open zip %s: %w", filepath.Base(zipPath), err)
	}
	defer func() { _ = reader.Close() }()

	aux := newZipAuxiliaryOpener(reader)
	var stats healthImportStatistics
	foundExportXML := false

	// 1. Stream export.xml (and export_cda.xml is skipped as redundant CDA view).
	for _, candidate := range []string{"apple_health_export/export.xml", "export.xml"} {
		if f, ok := aux.byLowerPath[candidate]; ok {
			foundExportXML = true
			rc, openErr := f.Open()
			if openErr != nil {
				return stats, fmt.Errorf("open %s in zip: %w", f.Name, openErr)
			}
			xmlStats, xmlErr := streamImportAppleHealthXML(ctx, db, rc, aux, filepath.Base(zipPath), reportProgress)
			_ = rc.Close()
			stats.merge(xmlStats)
			if xmlErr != nil {
				return stats, xmlErr
			}
			stats.filesProcessed++
			break
		}
	}

	// 2. Import standalone ECG CSVs, Clinical JSONs, and any .fit files inside the zip.
	auxStats, auxErr := importAppleHealthAuxiliaryFiles(ctx, db, aux, filepath.Base(zipPath), reportProgress)
	stats.merge(auxStats)
	if auxErr != nil {
		return stats, auxErr
	}

	for _, fitRel := range aux.listBySuffix(".fit") {
		rc, resolvedName, openErr := aux.openRelative(fitRel)
		if openErr != nil {
			continue
		}
		fitBytes, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return stats, fmt.Errorf("read %s from zip: %w", resolvedName, readErr)
		}
		fitStats, fitErr := importFITBytes(ctx, db, fitBytes, resolvedName, zipPath+"#"+resolvedName)
		if fitErr != nil {
			return stats, fitErr
		}
		stats.merge(fitStats)
	}

	if !foundExportXML && stats.filesProcessed == 0 && stats.totalAdded() == 0 {
		return stats, fmt.Errorf("zip archive %s contains no export.xml, ECG, clinical, or .fit files", filepath.Base(zipPath))
	}
	return stats, nil
}

// importAppleHealthXMLFile streams a standalone export.xml file from disk along
// with any readable sibling workout-routes/, electrocardiograms/, and
// clinical-records/ directories.
func importAppleHealthXMLFile(
	ctx context.Context,
	db *sql.DB,
	xmlPath string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	file, err := os.Open(xmlPath)
	if err != nil {
		return healthImportStatistics{}, err
	}
	defer func() { _ = file.Close() }()

	aux := &dirAuxiliaryOpener{baseDir: filepath.Dir(xmlPath)}
	stats, err := streamImportAppleHealthXML(ctx, db, file, aux, filepath.Base(xmlPath), reportProgress)
	if err != nil {
		return stats, err
	}
	stats.filesProcessed++
	auxStats, auxErr := importAppleHealthAuxiliaryFiles(ctx, db, aux, filepath.Base(xmlPath), reportProgress)
	stats.merge(auxStats)
	return stats, auxErr
}

// xmlControlCharSanitizingReader strips invalid XML 1.0 control characters
// (such as \x0b or \x1a occasionally emitted in older iOS device metadata) while
// streaming without buffering the file.
type xmlControlCharSanitizingReader struct {
	reader *bufio.Reader
}

func newXMLControlCharSanitizingReader(r io.Reader) *xmlControlCharSanitizingReader {
	return &xmlControlCharSanitizingReader{reader: bufio.NewReaderSize(r, 256*1024)}
}

func (s *xmlControlCharSanitizingReader) Read(p []byte) (int, error) {
	n, err := s.reader.Read(p)
	for i := 0; i < n; i++ {
		b := p[i]
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			p[i] = ' '
		}
	}
	return n, err
}

// streamImportAppleHealthXML streams an Apple Health export.xml using
// encoding/xml.Decoder with bounded memory and batched SQLite commits.
func streamImportAppleHealthXML(
	ctx context.Context,
	db *sql.DB,
	rawReader io.Reader,
	aux appleHealthAuxiliaryOpener,
	originLabel string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	var stats healthImportStatistics
	decoder := xml.NewDecoder(newXMLControlCharSanitizingReader(rawReader))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return stats, err
	}
	sampleStmt, err := tx.PrepareContext(ctx, insertHealthSampleSQL)
	if err != nil {
		_ = tx.Rollback()
		return stats, err
	}
	pendingInTx := 0
	var elementsProcessed int64

	flushBatch := func() error {
		if pendingInTx == 0 {
			return nil
		}
		if err := sampleStmt.Close(); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		sampleStmt, err = tx.PrepareContext(ctx, insertHealthSampleSQL)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		pendingInTx = 0
		return nil
	}

	defer func() {
		if sampleStmt != nil {
			_ = sampleStmt.Close()
		}
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("stream Apple Health XML: %w", err)
		}
		startElem, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch startElem.Name.Local {
		case "Record":
			sample, parseErr := decodeXMLRecordElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			added, insErr := insertHealthSampleRow(ctx, sampleStmt, sample, originLabel)
			if insErr != nil {
				return stats, insErr
			}
			if added {
				stats.samplesAdded++
			}
			if sample.GetHealthkitTypeIdentifier() == "HKCategoryTypeIdentifierSleepAnalysis" {
				sleepAdded, sleepErr := upsertSleepFromSample(ctx, tx, sample, originLabel)
				if sleepErr != nil {
					return stats, sleepErr
				}
				if sleepAdded {
					stats.sleepStagesAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "Workout":
			workout, routeFileRef, parseErr := decodeXMLWorkoutElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			if routeFileRef != "" && aux != nil {
				if routeSummary, gpxErr := parseGPXRouteSummaryFromOpener(aux, routeFileRef); gpxErr == nil && routeSummary != nil {
					workout.RouteSummary = routeSummary
					if workout.TotalAscentMeters == nil && routeSummary.TotalElevationGainMeters != nil && routeSummary.GetTotalElevationGainMeters() > 0 {
						workout.TotalAscentMeters = proto.Float64(routeSummary.GetTotalElevationGainMeters())
					}
				}
			}
			prov := &contract.HealthWorkoutSourceProvenance{
				SourceKind:       workoutSourceKindAppleHealthXML,
				SourceName:       workout.GetSourceName(),
				SourceIdentifier: workout.GetHealthkitSampleUuid(),
				ImportedFileName: originLabel,
			}
			_, wAdded, wUpdated, wDedup, wErr := upsertDeduplicatedWorkout(ctx, tx, workout, prov, workout.GetDevice(), "")
			if wErr != nil {
				return stats, wErr
			}
			if wAdded {
				stats.workoutsAdded++
			}
			if wUpdated {
				stats.workoutsUpdated++
			}
			if wDedup {
				stats.workoutsDeduplicated++
			}
			pendingInTx++
			elementsProcessed++

		case "Correlation":
			corr, parseErr := decodeXMLCorrelationElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			ref := healthRecordReference(healthRecordKindCorrelation, corr.GetReferenceIdentifier())
			startUnix := corr.GetStartTime().GetSeconds()
			endUnix := corr.GetEndTime().GetSeconds()
			calDate := localCalendarDateForUnixAndOffset(startUnix, corr.GetUtcOffsetSeconds(), corr.GetUtcOffsetSeconds() != 0)
			cAdded, _, cErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindCorrelation,
				startUnix, endUnix, corr.GetUtcOffsetSeconds(), calDate, corr, corr.GetDevice(), "", originLabel)
			if cErr != nil {
				return stats, cErr
			}
			if cAdded {
				stats.correlationsAdded++
			}
			// Also store component samples in health_samples so daily metric
			// queries (e.g. blood pressure systolic/diastolic) include them.
			for _, comp := range corr.GetComponentSamples() {
				compAdded, insErr := insertHealthSampleRow(ctx, sampleStmt, comp, originLabel)
				if insErr != nil {
					return stats, insErr
				}
				if compAdded {
					stats.samplesAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "ActivitySummary":
			summary, parseErr := decodeXMLActivitySummaryElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			if summary != nil && healthCalendarDatePattern.MatchString(summary.GetCalendarDate()) {
				dayStart, _ := time.ParseInLocation("2006-01-02", summary.GetCalendarDate(), time.Local)
				ref := healthRecordReference(healthRecordKindActivitySummary, summary.GetCalendarDate())
				aAdded, _, aErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindActivitySummary,
					dayStart.Unix(), dayStart.AddDate(0, 0, 1).Unix()-1, 0, summary.GetCalendarDate(),
					summary, "Apple Watch", "", originLabel)
				if aErr != nil {
					return stats, aErr
				}
				if aAdded {
					stats.activitySummariesAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "Audiogram":
			audiogram, parseErr := decodeXMLAudiogramElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			if audiogram != nil {
				ref := healthRecordReference(healthRecordKindAudiogram, audiogram.GetReferenceIdentifier())
				startUnix := audiogram.GetStartTime().GetSeconds()
				endUnix := audiogram.GetEndTime().GetSeconds()
				calDate := localCalendarDateForUnixAndOffset(startUnix, audiogram.GetUtcOffsetSeconds(), audiogram.GetUtcOffsetSeconds() != 0)
				auAdded, _, auErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindAudiogram,
					startUnix, endUnix, audiogram.GetUtcOffsetSeconds(), calDate,
					audiogram, audiogram.GetDevice(), "", originLabel)
				if auErr != nil {
					return stats, auErr
				}
				if auAdded {
					stats.audiogramsAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "ClinicalRecord":
			clinical, parseErr := decodeXMLClinicalRecordElement(decoder, startElem, aux)
			if parseErr != nil {
				return stats, parseErr
			}
			if clinical != nil {
				ref := healthRecordReference(healthRecordKindClinical, clinical.GetReferenceIdentifier())
				recUnix := int64(0)
				if clinical.GetRecordedTime() != nil {
					recUnix = clinical.GetRecordedTime().GetSeconds()
				}
				calDate := ""
				if recUnix > 0 {
					calDate = localCalendarDateForUnixAndOffset(recUnix, clinical.GetUtcOffsetSeconds(), clinical.GetUtcOffsetSeconds() != 0)
				}
				clAdded, _, clErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindClinical,
					recUnix, recUnix, clinical.GetUtcOffsetSeconds(), calDate,
					clinical, clinical.GetSourceName(), "", originLabel)
				if clErr != nil {
					return stats, clErr
				}
				if clAdded {
					stats.clinicalRecordsAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "Electrocardiogram":
			ecg, parseErr := decodeXMLElectrocardiogramElement(decoder, startElem)
			if parseErr != nil {
				return stats, parseErr
			}
			if ecg != nil {
				ref := healthRecordReference(healthRecordKindECG, ecg.GetReferenceIdentifier())
				recUnix := ecg.GetRecordedTime().GetSeconds()
				calDate := localCalendarDateForUnixAndOffset(recUnix, ecg.GetUtcOffsetSeconds(), ecg.GetUtcOffsetSeconds() != 0)
				eAdded, _, eErr := upsertStoredHealthRecord(ctx, tx, ref, healthRecordKindECG,
					recUnix, recUnix, ecg.GetUtcOffsetSeconds(), calDate,
					ecg, ecg.GetDevice(), "", originLabel)
				if eErr != nil {
					return stats, eErr
				}
				if eAdded {
					stats.ecgsAdded++
				}
			}
			pendingInTx++
			elementsProcessed++

		case "HealthData", "ExportDate", "Me":
			// Root container or user profile header; continue streaming children.
			continue
		default:
			if err := decoder.Skip(); err != nil {
				return stats, err
			}
		}

		if pendingInTx >= appleHealthBatchCommitSize {
			if err := flushBatch(); err != nil {
				return stats, err
			}
			if reportProgress != nil {
				reportProgress("import_apple_health_xml", elementsProcessed, fmt.Sprintf("Imported %d Apple Health elements", elementsProcessed))
			}
		}
	}

	if err := sampleStmt.Close(); err != nil {
		return stats, err
	}
	sampleStmt = nil
	if err := tx.Commit(); err != nil {
		return stats, err
	}
	tx = nil
	return stats, nil
}

func importAppleHealthAuxiliaryFiles(
	ctx context.Context,
	db *sql.DB,
	aux appleHealthAuxiliaryOpener,
	originLabel string,
	reportProgress func(phase string, done int64, message string),
) (healthImportStatistics, error) {
	var stats healthImportStatistics
	if aux == nil {
		return stats, nil
	}

	// 1. Electrocardiogram CSV files (electrocardiograms/*.csv).
	ecgPaths := aux.listBySuffix(".csv")
	for _, relPath := range ecgPaths {
		lower := strings.ToLower(relPath)
		if !strings.Contains(lower, "electrocardiogram") && !strings.HasPrefix(path.Base(lower), "ecg") {
			continue
		}
		rc, resolvedName, err := aux.openRelative(relPath)
		if err != nil {
			continue
		}
		ecg, parseErr := parseAppleHealthECGCsv(rc, resolvedName)
		_ = rc.Close()
		if parseErr != nil || ecg == nil {
			continue
		}
		ref := healthRecordReference(healthRecordKindECG, ecg.GetReferenceIdentifier())
		recUnix := int64(0)
		if ecg.GetRecordedTime() != nil {
			recUnix = ecg.GetRecordedTime().GetSeconds()
		}
		calDate := ""
		if recUnix > 0 {
			calDate = localCalendarDateForUnixAndOffset(recUnix, ecg.GetUtcOffsetSeconds(), ecg.GetUtcOffsetSeconds() != 0)
		}
		added, _, upsertErr := upsertStoredHealthRecord(ctx, db, ref, healthRecordKindECG,
			recUnix, recUnix, ecg.GetUtcOffsetSeconds(), calDate, ecg, ecg.GetDevice(), "", originLabel)
		if upsertErr != nil {
			return stats, upsertErr
		}
		if added {
			stats.ecgsAdded++
		}
		stats.filesProcessed++
	}

	// 2. Standalone clinical-records/*.json FHIR resources not already imported via XML.
	existingClinicalFiles := map[string]struct{}{}
	if existingClinical, readErr := readStoredHealthRecords(ctx, db, "WHERE record_kind=?", string(healthRecordKindClinical)); readErr == nil {
		for _, rec := range existingClinical {
			if cl, ok := rec.record.(*contract.HealthClinicalRecord); ok {
				if ref := strings.TrimSpace(cl.GetResourceFileReference()); ref != "" {
					existingClinicalFiles[strings.ToLower(path.Base(strings.ReplaceAll(ref, "\\", "/")))] = struct{}{}
				}
			}
		}
	}
	for _, relPath := range aux.listBySuffix(".json") {
		lower := strings.ToLower(relPath)
		if !strings.Contains(lower, "clinical-records") {
			continue
		}
		if _, alreadyReferencedByXML := existingClinicalFiles[path.Base(lower)]; alreadyReferencedByXML {
			continue
		}
		rc, resolvedName, err := aux.openRelative(relPath)
		if err != nil {
			continue
		}
		rawJSON, readErr := io.ReadAll(io.LimitReader(rc, 4*1024*1024))
		_ = rc.Close()
		if readErr != nil || len(rawJSON) == 0 {
			continue
		}
		clinical := parseFHIRClinicalJSON(rawJSON, resolvedName)
		if clinical == nil {
			continue
		}
		ref := healthRecordReference(healthRecordKindClinical, clinical.GetReferenceIdentifier())
		recUnix := int64(0)
		if clinical.GetRecordedTime() != nil {
			recUnix = clinical.GetRecordedTime().GetSeconds()
		}
		calDate := ""
		if recUnix > 0 {
			calDate = localCalendarDateForUnixAndOffset(recUnix, clinical.GetUtcOffsetSeconds(), clinical.GetUtcOffsetSeconds() != 0)
		}
		added, _, upsertErr := upsertStoredHealthRecord(ctx, db, ref, healthRecordKindClinical,
			recUnix, recUnix, clinical.GetUtcOffsetSeconds(), calDate, clinical, clinical.GetSourceName(), "", originLabel)
		if upsertErr != nil {
			return stats, upsertErr
		}
		if added {
			stats.clinicalRecordsAdded++
		}
		stats.filesProcessed++
	}

	// 3. Standalone GPX workout routes not already linked by XML <WorkoutRoute>.
	gpxPaths := aux.listBySuffix(".gpx")
	if len(gpxPaths) > 0 && reportProgress != nil {
		reportProgress("import_gpx_routes", 0, fmt.Sprintf("Checking %d GPX routes", len(gpxPaths)))
	}
	for _, relPath := range gpxPaths {
		rc, resolvedName, err := aux.openRelative(relPath)
		if err != nil {
			continue
		}
		summary, startUnix, parseErr := parseGPXRouteStream(rc, resolvedName)
		_ = rc.Close()
		if parseErr != nil || summary == nil || startUnix == 0 {
			continue
		}
		candidates, readErr := readStoredHealthRecords(ctx, db,
			"WHERE record_kind=? AND start_unix BETWEEN ? AND ?",
			string(healthRecordKindWorkout), startUnix-workoutStartToleranceSeconds, startUnix+workoutStartToleranceSeconds)
		if readErr != nil || len(candidates) == 0 {
			continue
		}
		target := candidates[0]
		workout := proto.Clone(target.record).(*contract.HealthWorkout)
		if workout.GetRouteSummary() != nil && workout.GetRouteSummary().GetPointCount() >= summary.GetPointCount() {
			continue
		}
		workout.RouteSummary = summary
		if workout.TotalAscentMeters == nil && summary.TotalElevationGainMeters != nil && summary.GetTotalElevationGainMeters() > 0 {
			workout.TotalAscentMeters = proto.Float64(summary.GetTotalElevationGainMeters())
		}
		_, changed, upsertErr := upsertStoredHealthRecord(ctx, db, target.reference, healthRecordKindWorkout,
			target.startUnix, target.endUnix, target.utcOffsetSeconds, target.calendarDate,
			workout, target.deviceName, "", target.sourceProvenance)
		if upsertErr != nil {
			return stats, upsertErr
		}
		if changed {
			stats.workoutsUpdated++
		}
	}
	return stats, nil
}

func xmlAttrMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		m[attr.Name.Local] = attr.Value
	}
	return m
}

func decodeXMLRecordElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthSample, error) {
	attrs := xmlAttrMap(start.Attr)
	sample := &contract.HealthSample{
		HealthkitTypeIdentifier: attrs["type"],
		Unit:                    attrs["unit"],
		RawValue:                attrs["value"],
		SourceName:              attrs["sourceName"],
		SourceVersion:           attrs["sourceVersion"],
		Device:                  attrs["device"],
	}
	if val, err := strconv.ParseFloat(strings.TrimSpace(attrs["value"]), 64); err == nil && !math.IsNaN(val) && !math.IsInf(val, 0) {
		sample.NumericValue = proto.Float64(val)
	}
	if t, offset, calDate, ok := parseAppleHealthTimestamp(attrs["startDate"]); ok {
		sample.StartTime = timestamppb.New(t.UTC())
		sample.UtcOffsetSeconds = offset
		sample.CalendarDate = calDate
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["endDate"]); ok {
		sample.EndTime = timestamppb.New(t.UTC())
	} else {
		sample.EndTime = sample.StartTime
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["creationDate"]); ok {
		sample.CreationTime = timestamppb.New(t.UTC())
	}

	// Read child MetadataEntry elements until </Record>.
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "MetadataEntry" {
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					sample.MetadataEntries = append(sample.MetadataEntries, &contract.HealthMetadataEntry{
						Key:   k,
						Value: ma["value"],
					})
				}
			}
			if err := decoder.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return sample, nil
			}
		}
	}
}

func sleepStageFromHealthKitRawValue(rawValue string) contract.HealthSleepStage {
	switch strings.TrimSpace(rawValue) {
	case "HKCategoryValueSleepAnalysisInBed", "0":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_IN_BED
	case "HKCategoryValueSleepAnalysisAsleep", "HKCategoryValueSleepAnalysisAsleepUnspecified", "1":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_UNSPECIFIED
	case "HKCategoryValueSleepAnalysisAwake", "2":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_AWAKE
	case "HKCategoryValueSleepAnalysisAsleepCore", "3":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_CORE
	case "HKCategoryValueSleepAnalysisAsleepDeep", "4":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_DEEP
	case "HKCategoryValueSleepAnalysisAsleepREM", "5":
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_REM
	default:
		return contract.HealthSleepStage_HEALTH_SLEEP_STAGE_ASLEEP_UNSPECIFIED
	}
}

func upsertSleepFromSample(ctx context.Context, runner sqlQueryRunner, sample *contract.HealthSample, originLabel string) (bool, error) {
	if sample == nil || sample.GetStartTime() == nil || sample.GetEndTime() == nil {
		return false, nil
	}
	stage := sleepStageFromHealthKitRawValue(sample.GetRawValue())
	sampleID := strings.TrimSpace(sample.GetHealthkitSampleUuid())
	if sampleID == "" {
		sampleID = deterministicHealthHash(
			"sleep",
			formatUnixForHash(sample.GetStartTime()),
			formatUnixForHash(sample.GetEndTime()),
			stage.String(),
			sample.GetSourceName(),
		)
	}
	session := &contract.HealthSleepSession{
		HealthkitSampleUuid: sampleID,
		StartTime:           sample.GetStartTime(),
		EndTime:             sample.GetEndTime(),
		SleepStage:          stage,
		SourceName:          sample.GetSourceName(),
		SourceVersion:       sample.GetSourceVersion(),
		Device:              sample.GetDevice(),
		UtcOffsetSeconds:    sample.GetUtcOffsetSeconds(),
	}
	ref := healthRecordReference(healthRecordKindSleepSession, sampleID)
	endUnix := session.GetEndTime().GetSeconds()
	calDate := localCalendarDateForUnixAndOffset(endUnix, session.GetUtcOffsetSeconds(), session.GetUtcOffsetSeconds() != 0)
	added, _, err := upsertStoredHealthRecord(ctx, runner, ref, healthRecordKindSleepSession,
		session.GetStartTime().GetSeconds(), endUnix, session.GetUtcOffsetSeconds(), calDate,
		session, session.GetDevice(), "", originLabel)
	return added, err
}

func decodeXMLWorkoutElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthWorkout, string, error) {
	attrs := xmlAttrMap(start.Attr)
	activityCase := healthKitWorkoutActivityCaseName(attrs["workoutActivityType"])
	workout := &contract.HealthWorkout{
		HealthkitWorkoutActivityTypeName: activityCase,
		SourceName:                       attrs["sourceName"],
		SourceVersion:                    attrs["sourceVersion"],
		Device:                           attrs["device"],
	}
	if t, offset, _, ok := parseAppleHealthTimestamp(attrs["startDate"]); ok {
		workout.StartTime = timestamppb.New(t.UTC())
		workout.UtcOffsetSeconds = offset
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["endDate"]); ok {
		workout.EndTime = timestamppb.New(t.UTC())
	} else {
		workout.EndTime = workout.StartTime
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["creationDate"]); ok {
		workout.CreationTime = timestamppb.New(t.UTC())
	}
	if dur, err := strconv.ParseFloat(strings.TrimSpace(attrs["duration"]), 64); err == nil && dur > 0 {
		workout.DurationSeconds = convertDurationToSeconds(dur, attrs["durationUnit"])
	} else if workout.GetStartTime() != nil && workout.GetEndTime() != nil {
		workout.DurationSeconds = float64(max(workout.GetEndTime().GetSeconds()-workout.GetStartTime().GetSeconds(), 0))
	}
	if dist, err := strconv.ParseFloat(strings.TrimSpace(attrs["totalDistance"]), 64); err == nil && dist > 0 {
		workout.TotalDistanceMeters = proto.Float64(convertDistanceToMeters(dist, attrs["totalDistanceUnit"]))
	}
	if energy, err := strconv.ParseFloat(strings.TrimSpace(attrs["totalEnergyBurned"]), 64); err == nil && energy > 0 {
		workout.TotalEnergyBurnedKilocalories = proto.Float64(convertEnergyToKilocalories(energy, attrs["totalEnergyBurnedUnit"]))
	}

	var routeFileRef string
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "MetadataEntry":
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					workout.MetadataEntries = append(workout.MetadataEntries, &contract.HealthMetadataEntry{Key: k, Value: ma["value"]})
					if k == "HKExternalUUID" && strings.TrimSpace(ma["value"]) != "" && workout.HealthkitSampleUuid == "" {
						workout.HealthkitSampleUuid = strings.TrimSpace(ma["value"])
					}
					if k == "HKElevationAscended" && workout.TotalAscentMeters == nil {
						if meters, ok := parseElevationMetadataMeters(ma["value"]); ok {
							workout.TotalAscentMeters = proto.Float64(meters)
						}
					}
				}
				if err := decoder.Skip(); err != nil {
					return nil, "", err
				}
			case "WorkoutStatistics":
				stat := parseXMLWorkoutStatistic(t.Attr)
				workout.Statistics = append(workout.Statistics, stat)
				applyWorkoutStatisticToSummaryFields(workout, stat)
				if err := decoder.Skip(); err != nil {
					return nil, "", err
				}
			case "WorkoutEvent":
				ev, evErr := decodeXMLWorkoutEvent(decoder, t)
				if evErr != nil {
					return nil, "", evErr
				}
				workout.Events = append(workout.Events, ev)
			case "WorkoutRoute":
				ref, routeErr := decodeXMLWorkoutRouteFileRef(decoder, t)
				if routeErr != nil {
					return nil, "", routeErr
				}
				if ref != "" && routeFileRef == "" {
					routeFileRef = ref
				}
			default:
				if err := decoder.Skip(); err != nil {
					return nil, "", err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				if strings.TrimSpace(workout.GetHealthkitSampleUuid()) == "" {
					workout.HealthkitSampleUuid = deterministicHealthHash(
						"xml-workout",
						workout.GetHealthkitWorkoutActivityTypeName(),
						formatUnixForHash(workout.GetStartTime()),
						formatUnixForHash(workout.GetEndTime()),
						workout.GetSourceName(),
					)
				}
				return workout, routeFileRef, nil
			}
		}
	}
}

func parseXMLWorkoutStatistic(attrs []xml.Attr) *contract.HealthWorkoutStatistic {
	m := xmlAttrMap(attrs)
	stat := &contract.HealthWorkoutStatistic{
		QuantityTypeIdentifier: m["type"],
		Unit:                   m["unit"],
	}
	if t, _, _, ok := parseAppleHealthTimestamp(m["startDate"]); ok {
		stat.StartTime = timestamppb.New(t.UTC())
	}
	if t, _, _, ok := parseAppleHealthTimestamp(m["endDate"]); ok {
		stat.EndTime = timestamppb.New(t.UTC())
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(m["sum"]), 64); err == nil {
		stat.SumValue = proto.Float64(v)
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(m["average"]), 64); err == nil {
		stat.AverageValue = proto.Float64(v)
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(m["minimum"]), 64); err == nil {
		stat.MinimumValue = proto.Float64(v)
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(m["maximum"]), 64); err == nil {
		stat.MaximumValue = proto.Float64(v)
	}
	return stat
}

func applyWorkoutStatisticToSummaryFields(workout *contract.HealthWorkout, stat *contract.HealthWorkoutStatistic) {
	if workout == nil || stat == nil {
		return
	}
	switch stat.GetQuantityTypeIdentifier() {
	case "HKQuantityTypeIdentifierActiveEnergyBurned":
		if workout.TotalEnergyBurnedKilocalories == nil && stat.SumValue != nil {
			workout.TotalEnergyBurnedKilocalories = proto.Float64(convertEnergyToKilocalories(stat.GetSumValue(), stat.GetUnit()))
		}
	case "HKQuantityTypeIdentifierDistanceWalkingRunning",
		"HKQuantityTypeIdentifierDistanceCycling",
		"HKQuantityTypeIdentifierDistanceSwimming",
		"HKQuantityTypeIdentifierDistanceDownhillSnowSports",
		"HKQuantityTypeIdentifierDistanceWheelchair",
		"HKQuantityTypeIdentifierDistanceCrossCountrySkiing",
		"HKQuantityTypeIdentifierDistancePaddleSports",
		"HKQuantityTypeIdentifierDistanceRowing":
		if workout.TotalDistanceMeters == nil && stat.SumValue != nil {
			workout.TotalDistanceMeters = proto.Float64(convertDistanceToMeters(stat.GetSumValue(), stat.GetUnit()))
		}
	case "HKQuantityTypeIdentifierHeartRate":
		if workout.AverageHeartRateBeatsPerMinute == nil && stat.AverageValue != nil {
			workout.AverageHeartRateBeatsPerMinute = proto.Float64(stat.GetAverageValue())
		}
		if workout.MaxHeartRateBeatsPerMinute == nil && stat.MaximumValue != nil {
			workout.MaxHeartRateBeatsPerMinute = proto.Float64(stat.GetMaximumValue())
		}
	case "HKQuantityTypeIdentifierCyclingPower", "HKQuantityTypeIdentifierRunningPower":
		if workout.AveragePowerWatts == nil && stat.AverageValue != nil {
			workout.AveragePowerWatts = proto.Float64(stat.GetAverageValue())
		}
	case "HKQuantityTypeIdentifierCyclingCadence":
		if workout.AverageCadence == nil && stat.AverageValue != nil {
			workout.AverageCadence = proto.Float64(stat.GetAverageValue())
		}
	case "HKQuantityTypeIdentifierCyclingSpeed", "HKQuantityTypeIdentifierRunningSpeed":
		if workout.AverageSpeedMetersPerSecond == nil && stat.AverageValue != nil {
			workout.AverageSpeedMetersPerSecond = proto.Float64(convertSpeedToMetersPerSecond(stat.GetAverageValue(), stat.GetUnit()))
		}
	}
}

func decodeXMLWorkoutEvent(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthWorkoutEvent, error) {
	attrs := xmlAttrMap(start.Attr)
	ev := &contract.HealthWorkoutEvent{
		EventType: attrs["type"],
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["date"]); ok {
		ev.EventTime = timestamppb.New(t.UTC())
	}
	if dur, err := strconv.ParseFloat(strings.TrimSpace(attrs["duration"]), 64); err == nil && dur > 0 {
		ev.DurationSeconds = proto.Float64(convertDurationToSeconds(dur, attrs["durationUnit"]))
	}
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "MetadataEntry" {
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					ev.MetadataEntries = append(ev.MetadataEntries, &contract.HealthMetadataEntry{Key: k, Value: ma["value"]})
				}
			}
			if err := decoder.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return ev, nil
			}
		}
	}
}

func decodeXMLWorkoutRouteFileRef(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var fileRef string
	for {
		tok, err := decoder.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "FileReference" {
				attrs := xmlAttrMap(t.Attr)
				if p := strings.TrimSpace(attrs["path"]); p != "" {
					fileRef = p
				}
			}
			if err := decoder.Skip(); err != nil {
				return "", err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return fileRef, nil
			}
		}
	}
}

func decodeXMLCorrelationElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthCorrelation, error) {
	attrs := xmlAttrMap(start.Attr)
	corr := &contract.HealthCorrelation{
		CorrelationTypeIdentifier: attrs["type"],
		SourceName:                attrs["sourceName"],
		SourceVersion:             attrs["sourceVersion"],
		Device:                    attrs["device"],
	}
	if t, offset, _, ok := parseAppleHealthTimestamp(attrs["startDate"]); ok {
		corr.StartTime = timestamppb.New(t.UTC())
		corr.UtcOffsetSeconds = offset
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["endDate"]); ok {
		corr.EndTime = timestamppb.New(t.UTC())
	} else {
		corr.EndTime = corr.StartTime
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["creationDate"]); ok {
		corr.CreationTime = timestamppb.New(t.UTC())
	}
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Record":
				sample, recErr := decodeXMLRecordElement(decoder, t)
				if recErr != nil {
					return nil, recErr
				}
				corr.ComponentSamples = append(corr.ComponentSamples, sample)
			case "MetadataEntry":
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					corr.MetadataEntries = append(corr.MetadataEntries, &contract.HealthMetadataEntry{Key: k, Value: ma["value"]})
				}
				if err := decoder.Skip(); err != nil {
					return nil, err
				}
			default:
				if err := decoder.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				corr.ReferenceIdentifier = deterministicHealthHash(
					"correlation",
					corr.GetCorrelationTypeIdentifier(),
					formatUnixForHash(corr.GetStartTime()),
					formatUnixForHash(corr.GetEndTime()),
					corr.GetSourceName(),
				)
				return corr, nil
			}
		}
	}
}

func decodeXMLActivitySummaryElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthActivitySummary, error) {
	attrs := xmlAttrMap(start.Attr)
	if err := decoder.Skip(); err != nil {
		return nil, err
	}
	calDate := strings.TrimSpace(attrs["dateComponents"])
	if !healthCalendarDatePattern.MatchString(calDate) {
		return nil, nil
	}
	summary := &contract.HealthActivitySummary{CalendarDate: calDate}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["activeEnergyBurned"]); ok {
		summary.ActiveEnergyBurnedKilocalories = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["activeEnergyBurnedGoal"]); ok {
		summary.ActiveEnergyBurnedGoalKilocalories = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleMoveTime"]); ok {
		summary.AppleMoveTimeMinutes = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleMoveTimeGoal"]); ok {
		summary.AppleMoveTimeGoalMinutes = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleExerciseTime"]); ok {
		summary.AppleExerciseTimeMinutes = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleExerciseTimeGoal"]); ok {
		summary.AppleExerciseTimeGoalMinutes = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleStandHours"]); ok {
		summary.AppleStandHours = proto.Float64(v)
	}
	if v, ok := parseOptionalPositiveOrZeroFloat(attrs["appleStandHoursGoal"]); ok {
		summary.AppleStandHoursGoal = proto.Float64(v)
	}
	return summary, nil
}

func decodeXMLAudiogramElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthAudiogram, error) {
	attrs := xmlAttrMap(start.Attr)
	audiogram := &contract.HealthAudiogram{
		SourceName:    attrs["sourceName"],
		SourceVersion: attrs["sourceVersion"],
		Device:        attrs["device"],
	}
	if t, offset, _, ok := parseAppleHealthTimestamp(attrs["startDate"]); ok {
		audiogram.StartTime = timestamppb.New(t.UTC())
		audiogram.UtcOffsetSeconds = offset
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["endDate"]); ok {
		audiogram.EndTime = timestamppb.New(t.UTC())
	} else {
		audiogram.EndTime = audiogram.StartTime
	}
	if t, _, _, ok := parseAppleHealthTimestamp(attrs["creationDate"]); ok {
		audiogram.CreationTime = timestamppb.New(t.UTC())
	}
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "SensitivityPoint":
				pa := xmlAttrMap(t.Attr)
				freq, freqErr := strconv.ParseFloat(strings.TrimSpace(pa["frequencyValue"]), 64)
				if freqErr != nil {
					freq, freqErr = strconv.ParseFloat(strings.TrimSpace(pa["frequency"]), 64)
				}
				if freqErr == nil {
					pt := &contract.HealthAudiogramSensitivityPoint{FrequencyHertz: freq}
					if left, err := strconv.ParseFloat(strings.TrimSpace(pa["leftEarValue"]), 64); err == nil {
						pt.LeftEarDecibelsHl = proto.Float64(left)
					} else if left, err := strconv.ParseFloat(strings.TrimSpace(pa["leftEarSensitivity"]), 64); err == nil {
						pt.LeftEarDecibelsHl = proto.Float64(left)
					}
					if right, err := strconv.ParseFloat(strings.TrimSpace(pa["rightEarValue"]), 64); err == nil {
						pt.RightEarDecibelsHl = proto.Float64(right)
					} else if right, err := strconv.ParseFloat(strings.TrimSpace(pa["rightEarSensitivity"]), 64); err == nil {
						pt.RightEarDecibelsHl = proto.Float64(right)
					}
					audiogram.SensitivityPoints = append(audiogram.SensitivityPoints, pt)
				}
			case "MetadataEntry":
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					audiogram.MetadataEntries = append(audiogram.MetadataEntries, &contract.HealthMetadataEntry{Key: k, Value: ma["value"]})
				}
			}
			if err := decoder.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				audiogram.ReferenceIdentifier = deterministicHealthHash(
					"audiogram",
					formatUnixForHash(audiogram.GetStartTime()),
					audiogram.GetSourceName(),
				)
				return audiogram, nil
			}
		}
	}
}

func decodeXMLClinicalRecordElement(decoder *xml.Decoder, start xml.StartElement, aux appleHealthAuxiliaryOpener) (*contract.HealthClinicalRecord, error) {
	attrs := xmlAttrMap(start.Attr)
	if err := decoder.Skip(); err != nil {
		return nil, err
	}
	clinical := &contract.HealthClinicalRecord{
		ClinicalTypeIdentifier: attrs["type"],
		DisplayName:            strings.TrimSpace(attrs["displayName"]),
		SourceName:             attrs["sourceName"],
		SourceUrl:              attrs["sourceURL"],
		FhirVersion:            attrs["fhirVersion"],
		ResourceFileReference:  attrs["resourceFilePath"],
	}
	if t, offset, _, ok := parseAppleHealthTimestamp(attrs["receivedDate"]); ok {
		clinical.RecordedTime = timestamppb.New(t.UTC())
		clinical.UtcOffsetSeconds = offset
	}
	if clinical.GetResourceFileReference() != "" && aux != nil {
		if rc, _, err := aux.openRelative(clinical.GetResourceFileReference()); err == nil {
			rawJSON, readErr := io.ReadAll(io.LimitReader(rc, 4*1024*1024))
			_ = rc.Close()
			if readErr == nil && len(rawJSON) > 0 {
				clinical.FhirResourceJson = string(rawJSON)
				enrichClinicalRecordFromFHIRJSON(clinical, rawJSON)
			}
		}
	}
	if clinical.GetDisplayName() == "" {
		clinical.DisplayName = humanizeHealthKitIdentifier(clinical.GetClinicalTypeIdentifier())
	}
	id := strings.TrimSpace(attrs["identifier"])
	if id == "" {
		id = deterministicHealthHash("clinical", clinical.GetClinicalTypeIdentifier(), clinical.GetResourceFileReference(), clinical.GetDisplayName(), clinical.GetSourceName())
	}
	clinical.ReferenceIdentifier = id
	return clinical, nil
}

func decodeXMLElectrocardiogramElement(decoder *xml.Decoder, start xml.StartElement) (*contract.HealthElectrocardiogram, error) {
	attrs := xmlAttrMap(start.Attr)
	ecg := &contract.HealthElectrocardiogram{
		Classification: strings.TrimSpace(attrs["classification"]),
		SymptomsStatus: strings.TrimSpace(attrs["symptomsStatus"]),
		SourceName:     attrs["sourceName"],
		SourceVersion:  attrs["sourceVersion"],
		Device:         attrs["device"],
	}
	if t, offset, _, ok := parseAppleHealthTimestamp(attrs["startDate"]); ok {
		ecg.RecordedTime = timestamppb.New(t.UTC())
		ecg.UtcOffsetSeconds = offset
	}
	if hr, err := strconv.ParseFloat(strings.TrimSpace(attrs["averageHeartRate"]), 64); err == nil && hr > 0 {
		ecg.AverageHeartRateBeatsPerMinute = proto.Float64(hr)
	}
	if hz, err := strconv.ParseFloat(strings.TrimSpace(attrs["samplingFrequency"]), 64); err == nil && hz > 0 {
		ecg.SamplingFrequencyHertz = proto.Float64(hz)
	}
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "MetadataEntry" {
				ma := xmlAttrMap(t.Attr)
				if k := strings.TrimSpace(ma["key"]); k != "" {
					ecg.MetadataEntries = append(ecg.MetadataEntries, &contract.HealthMetadataEntry{Key: k, Value: ma["value"]})
				}
			}
			if err := decoder.Skip(); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				ecg.ReferenceIdentifier = deterministicHealthHash("ecg", formatUnixForHash(ecg.GetRecordedTime()), ecg.GetClassification())
				return ecg, nil
			}
		}
	}
}

// parseAppleHealthECGCsv streams an Apple Health electrocardiograms/ecg_*.csv
// file, capturing header metadata and counting voltage sample rows without
// storing thousands of raw microvolt floats in the index.
func parseAppleHealthECGCsv(r io.Reader, fileRef string) (*contract.HealthElectrocardiogram, error) {
	csvReader := csv.NewReader(r)
	csvReader.FieldsPerRecord = -1
	csvReader.LazyQuotes = true

	ecg := &contract.HealthElectrocardiogram{
		CsvFileReference: fileRef,
		SourceName:       "Apple Watch ECG",
	}
	var voltageCount int32
	for {
		row, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		if len(row) == 0 {
			continue
		}
		first := strings.TrimSpace(strings.TrimPrefix(row[0], "\ufeff"))
		if first == "" {
			continue
		}
		// Numeric single-column row is a microvolt sample measurement.
		if len(row) == 1 {
			if _, numErr := strconv.ParseFloat(strings.ReplaceAll(first, ",", "."), 64); numErr == nil {
				voltageCount++
			}
			continue
		}
		val := strings.TrimSpace(strings.Join(row[1:], ", "))
		lowerKey := strings.ToLower(first)
		switch {
		case strings.Contains(lowerKey, "classification"):
			ecg.Classification = val
		case strings.Contains(lowerKey, "recorded date"):
			if t, offset, _, ok := parseAppleHealthTimestamp(val); ok {
				ecg.RecordedTime = timestamppb.New(t.UTC())
				ecg.UtcOffsetSeconds = offset
			}
		case strings.Contains(lowerKey, "symptom"):
			ecg.SymptomsStatus = val
		case strings.Contains(lowerKey, "software version"):
			ecg.SourceVersion = val
		case lowerKey == "device":
			ecg.Device = val
		case strings.Contains(lowerKey, "sample rate"):
			if hz := extractLeadingFloat(val); hz > 0 {
				ecg.SamplingFrequencyHertz = proto.Float64(hz)
			}
		case strings.Contains(lowerKey, "heart rate"):
			if hr := extractLeadingFloat(val); hr > 0 {
				ecg.AverageHeartRateBeatsPerMinute = proto.Float64(hr)
			}
		case lowerKey == "lead" || lowerKey == "unit":
			ecg.MetadataEntries = append(ecg.MetadataEntries, &contract.HealthMetadataEntry{Key: first, Value: val})
		}
	}
	ecg.VoltageMeasurementCount = voltageCount
	if ecg.GetClassification() == "" && ecg.GetRecordedTime() == nil && voltageCount == 0 {
		return nil, nil
	}
	if ecg.GetClassification() == "" {
		ecg.Classification = "Electrocardiogram"
	}
	ecg.ReferenceIdentifier = deterministicHealthHash("ecg", formatUnixForHash(ecg.GetRecordedTime()), path.Base(fileRef), ecg.GetClassification())
	return ecg, nil
}

type fhirClinicalEnvelope struct {
	ResourceType   string `json:"resourceType"`
	ID             string `json:"id"`
	Status         string `json:"status"`
	EffectiveDate  string `json:"effectiveDateTime"`
	Issued         string `json:"issued"`
	RecordedDate   string `json:"recordedDate"`
	OccurrenceDate string `json:"occurrenceDateTime"`
	Code           struct {
		Text   string `json:"text"`
		Coding []struct {
			Display string `json:"display"`
		} `json:"coding"`
	} `json:"code"`
	VaccineCode struct {
		Text   string `json:"text"`
		Coding []struct {
			Display string `json:"display"`
		} `json:"coding"`
	} `json:"vaccineCode"`
	MedicationCodeableConcept struct {
		Text   string `json:"text"`
		Coding []struct {
			Display string `json:"display"`
		} `json:"coding"`
	} `json:"medicationCodeableConcept"`
}

func parseFHIRClinicalJSON(raw []byte, fileRef string) *contract.HealthClinicalRecord {
	var env fhirClinicalEnvelope
	if err := json.Unmarshal(raw, &env); err != nil || strings.TrimSpace(env.ResourceType) == "" {
		return nil
	}
	clinical := &contract.HealthClinicalRecord{
		ClinicalTypeIdentifier: env.ResourceType,
		ResourceFileReference:  fileRef,
		FhirResourceJson:       string(raw),
		SourceName:             "Apple Health Clinical Records",
	}
	enrichClinicalRecordFromFHIRJSON(clinical, raw)
	id := strings.TrimSpace(env.ID)
	if id == "" {
		sum := sha256.Sum256(raw)
		id = hex.EncodeToString(sum[:12])
	}
	clinical.ReferenceIdentifier = deterministicHealthHash("clinical", env.ResourceType, id, path.Base(fileRef))
	return clinical
}

func enrichClinicalRecordFromFHIRJSON(clinical *contract.HealthClinicalRecord, raw []byte) {
	var env fhirClinicalEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return
	}
	if clinical.GetDisplayName() == "" {
		for _, candidate := range []string{
			env.Code.Text,
			firstCodingDisplay(env.Code.Coding),
			env.VaccineCode.Text,
			firstCodingDisplay(env.VaccineCode.Coding),
			env.MedicationCodeableConcept.Text,
			firstCodingDisplay(env.MedicationCodeableConcept.Coding),
			env.ResourceType,
		} {
			if strings.TrimSpace(candidate) != "" {
				clinical.DisplayName = strings.TrimSpace(candidate)
				break
			}
		}
	}
	if clinical.GetRecordedTime() == nil {
		for _, dateText := range []string{env.EffectiveDate, env.OccurrenceDate, env.RecordedDate, env.Issued} {
			if t, offset, _, ok := parseAppleHealthTimestamp(dateText); ok {
				clinical.RecordedTime = timestamppb.New(t.UTC())
				clinical.UtcOffsetSeconds = offset
				break
			}
		}
	}
}

func firstCodingDisplay(codings []struct {
	Display string `json:"display"`
}) string {
	for _, c := range codings {
		if strings.TrimSpace(c.Display) != "" {
			return c.Display
		}
	}
	return ""
}

func parseGPXRouteSummaryFromOpener(aux appleHealthAuxiliaryOpener, fileRef string) (*contract.HealthWorkoutRouteSummary, error) {
	rc, resolvedName, err := aux.openRelative(fileRef)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	summary, _, err := parseGPXRouteStream(rc, resolvedName)
	return summary, err
}

func parseGPXRouteStream(r io.Reader, routeRef string) (*contract.HealthWorkoutRouteSummary, int64, error) {
	decoder := xml.NewDecoder(r)
	summary := &contract.HealthWorkoutRouteSummary{
		RouteFileReference: routeRef,
	}
	var (
		pointCount int32
		minLat     = math.MaxFloat64
		maxLat     = -math.MaxFloat64
		minLon     = math.MaxFloat64
		maxLon     = -math.MaxFloat64
		prevEle    float64
		hasPrevEle bool
		elevGain   float64
		elevLoss   float64
		firstUnix  int64
		inTrkpt    bool
		inEle      bool
		inTime     bool
	)
	for {
		tok, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "trkpt":
				inTrkpt = true
				attrs := xmlAttrMap(t.Attr)
				lat, latErr := strconv.ParseFloat(strings.TrimSpace(attrs["lat"]), 64)
				lon, lonErr := strconv.ParseFloat(strings.TrimSpace(attrs["lon"]), 64)
				if latErr == nil && lonErr == nil {
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
			case "ele":
				if inTrkpt {
					inEle = true
				}
			case "time":
				if inTrkpt && firstUnix == 0 {
					inTime = true
				}
			}
		case xml.CharData:
			if inEle {
				if ele, err := strconv.ParseFloat(strings.TrimSpace(string(t)), 64); err == nil && !math.IsNaN(ele) {
					if hasPrevEle {
						delta := ele - prevEle
						if delta > 0 {
							elevGain += delta
						} else if delta < 0 {
							elevLoss += -delta
						}
					}
					prevEle = ele
					hasPrevEle = true
				}
				inEle = false
			} else if inTime {
				if parsed, _, _, ok := parseAppleHealthTimestamp(string(t)); ok {
					firstUnix = parsed.Unix()
				}
				inTime = false
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "trkpt":
				inTrkpt = false
				inEle = false
				inTime = false
			case "ele":
				inEle = false
			case "time":
				inTime = false
			}
		}
	}
	if pointCount == 0 {
		return nil, 0, nil
	}
	summary.PointCount = pointCount
	summary.MinLatitude = proto.Float64(minLat)
	summary.MaxLatitude = proto.Float64(maxLat)
	summary.MinLongitude = proto.Float64(minLon)
	summary.MaxLongitude = proto.Float64(maxLon)
	if hasPrevEle {
		summary.TotalElevationGainMeters = proto.Float64(elevGain)
		summary.TotalElevationLossMeters = proto.Float64(elevLoss)
	}
	return summary, firstUnix, nil
}

func healthKitWorkoutActivityCaseName(rawType string) string {
	trimmed := strings.TrimSpace(rawType)
	trimmed = strings.TrimPrefix(trimmed, "HKWorkoutActivityType")
	if trimmed == "" {
		return "other"
	}
	runes := []rune(trimmed)
	runes[0] = []rune(strings.ToLower(string(runes[0])))[0]
	return string(runes)
}

func convertDurationToSeconds(val float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "s", "sec", "second", "seconds":
		return val
	case "h", "hr", "hour", "hours":
		return val * 3600
	default:
		// Apple Health XML <Workout duration="..." durationUnit="min"> defaults to minutes.
		return val * 60
	}
}

func convertDistanceToMeters(val float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "m", "meter", "meters":
		return val
	case "mi", "mile", "miles":
		return val * 1609.344
	case "yd", "yard", "yards":
		return val * 0.9144
	default:
		// km
		return val * 1000
	}
}

func convertEnergyToKilocalories(val float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "kj":
		return val / 4.184
	case "j":
		return val / 4184
	default:
		return val
	}
}

func convertSpeedToMetersPerSecond(val float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "km/hr", "km/h":
		return val / 3.6
	case "mi/hr", "mph":
		return val * 0.44704
	default:
		return val
	}
}

func parseElevationMetadataMeters(raw string) (float64, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0, false
	}
	val, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	if len(fields) >= 2 {
		switch strings.ToLower(fields[1]) {
		case "cm":
			return val / 100.0, true
		case "ft":
			return val * 0.3048, true
		case "km":
			return val * 1000.0, true
		}
	}
	return val, true
}

func parseOptionalPositiveOrZeroFloat(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func extractLeadingFloat(raw string) float64 {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}
