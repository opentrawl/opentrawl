package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const currentProviderAttemptTableDDL = `create table provider_location_transmission_attempt (
  attempt_id integer primary key,
  asset_id text not null references asset(id),
  provider_operation integer not null check (provider_operation between 1 and 4),
  request_sha256 blob not null,
  operation_state integer not null check (operation_state between 2 and 6),
  transmission_started_at text not null,
  completed_at text
)`

type retainedProviderAttempt struct {
	attemptID             int64
	assetID               string
	providerOperation     archive.ProviderLocationOperation
	requestSHA256         []byte
	operationState        locationwire.OperationState
	transmissionStartedAt string
	completedAt           sql.NullString
}

type schemaReprojectionPlan struct {
	attempts                         []retainedProviderAttempt
	attemptStateCounts               map[locationwire.OperationState]int
	attemptOperationCounts           map[archive.ProviderLocationOperation]int
	composedLocationRows             int
	byteIdenticalCurrentLocationRows int
	preservedProviderOutcomeRows     int
	preservedCurrentRows             int
	preservedCardRows                int
	preservedTableFingerprints       map[string][sha256.Size]byte
}

func reprojectCurrentArchiveSchema(ctx context.Context, archivePath, backupPath string, apply bool) error {
	openedStore, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return err
	}
	plan, err := buildSchemaReprojectionPlan(ctx, openedStore.DB())
	closeErr := openedStore.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	printSchemaReprojectionPlan(plan, apply)
	if !apply {
		fmt.Println("Archive unchanged. Apply requires the explicit apply flag and a new external SQLite backup path.")
		return nil
	}
	if strings.TrimSpace(backupPath) == "" || !filepath.IsAbs(backupPath) {
		return errors.New("an absolute schema reprojection backup path is required for apply")
	}
	if sameFilePath(archivePath, backupPath) {
		return errors.New("schema reprojection backup must differ from the target archive")
	}
	if _, err := os.Stat(backupPath); err == nil {
		return errors.New("schema reprojection backup already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		return err
	}
	if err := createAndValidateSQLiteBackup(ctx, archivePath, backupPath, len(plan.attempts), plan.composedLocationRows); err != nil {
		return err
	}
	if err := applySchemaReprojectionPlan(ctx, archivePath, plan); err != nil {
		return err
	}
	if err := verifyAppliedSchemaReprojection(ctx, archivePath, plan); err != nil {
		return err
	}
	fmt.Println("Archive schema reprojection committed in one transaction after a validated SQLite backup.")
	return nil
}

func buildSchemaReprojectionPlan(ctx context.Context, database *sql.DB) (*schemaReprojectionPlan, error) {
	if err := validateLegacyProviderAttemptColumns(ctx, database); err != nil {
		return nil, err
	}
	rows, err := database.QueryContext(ctx, `select attempt_id, asset_id, provider_operation, request_sha256, latest_outcome_proto, transmission_started_at, completed_at from provider_location_transmission_attempt order by attempt_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	plan := &schemaReprojectionPlan{
		attemptStateCounts:         make(map[locationwire.OperationState]int),
		attemptOperationCounts:     make(map[archive.ProviderLocationOperation]int),
		preservedTableFingerprints: make(map[string][sha256.Size]byte),
	}
	for rows.Next() {
		var attempt retainedProviderAttempt
		var providerOperation int
		var encodedOutcome []byte
		if err := rows.Scan(&attempt.attemptID, &attempt.assetID, &providerOperation, &attempt.requestSHA256, &encodedOutcome, &attempt.transmissionStartedAt, &attempt.completedAt); err != nil {
			return nil, err
		}
		attempt.providerOperation = archive.ProviderLocationOperation(providerOperation)
		state, request, exchange, input, err := decodeRetainedProviderAttempt(attempt.providerOperation, encodedOutcome)
		if err != nil {
			return nil, fmt.Errorf("decode retained provider attempt %d: %w", attempt.attemptID, err)
		}
		attempt.operationState = state
		if strings.TrimSpace(attempt.assetID) == "" || input == nil || input.GetAssetId() != attempt.assetID {
			return nil, errors.New("retained provider attempt has a mismatched asset identity")
		}
		if len(attempt.requestSHA256) != sha256.Size {
			return nil, errors.New("retained provider attempt has an invalid request digest")
		}
		requestBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(request)
		if err != nil {
			return nil, err
		}
		requestDigest := sha256.Sum256(requestBytes)
		if !bytes.Equal(attempt.requestSHA256, requestDigest[:]) {
			return nil, errors.New("retained provider attempt request digest does not match its typed request")
		}
		if exchange == nil || !exchange.GetTransmissionStarted() {
			return nil, errors.New("retained provider attempt does not record a started transmission")
		}
		if state < locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED || state > locationwire.OperationState_OPERATION_STATE_FAILED {
			return nil, errors.New("retained provider attempt state does not fit the current attempt contract")
		}
		if _, err := time.Parse(time.RFC3339Nano, attempt.transmissionStartedAt); err != nil {
			return nil, errors.New("retained provider attempt has an invalid start time")
		}
		terminal := state == locationwire.OperationState_OPERATION_STATE_SUCCEEDED || state == locationwire.OperationState_OPERATION_STATE_NO_RESULT || state == locationwire.OperationState_OPERATION_STATE_FAILED
		if terminal != attempt.completedAt.Valid {
			return nil, errors.New("retained provider attempt completion time disagrees with its typed state")
		}
		if attempt.completedAt.Valid {
			completedAt, err := time.Parse(time.RFC3339Nano, attempt.completedAt.String)
			if err != nil {
				return nil, errors.New("retained provider attempt has an invalid completion time")
			}
			startedAt, _ := time.Parse(time.RFC3339Nano, attempt.transmissionStartedAt)
			if completedAt.Before(startedAt) {
				return nil, errors.New("retained provider attempt completed before transmission started")
			}
		}
		plan.attempts = append(plan.attempts, attempt)
		plan.attemptStateCounts[state]++
		plan.attemptOperationCounts[attempt.providerOperation]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := validateComposedLocationRedundancy(ctx, database, plan); err != nil {
		return nil, err
	}
	if err := countPreservedSchemaRows(ctx, database, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func validateLegacyProviderAttemptColumns(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, `pragma table_info(provider_location_transmission_attempt)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	columns := []string{}
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		columns = append(columns, name)
	}
	wanted := []string{"attempt_id", "asset_id", "provider_operation", "request_sha256", "latest_outcome_proto", "transmission_started_at", "completed_at"}
	if len(columns) != len(wanted) {
		return errors.New("provider attempt table is not the exact legacy shape")
	}
	for index := range wanted {
		if columns[index] != wanted[index] {
			return errors.New("provider attempt table is not the exact legacy shape")
		}
	}
	return rows.Err()
}

func decodeRetainedProviderAttempt(operation archive.ProviderLocationOperation, encoded []byte) (locationwire.OperationState, proto.Message, *locationwire.ProviderExchange, *locationwire.CaptureLocationInput, error) {
	switch operation {
	case archive.ProviderLocationOperationAppleReverseGeocoding:
		outcome := new(locationwire.AcquireAppleReverseGeocodingEvidenceOutcome)
		if err := proto.Unmarshal(encoded, outcome); err != nil {
			return 0, nil, nil, nil, err
		}
		return outcome.GetExchange().GetState(), outcome.GetRequest(), outcome.GetExchange(), outcome.GetRequest().GetInput(), nil
	case archive.ProviderLocationOperationAppleNearbyPlace:
		outcome := new(locationwire.AcquireAppleNearbyPlaceEvidenceOutcome)
		if err := proto.Unmarshal(encoded, outcome); err != nil {
			return 0, nil, nil, nil, err
		}
		return outcome.GetExchange().GetState(), outcome.GetRequest(), outcome.GetExchange(), outcome.GetRequest().GetInput(), nil
	case archive.ProviderLocationOperationGeoapifyReverseGeocoding:
		outcome := new(locationwire.AcquireGeoapifyReverseGeocodingEvidenceOutcome)
		if err := proto.Unmarshal(encoded, outcome); err != nil {
			return 0, nil, nil, nil, err
		}
		return outcome.GetExchange().GetState(), outcome.GetRequest(), outcome.GetExchange(), outcome.GetRequest().GetInput(), nil
	case archive.ProviderLocationOperationGeoapifyNearbyPlace:
		outcome := new(locationwire.AcquireGeoapifyNearbyPlaceEvidenceOutcome)
		if err := proto.Unmarshal(encoded, outcome); err != nil {
			return 0, nil, nil, nil, err
		}
		return outcome.GetExchange().GetState(), outcome.GetRequest(), outcome.GetExchange(), outcome.GetRequest().GetInput(), nil
	default:
		return 0, nil, nil, nil, errors.New("retained provider attempt has an unknown operation")
	}
}

func validateComposedLocationRedundancy(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	if err := database.QueryRowContext(ctx, `select count(*) from composed_photo_location_evidence_outcome`).Scan(&plan.composedLocationRows); err != nil {
		return err
	}
	if err := database.QueryRowContext(ctx, `select count(*) from composed_photo_location_evidence_outcome composed join current_photo_location_evidence current using(asset_id) where composed.outcome_proto=current.outcome_proto`).Scan(&plan.byteIdenticalCurrentLocationRows); err != nil {
		return err
	}
	var currentRows int
	if err := database.QueryRowContext(ctx, `select count(*) from current_photo_location_evidence`).Scan(&currentRows); err != nil {
		return err
	}
	if plan.composedLocationRows != plan.byteIdenticalCurrentLocationRows || currentRows != plan.byteIdenticalCurrentLocationRows {
		return errors.New("composed location outcomes are not exactly redundant with current location evidence")
	}
	return nil
}

func countPreservedSchemaRows(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	providerTables := []string{"configured_known_place_match_outcome", "apple_reverse_geocoding_evidence_outcome", "apple_nearby_place_evidence_outcome", "geoapify_reverse_geocoding_evidence_outcome", "geoapify_nearby_place_evidence_outcome", "failed_location_operation_history"}
	currentTables := []string{"current_photo_location_evidence", "current_photo_media_evidence", "photo_update_asset_outcome"}
	cardTables := []string{"photo_card_generation", "photo_card_generation_operation", "photo_card_generation_transmission_attempt", "current_photo_card"}
	for _, group := range []struct {
		tables      []string
		destination *int
	}{{providerTables, &plan.preservedProviderOutcomeRows}, {currentTables, &plan.preservedCurrentRows}, {cardTables, &plan.preservedCardRows}} {
		for _, tableName := range group.tables {
			var count int
			if err := database.QueryRowContext(ctx, `select count(*) from `+tableName).Scan(&count); err != nil {
				return err
			}
			*group.destination += count
			fingerprint, err := fingerprintTable(ctx, database, tableName)
			if err != nil {
				return err
			}
			plan.preservedTableFingerprints[tableName] = fingerprint
		}
	}
	return nil
}

func fingerprintTable(ctx context.Context, database *sql.DB, tableName string) ([sha256.Size]byte, error) {
	rows, err := database.QueryContext(ctx, `select * from `+store.QuoteIdent(tableName)+` order by rowid`)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	digest := sha256.New()
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return [sha256.Size]byte{}, err
		}
		for _, value := range values {
			if err := writeFingerprintValue(digest, value); err != nil {
				return [sha256.Size]byte{}, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

type fingerprintWriter interface{ Write([]byte) (int, error) }

func writeFingerprintValue(destination fingerprintWriter, value any) error {
	var kind byte
	var encoded []byte
	switch typed := value.(type) {
	case nil:
		kind = 0
	case int64:
		kind = 1
		encoded = make([]byte, 8)
		binary.BigEndian.PutUint64(encoded, uint64(typed))
	case float64:
		kind = 2
		encoded = make([]byte, 8)
		binary.BigEndian.PutUint64(encoded, math.Float64bits(typed))
	case string:
		kind = 3
		encoded = []byte(typed)
	case []byte:
		kind = 4
		encoded = typed
	default:
		return fmt.Errorf("unsupported SQLite fingerprint value %T", value)
	}
	length := make([]byte, 8)
	binary.BigEndian.PutUint64(length, uint64(len(encoded)))
	_, err := destination.Write(append([]byte{kind}, append(length, encoded...)...))
	return err
}

func printSchemaReprojectionPlan(plan *schemaReprojectionPlan, apply bool) {
	mode := "dry run"
	if apply {
		mode = "approved apply"
	}
	fmt.Printf("Current archive schema reprojection — %s\n", mode)
	fmt.Printf("Provider attempts: %d typed rows; operations apple_reverse=%d apple_nearby=%d geoapify_reverse=%d geoapify_nearby=%d.\n", len(plan.attempts), plan.attemptOperationCounts[archive.ProviderLocationOperationAppleReverseGeocoding], plan.attemptOperationCounts[archive.ProviderLocationOperationAppleNearbyPlace], plan.attemptOperationCounts[archive.ProviderLocationOperationGeoapifyReverseGeocoding], plan.attemptOperationCounts[archive.ProviderLocationOperationGeoapifyNearbyPlace])
	fmt.Printf("Attempt states: transmission_started=%d response_retained=%d succeeded=%d no_result=%d failed=%d.\n", plan.attemptStateCounts[locationwire.OperationState_OPERATION_STATE_TRANSMISSION_STARTED], plan.attemptStateCounts[locationwire.OperationState_OPERATION_STATE_RESPONSE_RETAINED], plan.attemptStateCounts[locationwire.OperationState_OPERATION_STATE_SUCCEEDED], plan.attemptStateCounts[locationwire.OperationState_OPERATION_STATE_NO_RESULT], plan.attemptStateCounts[locationwire.OperationState_OPERATION_STATE_FAILED])
	fmt.Printf("Redundant composed location rows: %d; byte-identical current location rows: %d.\n", plan.composedLocationRows, plan.byteIdenticalCurrentLocationRows)
	fmt.Printf("Preserved rows outside the transformed table: provider=%d current=%d card=%d.\n", plan.preservedProviderOutcomeRows, plan.preservedCurrentRows, plan.preservedCardRows)
	fmt.Println("Planned DDL: rebuild only provider_location_transmission_attempt with typed operation_state, recreate provider_location_attempt_asset_idx, then drop only composed_photo_location_evidence_outcome. Provider outcomes, current evidence, cards, attempt identities/digests/times and every other table remain untouched.")
}

func createAndValidateSQLiteBackup(ctx context.Context, archivePath, backupPath string, expectedAttempts, expectedComposedRows int) error {
	openedStore, err := store.Open(ctx, store.Options{Path: archivePath})
	if err != nil {
		return err
	}
	escapedBackupPath := strings.ReplaceAll(backupPath, "'", "''")
	_, backupErr := openedStore.DB().ExecContext(ctx, `vacuum into '`+escapedBackupPath+`'`)
	closeErr := openedStore.Close()
	if err := errors.Join(backupErr, closeErr); err != nil {
		return err
	}
	backupStore, err := store.OpenReadOnly(ctx, backupPath)
	if err != nil {
		return err
	}
	defer func() { _ = backupStore.Close() }()
	var integrity string
	if err := backupStore.DB().QueryRowContext(ctx, `pragma integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("schema reprojection backup failed integrity check")
	}
	var attempts, composed int
	if err := backupStore.DB().QueryRowContext(ctx, `select count(*) from provider_location_transmission_attempt`).Scan(&attempts); err != nil {
		return err
	}
	if err := backupStore.DB().QueryRowContext(ctx, `select count(*) from composed_photo_location_evidence_outcome`).Scan(&composed); err != nil {
		return err
	}
	if attempts != expectedAttempts || composed != expectedComposedRows {
		return errors.New("schema reprojection backup row counts differ from the validated source")
	}
	return nil
}

func applySchemaReprojectionPlan(ctx context.Context, archivePath string, plan *schemaReprojectionPlan) error {
	openedStore, err := store.Open(ctx, store.Options{Path: archivePath})
	if err != nil {
		return err
	}
	defer func() { _ = openedStore.Close() }()
	return withTransaction(ctx, openedStore.DB(), func(transaction *sql.Tx) error {
		if _, err := transaction.ExecContext(ctx, `alter table provider_location_transmission_attempt rename to provider_location_transmission_attempt_legacy_reprojection`); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, currentProviderAttemptTableDDL); err != nil {
			return err
		}
		statement, err := transaction.PrepareContext(ctx, `insert into provider_location_transmission_attempt(attempt_id, asset_id, provider_operation, request_sha256, operation_state, transmission_started_at, completed_at) values (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, attempt := range plan.attempts {
			if _, err := statement.ExecContext(ctx, attempt.attemptID, attempt.assetID, attempt.providerOperation, attempt.requestSHA256, attempt.operationState, attempt.transmissionStartedAt, attempt.completedAt); err != nil {
				_ = statement.Close()
				return err
			}
		}
		if err := statement.Close(); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `drop table provider_location_transmission_attempt_legacy_reprojection`); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `create index provider_location_attempt_asset_idx on provider_location_transmission_attempt(asset_id, provider_operation, attempt_id desc)`); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `drop table composed_photo_location_evidence_outcome`); err != nil {
			return err
		}
		return nil
	})
}

func verifyAppliedSchemaReprojection(ctx context.Context, archivePath string, plan *schemaReprojectionPlan) error {
	openedStore, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = openedStore.Close() }()
	var integrity string
	if err := openedStore.DB().QueryRowContext(ctx, `pragma integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("reprojected archive failed integrity check")
	}
	var attemptRows int
	if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from provider_location_transmission_attempt`).Scan(&attemptRows); err != nil || attemptRows != len(plan.attempts) {
		return errors.New("reprojected provider attempt count differs from the validated plan")
	}
	rows, err := openedStore.DB().QueryContext(ctx, `select attempt_id, asset_id, provider_operation, request_sha256, operation_state, transmission_started_at, completed_at from provider_location_transmission_attempt order by attempt_id`)
	if err != nil {
		return err
	}
	for index := 0; rows.Next(); index++ {
		if index >= len(plan.attempts) {
			_ = rows.Close()
			return errors.New("reprojected provider attempts exceed the validated plan")
		}
		var observed retainedProviderAttempt
		var providerOperation, operationState int
		if err := rows.Scan(&observed.attemptID, &observed.assetID, &providerOperation, &observed.requestSHA256, &operationState, &observed.transmissionStartedAt, &observed.completedAt); err != nil {
			_ = rows.Close()
			return err
		}
		observed.providerOperation = archive.ProviderLocationOperation(providerOperation)
		observed.operationState = locationwire.OperationState(operationState)
		expected := plan.attempts[index]
		if observed.attemptID != expected.attemptID || observed.assetID != expected.assetID || observed.providerOperation != expected.providerOperation || !bytes.Equal(observed.requestSHA256, expected.requestSHA256) || observed.operationState != expected.operationState || observed.transmissionStartedAt != expected.transmissionStartedAt || observed.completedAt != expected.completedAt {
			_ = rows.Close()
			return errors.New("reprojected provider attempt differs from the validated typed row")
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for tableName, expectedFingerprint := range plan.preservedTableFingerprints {
		observedFingerprint, err := fingerprintTable(ctx, openedStore.DB(), tableName)
		if err != nil {
			return err
		}
		if observedFingerprint != expectedFingerprint {
			return fmt.Errorf("preserved table %s changed during schema reprojection", tableName)
		}
	}
	var composedTableCount int
	if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name='composed_photo_location_evidence_outcome'`).Scan(&composedTableCount); err != nil || composedTableCount != 0 {
		return errors.New("redundant composed location table remains after reprojection")
	}
	var attemptIndexCount int
	if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from sqlite_master where type='index' and name='provider_location_attempt_asset_idx' and tbl_name='provider_location_transmission_attempt'`).Scan(&attemptIndexCount); err != nil || attemptIndexCount != 1 {
		return errors.New("provider attempt index was not recreated on the current table")
	}
	return nil
}
