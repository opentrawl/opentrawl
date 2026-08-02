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

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const currentPhotoTextExtractionTableDDL = `create table photo_text_extraction (
  asset_id text primary key references asset(id),
  input_sha256 blob not null,
  request_text text not null,
  response_body blob,
  response_rejected integer not null default 0 check (response_rejected in (0, 1)),
  response_retained_at text,
  model_identifier text,
  thread_identifier text,
  turn_identifier text
)`

const currentPhotoCardGenerationTableDDL = `create table photo_card_generation (
  asset_id text primary key references asset(id),
  input_sha256 blob not null,
  request_text text not null,
  response_body blob,
  response_rejected integer not null default 0 check (response_rejected in (0, 1)),
  response_retained_at text,
  model_identifier text,
  thread_identifier text,
  turn_identifier text,
  descriptions_repair_request_text text,
  descriptions_repair_response_body blob,
  descriptions_repair_response_rejected integer not null default 0 check (descriptions_repair_response_rejected in (0, 1)),
  descriptions_repair_response_retained_at text,
  descriptions_repair_thread_identifier text,
  descriptions_repair_turn_identifier text,
  completed_at text
)`

const currentPhotoModelGenerationOperationTableDDL = `create table photo_model_generation_operation (
  asset_id text not null references asset(id),
  input_sha256 blob not null,
  operation_phase integer not null check (operation_phase in (1, 2, 3)),
  operation_state integer not null check (operation_state between 1 and 5),
  thread_identifier text not null default '',
  turn_identifier text not null default '',
  failure_detail text not null default '',
  changed_at text not null,
  primary key (asset_id, input_sha256, operation_phase)
)`

const currentPhotoModelGenerationTransmissionAttemptTableDDL = `create table photo_model_generation_transmission_attempt (
  attempt_id integer primary key,
  asset_id text not null references asset(id),
  input_sha256 blob not null,
  operation_phase integer not null check (operation_phase in (1, 2, 3)),
  operation_state integer not null check (operation_state between 2 and 5),
  thread_identifier text not null,
  turn_identifier text not null,
  failure_detail text not null default '',
  input_tokens integer,
  cached_input_tokens integer,
  output_tokens integer,
  reasoning_output_tokens integer,
  total_tokens integer,
  transmission_started_at text not null,
  completed_at text
)`

var preservedPhotoArchiveTables = []string{
	"source_library",
	"crawl_snapshot",
	"crawl_seen_asset",
	"update_cursor_state",
	"asset",
	"asset_resource",
	"album_membership",
	"location_observation",
	"known_place",
	"configured_known_place_match_outcome",
	"apple_reverse_geocoding_evidence_outcome",
	"apple_nearby_place_evidence_outcome",
	"geoapify_reverse_geocoding_evidence_outcome",
	"geoapify_nearby_place_evidence_outcome",
	"failed_location_operation_history",
	"provider_location_transmission_attempt",
	"current_photo_location_evidence",
	"current_photo_media_evidence",
	"asset_fts",
	"short_refs",
}

var rejectedResponseFollowUpPreservedTables = append(append([]string{}, preservedPhotoArchiveTables...),
	"photo_model_generation_operation",
	"photo_model_generation_transmission_attempt",
)

const obsoleteSemanticCardPromptPrefix = "Role: Build every remaining semantic section of a useful personal photo-library card from the current rendered image, retained literal OCR and checked factual evidence.\n\nGoal: Verify and, where necessary, correct the retained OCR from the pixels; then decide what the photo is of and where it depicts. OpenTrawl will mechanically apply your correction patch and combine the corrected OCR with your semantic sections into one stored card.\n\nSuccess criteria:\n- Descriptions state only visible properties, composition and distinguishing image detail. Never claim why or how the photographer captured the image, or whether the capture was intentional, accidental or incidental."

type obsoleteSemanticCardContractRow struct {
	assetID                  string
	inputSHA256              []byte
	hasMatchingCurrentCard   bool
	hasMatchingSearchRow     bool
	hasMatchingStoredOutcome bool
}

type retainedPhotoModelGenerationOperation struct {
	assetID          string
	inputSHA256      []byte
	operationPhase   int
	operationState   int
	threadIdentifier string
	turnIdentifier   string
	failureDetail    string
	changedAt        string
}

type retainedPhotoModelGenerationTransmissionAttempt struct {
	attemptID             int64
	assetID               string
	inputSHA256           []byte
	operationPhase        int
	operationState        int
	threadIdentifier      string
	turnIdentifier        string
	failureDetail         string
	transmissionStartedAt string
	completedAt           sql.NullString
}

type schemaReprojectionPlan struct {
	alreadyCurrent                    bool
	addRejectedResponseColumns        bool
	photoTextExtractionRows           int
	photoCardGenerationRows           int
	photoCardGenerationOperationRows  []retainedPhotoModelGenerationOperation
	photoCardGenerationAttemptRows    []retainedPhotoModelGenerationTransmissionAttempt
	currentPhotoCardRows              int
	photoCardObservationSearchRows    int
	cardStoredUpdateOutcomeRows       int
	retainedPhotoUpdateOutcomeRows    int
	retainedPhotoUpdateOutcomeDigest  [sha256.Size]byte
	retainedObservationSearchDigest   [sha256.Size]byte
	preservedTableFingerprints        map[string][sha256.Size]byte
	predecessorModelSchemaFingerprint [sha256.Size]byte
	currentModelSchemaFingerprint     [sha256.Size]byte
	invalidateObsoleteSemanticCards   bool
	obsoleteSemanticCardRows          []obsoleteSemanticCardContractRow
	obsoleteSemanticCardRowsDigest    [sha256.Size]byte
	retainedProjectionFingerprints    map[string][sha256.Size]byte
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
	if plan.alreadyCurrent {
		fmt.Println("Archive unchanged. The live OCR-first model schema is already current.")
		return nil
	}
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
	if err := createAndValidateSQLiteBackup(ctx, archivePath, backupPath, plan); err != nil {
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
	current, err := archiveHasCurrentPhotoModelSchema(ctx, database)
	if err != nil {
		return nil, err
	}
	plan := &schemaReprojectionPlan{
		alreadyCurrent:                 current,
		preservedTableFingerprints:     make(map[string][sha256.Size]byte),
		retainedProjectionFingerprints: make(map[string][sha256.Size]byte),
	}
	if current {
		if err := validateCurrentPhotoModelSchema(ctx, database); err != nil {
			return nil, err
		}
		plan.currentModelSchemaFingerprint, err = photoModelSchemaFingerprint(ctx, database, []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
		if err != nil {
			return nil, err
		}
		expectedFingerprint, err := expectedCurrentPhotoModelSchemaFingerprint(ctx)
		if err != nil {
			return nil, err
		}
		if plan.currentModelSchemaFingerprint != expectedFingerprint {
			return nil, errors.New("current photo model tables do not match the exact live schema")
		}
		if err := loadObsoleteSemanticCardContractPlan(ctx, database, plan); err != nil {
			return nil, err
		}
		return plan, nil
	}
	currentOCRFirstSchema, err := archiveHasOCRFirstPhotoModelSchema(ctx, database)
	if err != nil {
		return nil, err
	}
	if currentOCRFirstSchema {
		if err := validateRejectedResponsePredecessorPhotoModelSchema(ctx, database); err != nil {
			return nil, err
		}
		plan.addRejectedResponseColumns = true
		plan.predecessorModelSchemaFingerprint, err = photoModelSchemaFingerprint(ctx, database, []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
		if err != nil {
			return nil, err
		}
		for _, tableName := range rejectedResponseFollowUpPreservedTables {
			fingerprint, err := fingerprintTable(ctx, database, tableName)
			if err != nil {
				return nil, err
			}
			plan.preservedTableFingerprints[tableName] = fingerprint
		}
		if err := database.QueryRowContext(ctx, `select count(*) from photo_text_extraction`).Scan(&plan.photoTextExtractionRows); err != nil {
			return nil, err
		}
		if err := database.QueryRowContext(ctx, `select count(*) from photo_card_generation`).Scan(&plan.photoCardGenerationRows); err != nil {
			return nil, err
		}
		for destination, query := range map[*int]string{
			&plan.currentPhotoCardRows:           `select count(*) from current_photo_card`,
			&plan.photoCardObservationSearchRows: `select count(*) from observation_fts where id like 'photo-card:%'`,
			&plan.cardStoredUpdateOutcomeRows:    `select count(*) from photo_update_asset_outcome where outcome_kind='card_stored'`,
			&plan.retainedPhotoUpdateOutcomeRows: `select count(*) from photo_update_asset_outcome where outcome_kind<>'card_stored'`,
		} {
			if err := database.QueryRowContext(ctx, query).Scan(destination); err != nil {
				return nil, err
			}
		}
		plan.retainedPhotoUpdateOutcomeDigest, err = fingerprintQuery(ctx, database, `select * from photo_update_asset_outcome where outcome_kind<>'card_stored' order by asset_id`)
		if err != nil {
			return nil, err
		}
		plan.retainedObservationSearchDigest, err = fingerprintQuery(ctx, database, `select * from observation_fts where id not like 'photo-card:%' order by rowid`)
		if err != nil {
			return nil, err
		}
		return plan, nil
	}
	if err := validatePredecessorPhotoModelSchema(ctx, database); err != nil {
		return nil, err
	}
	plan.predecessorModelSchemaFingerprint, err = photoModelSchemaFingerprint(ctx, database, []string{"photo_card_generation", "photo_card_generation_operation", "photo_card_generation_transmission_attempt"})
	if err != nil {
		return nil, err
	}
	if err := loadRowsToReproject(ctx, database, plan); err != nil {
		return nil, err
	}
	if err := fingerprintPreservedPhotoArchiveTables(ctx, database, plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func archiveHasCurrentPhotoModelSchema(ctx context.Context, database *sql.DB) (bool, error) {
	var rejectedResponseColumns int
	err := database.QueryRowContext(ctx, `select count(*) from pragma_table_info('photo_text_extraction') where name='response_rejected'`).Scan(&rejectedResponseColumns)
	return rejectedResponseColumns == 1, err
}

func archiveHasOCRFirstPhotoModelSchema(ctx context.Context, database *sql.DB) (bool, error) {
	var currentTables int
	err := database.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name in ('photo_text_extraction', 'photo_model_generation_operation', 'photo_model_generation_transmission_attempt')`).Scan(&currentTables)
	return currentTables == 3, err
}

func loadObsoleteSemanticCardContractPlan(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	rows, err := database.QueryContext(ctx, `
select generation.asset_id, generation.input_sha256,
       exists(select 1 from current_photo_card card where card.asset_id=generation.asset_id and card.input_sha256=generation.input_sha256),
       exists(select 1 from current_photo_card card join observation_fts search on search.id='photo-card:' || card.asset_id where card.asset_id=generation.asset_id and card.input_sha256=generation.input_sha256),
       exists(select 1 from current_photo_card card join photo_update_asset_outcome outcome on outcome.asset_id=card.asset_id and outcome.outcome_kind='card_stored' where card.asset_id=generation.asset_id and card.input_sha256=generation.input_sha256)
from photo_card_generation generation
where substr(generation.request_text, 1, length(?))=?
order by generation.asset_id`, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix)
	if err != nil {
		return err
	}
	for rows.Next() {
		var row obsoleteSemanticCardContractRow
		if err := rows.Scan(&row.assetID, &row.inputSHA256, &row.hasMatchingCurrentCard, &row.hasMatchingSearchRow, &row.hasMatchingStoredOutcome); err != nil {
			_ = rows.Close()
			return err
		}
		plan.obsoleteSemanticCardRows = append(plan.obsoleteSemanticCardRows, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(plan.obsoleteSemanticCardRows) == 0 {
		return nil
	}
	plan.alreadyCurrent = false
	plan.invalidateObsoleteSemanticCards = true
	plan.obsoleteSemanticCardRowsDigest, err = fingerprintQuery(ctx, database, `select * from photo_card_generation where substr(request_text, 1, length(?))=? order by asset_id`, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix)
	if err != nil {
		return err
	}
	for _, tableName := range append(append([]string{}, rejectedResponseFollowUpPreservedTables...), "photo_text_extraction") {
		fingerprint, err := fingerprintTable(ctx, database, tableName)
		if err != nil {
			return err
		}
		plan.preservedTableFingerprints[tableName] = fingerprint
	}
	plan.retainedProjectionFingerprints["photo_card_generation"], err = fingerprintQuery(ctx, database, `select * from photo_card_generation where not (substr(request_text, 1, length(?))=?) order by rowid`, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix)
	if err != nil {
		return err
	}
	matchingCurrentCardAssetIDs := make([]string, 0, len(plan.obsoleteSemanticCardRows))
	for _, row := range plan.obsoleteSemanticCardRows {
		if row.hasMatchingCurrentCard {
			matchingCurrentCardAssetIDs = append(matchingCurrentCardAssetIDs, row.assetID)
		}
	}
	for tableName, projection := range map[string]struct {
		query          string
		excludedColumn string
		excludedValues []string
	}{
		"current_photo_card":         {`select * from current_photo_card`, "asset_id", matchingCurrentCardAssetIDs},
		"observation_fts":            {`select * from observation_fts`, "id", prefixedPhotoCardIdentifiers(matchingCurrentCardAssetIDs)},
		"photo_update_asset_outcome": {`select * from photo_update_asset_outcome`, "asset_id", matchingCurrentCardAssetIDs},
	} {
		query, arguments := excludingColumnValuesQuery(projection.query, projection.excludedColumn, projection.excludedValues)
		plan.retainedProjectionFingerprints[tableName], err = fingerprintQuery(ctx, database, query+` order by rowid`, arguments...)
		if err != nil {
			return err
		}
	}
	return nil
}

func excludingColumnValuesQuery(query, columnName string, values []string) (string, []any) {
	if len(values) == 0 {
		return query, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	arguments := make([]any, len(values))
	for index, value := range values {
		arguments[index] = value
	}
	return query + ` where ` + store.QuoteIdent(columnName) + ` not in (` + placeholders + `)`, arguments
}

func prefixedPhotoCardIdentifiers(assetIDs []string) []string {
	identifiers := make([]string, len(assetIDs))
	for index, assetID := range assetIDs {
		identifiers[index] = "photo-card:" + assetID
	}
	return identifiers
}

func validateRejectedResponsePredecessorPhotoModelSchema(ctx context.Context, database *sql.DB) error {
	wanted := map[string][]string{
		"photo_text_extraction":                       {"asset_id", "input_sha256", "request_text", "response_body", "response_retained_at", "model_identifier", "thread_identifier", "turn_identifier"},
		"photo_card_generation":                       {"asset_id", "input_sha256", "request_text", "response_body", "response_retained_at", "model_identifier", "thread_identifier", "turn_identifier", "descriptions_repair_request_text", "descriptions_repair_response_body", "descriptions_repair_response_retained_at", "descriptions_repair_thread_identifier", "descriptions_repair_turn_identifier", "completed_at"},
		"photo_model_generation_operation":            {"asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "changed_at"},
		"photo_model_generation_transmission_attempt": {"attempt_id", "asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "transmission_started_at", "completed_at"},
	}
	for tableName, columns := range wanted {
		if err := validateExactTableColumns(ctx, database, tableName, columns); err != nil {
			return fmt.Errorf("rejected-response predecessor %s: %w", tableName, err)
		}
	}
	return nil
}

func validatePredecessorPhotoModelSchema(ctx context.Context, database *sql.DB) error {
	wanted := map[string][]string{
		"photo_card_generation":                      {"asset_id", "input_sha256", "request_text", "response_body", "response_retained_at", "model_identifier", "thread_identifier", "turn_identifier", "descriptions_repair_request_text", "descriptions_repair_response_body", "descriptions_repair_response_retained_at", "descriptions_repair_thread_identifier", "descriptions_repair_turn_identifier", "completed_at", "failure_text"},
		"photo_card_generation_operation":            {"asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "changed_at"},
		"photo_card_generation_transmission_attempt": {"attempt_id", "asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "transmission_started_at", "completed_at"},
	}
	for tableName, columns := range wanted {
		if err := validateExactTableColumns(ctx, database, tableName, columns); err != nil {
			return fmt.Errorf("predecessor %s: %w", tableName, err)
		}
	}
	return nil
}

func validateCurrentPhotoModelSchema(ctx context.Context, database *sql.DB) error {
	var obsoleteTables int
	if err := database.QueryRowContext(ctx, `select count(*) from sqlite_master where type='table' and name in ('photo_card_generation_operation', 'photo_card_generation_transmission_attempt')`).Scan(&obsoleteTables); err != nil {
		return err
	}
	if obsoleteTables != 0 {
		return errors.New("obsolete photo card model tables remain beside the current schema")
	}
	wanted := map[string][]string{
		"photo_text_extraction":                       {"asset_id", "input_sha256", "request_text", "response_body", "response_rejected", "response_retained_at", "model_identifier", "thread_identifier", "turn_identifier"},
		"photo_card_generation":                       {"asset_id", "input_sha256", "request_text", "response_body", "response_rejected", "response_retained_at", "model_identifier", "thread_identifier", "turn_identifier", "descriptions_repair_request_text", "descriptions_repair_response_body", "descriptions_repair_response_rejected", "descriptions_repair_response_retained_at", "descriptions_repair_thread_identifier", "descriptions_repair_turn_identifier", "completed_at"},
		"photo_model_generation_operation":            {"asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "changed_at"},
		"photo_model_generation_transmission_attempt": {"attempt_id", "asset_id", "input_sha256", "operation_phase", "operation_state", "thread_identifier", "turn_identifier", "failure_detail", "input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens", "transmission_started_at", "completed_at"},
	}
	for tableName, columns := range wanted {
		if err := validateExactTableColumns(ctx, database, tableName, columns); err != nil {
			return fmt.Errorf("current %s: %w", tableName, err)
		}
	}
	return nil
}

func validateExactTableColumns(ctx context.Context, database *sql.DB, tableName string, wanted []string) error {
	rows, err := database.QueryContext(ctx, `pragma table_info(`+store.QuoteIdent(tableName)+`)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var observed []string
	for rows.Next() {
		var position, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		observed = append(observed, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(observed) != len(wanted) {
		return fmt.Errorf("columns differ: got %v", observed)
	}
	for index := range wanted {
		if observed[index] != wanted[index] {
			return fmt.Errorf("columns differ: got %v", observed)
		}
	}
	return nil
}

func loadRowsToReproject(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	if err := database.QueryRowContext(ctx, `select count(*) from photo_card_generation`).Scan(&plan.photoCardGenerationRows); err != nil {
		return err
	}
	operationRows, err := database.QueryContext(ctx, `select asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at from photo_card_generation_operation order by asset_id, input_sha256, operation_phase`)
	if err != nil {
		return err
	}
	for operationRows.Next() {
		var row retainedPhotoModelGenerationOperation
		if err := operationRows.Scan(&row.assetID, &row.inputSHA256, &row.operationPhase, &row.operationState, &row.threadIdentifier, &row.turnIdentifier, &row.failureDetail, &row.changedAt); err != nil {
			_ = operationRows.Close()
			return err
		}
		row.operationPhase++
		plan.photoCardGenerationOperationRows = append(plan.photoCardGenerationOperationRows, row)
	}
	if err := errors.Join(operationRows.Err(), operationRows.Close()); err != nil {
		return err
	}
	attemptRows, err := database.QueryContext(ctx, `select attempt_id, asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, transmission_started_at, completed_at from photo_card_generation_transmission_attempt order by attempt_id`)
	if err != nil {
		return err
	}
	for attemptRows.Next() {
		var row retainedPhotoModelGenerationTransmissionAttempt
		if err := attemptRows.Scan(&row.attemptID, &row.assetID, &row.inputSHA256, &row.operationPhase, &row.operationState, &row.threadIdentifier, &row.turnIdentifier, &row.failureDetail, &row.transmissionStartedAt, &row.completedAt); err != nil {
			_ = attemptRows.Close()
			return err
		}
		row.operationPhase++
		plan.photoCardGenerationAttemptRows = append(plan.photoCardGenerationAttemptRows, row)
	}
	if err := errors.Join(attemptRows.Err(), attemptRows.Close()); err != nil {
		return err
	}
	for destination, query := range map[*int]string{
		&plan.currentPhotoCardRows:           `select count(*) from current_photo_card`,
		&plan.photoCardObservationSearchRows: `select count(*) from observation_fts where id like 'photo-card:%'`,
		&plan.cardStoredUpdateOutcomeRows:    `select count(*) from photo_update_asset_outcome where outcome_kind='card_stored'`,
		&plan.retainedPhotoUpdateOutcomeRows: `select count(*) from photo_update_asset_outcome where outcome_kind<>'card_stored'`,
	} {
		if err := database.QueryRowContext(ctx, query).Scan(destination); err != nil {
			return err
		}
	}
	plan.retainedPhotoUpdateOutcomeDigest, err = fingerprintQuery(ctx, database, `select * from photo_update_asset_outcome where outcome_kind<>'card_stored' order by asset_id`)
	if err != nil {
		return err
	}
	plan.retainedObservationSearchDigest, err = fingerprintQuery(ctx, database, `select * from observation_fts where id not like 'photo-card:%' order by rowid`)
	if err != nil {
		return err
	}
	return nil
}

func fingerprintPreservedPhotoArchiveTables(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	for _, tableName := range preservedPhotoArchiveTables {
		fingerprint, err := fingerprintTable(ctx, database, tableName)
		if err != nil {
			return err
		}
		plan.preservedTableFingerprints[tableName] = fingerprint
	}
	return nil
}

func validateCurrentReprojectionResultIsEmpty(ctx context.Context, database *sql.DB) error {
	queries := []string{
		`select count(*) from photo_text_extraction`,
		`select count(*) from photo_card_generation`,
		`select count(*) from current_photo_card`,
		`select count(*) from observation_fts where id like 'photo-card:%'`,
		`select count(*) from photo_update_asset_outcome where outcome_kind='card_stored'`,
	}
	for _, query := range queries {
		var rows int
		if err := database.QueryRowContext(ctx, query).Scan(&rows); err != nil {
			return err
		}
		if rows != 0 {
			return errors.New("current OCR-first schema contains new model work; the one-off reprojection is already complete")
		}
	}
	return nil
}

func fingerprintTable(ctx context.Context, database *sql.DB, tableName string) ([sha256.Size]byte, error) {
	return fingerprintQuery(ctx, database, `select * from `+store.QuoteIdent(tableName)+` order by rowid`)
}

func photoModelSchemaFingerprint(ctx context.Context, database *sql.DB, tableNames []string) ([sha256.Size]byte, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tableNames)), ",")
	arguments := make([]any, len(tableNames))
	for index := range tableNames {
		arguments[index] = tableNames[index]
	}
	return fingerprintQuery(ctx, database, `select name, sql from sqlite_master where type='table' and name in (`+placeholders+`) order by name`, arguments...)
}

func expectedCurrentPhotoModelSchemaFingerprint(ctx context.Context) ([sha256.Size]byte, error) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	defer func() { _ = database.Close() }()
	for _, statement := range []string{currentPhotoTextExtractionTableDDL, currentPhotoCardGenerationTableDDL, currentPhotoModelGenerationOperationTableDDL, currentPhotoModelGenerationTransmissionAttemptTableDDL} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	return photoModelSchemaFingerprint(ctx, database, []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
}

func fingerprintQuery(ctx context.Context, database *sql.DB, query string, arguments ...any) ([sha256.Size]byte, error) {
	rows, err := database.QueryContext(ctx, query, arguments...)
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
	if plan.alreadyCurrent {
		fmt.Printf("Current archive schema reprojection — already current\nModel schema fingerprint: %x.\n", plan.currentModelSchemaFingerprint)
		return
	}
	mode := "dry run"
	if apply {
		mode = "approved apply"
	}
	fmt.Printf("Current archive schema reprojection — %s\n", mode)
	if plan.invalidateObsoleteSemanticCards {
		currentCards, searchRows, storedOutcomes := 0, 0, 0
		for _, row := range plan.obsoleteSemanticCardRows {
			if row.hasMatchingCurrentCard {
				currentCards++
			}
			if row.hasMatchingSearchRow {
				searchRows++
			}
			if row.hasMatchingStoredOutcome {
				storedOutcomes++
			}
		}
		fmt.Printf("Invalidate obsolete semantic-card contract: %d generations, %d matching current cards, %d matching card search rows and %d matching card-stored outcomes.\n", len(plan.obsoleteSemanticCardRows), currentCards, searchRows, storedOutcomes)
		fmt.Println("Retain first-pass OCR, all model operation and attempt history, and byte-identical source, media, known-place, Apple, Geoapify and current-location evidence. No provider or model call runs.")
		fmt.Printf("Current model schema fingerprint remains %x; no table or column changes are required.\n", plan.currentModelSchemaFingerprint)
		return
	}
	if plan.addRejectedResponseColumns {
		fmt.Printf("Invalidate obsolete model product: %d text extractions, %d semantic-card generations, %d current cards, %d card search observations and %d card-stored outcomes.\n", plan.photoTextExtractionRows, plan.photoCardGenerationRows, plan.currentPhotoCardRows, plan.photoCardObservationSearchRows, plan.cardStoredUpdateOutcomeRows)
		fmt.Printf("Retain %d non-card update outcomes, all model attempt history, and byte-identical source, media, known-place, Apple, Geoapify and current-location evidence. No provider or model call runs.\n", plan.retainedPhotoUpdateOutcomeRows)
		fmt.Printf("Predecessor model schema fingerprint: %x.\n", plan.predecessorModelSchemaFingerprint)
		fmt.Println("Planned DDL: rebuild empty photo_text_extraction and photo_card_generation with the exact live rejected-response columns; remove only obsolete card projections and completion outcomes.")
		return
	}
	fmt.Printf("Retain model history: %d operations and %d attempts; remap semantic-card phase 1→2 and description-repair phase 2→3; old token usage remains unknown.\n", len(plan.photoCardGenerationOperationRows), len(plan.photoCardGenerationAttemptRows))
	fmt.Printf("Invalidate obsolete model product: %d whole-card generations, %d current cards, %d card search observations and %d card-stored outcomes.\n", plan.photoCardGenerationRows, plan.currentPhotoCardRows, plan.photoCardObservationSearchRows, plan.cardStoredUpdateOutcomeRows)
	fmt.Printf("Retain %d non-card update outcomes and byte-identical source, media, known-place, Apple, Geoapify and composed current-location evidence.\n", plan.retainedPhotoUpdateOutcomeRows)
	fmt.Printf("Predecessor model schema fingerprint: %x.\n", plan.predecessorModelSchemaFingerprint)
	fmt.Println("Planned DDL: create empty typed photo_text_extraction; rebuild photo_card_generation without failure_text; replace model operation and attempt tables with the three-phase OCR-first names and nullable token counts. No provider or model call runs.")
}

func createAndValidateSQLiteBackup(ctx context.Context, archivePath, backupPath string, plan *schemaReprojectionPlan) error {
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
	if err := validateSQLiteIntegrity(ctx, backupStore.DB(), "schema reprojection backup"); err != nil {
		return err
	}
	backupPlan, err := buildSchemaReprojectionPlan(ctx, backupStore.DB())
	if err != nil {
		return err
	}
	if plan.invalidateObsoleteSemanticCards {
		if !backupPlan.invalidateObsoleteSemanticCards || len(backupPlan.obsoleteSemanticCardRows) != len(plan.obsoleteSemanticCardRows) || backupPlan.obsoleteSemanticCardRowsDigest != plan.obsoleteSemanticCardRowsDigest {
			return errors.New("schema reprojection backup differs from the obsolete semantic-card contract plan")
		}
	}
	if backupPlan.predecessorModelSchemaFingerprint != plan.predecessorModelSchemaFingerprint || backupPlan.photoTextExtractionRows != plan.photoTextExtractionRows || backupPlan.photoCardGenerationRows != plan.photoCardGenerationRows || len(backupPlan.photoCardGenerationOperationRows) != len(plan.photoCardGenerationOperationRows) || len(backupPlan.photoCardGenerationAttemptRows) != len(plan.photoCardGenerationAttemptRows) || backupPlan.currentPhotoCardRows != plan.currentPhotoCardRows || backupPlan.photoCardObservationSearchRows != plan.photoCardObservationSearchRows || backupPlan.cardStoredUpdateOutcomeRows != plan.cardStoredUpdateOutcomeRows {
		return errors.New("schema reprojection backup differs from the validated source")
	}
	for tableName, expectedFingerprint := range plan.preservedTableFingerprints {
		if backupPlan.preservedTableFingerprints[tableName] != expectedFingerprint {
			return fmt.Errorf("schema reprojection backup changed preserved table %s", tableName)
		}
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
		if plan.invalidateObsoleteSemanticCards {
			for _, row := range plan.obsoleteSemanticCardRows {
				if row.hasMatchingCurrentCard {
					for _, deletion := range []struct {
						query        string
						arguments    []any
						expectedRows bool
						description  string
					}{
						{`delete from observation_fts where id=?`, []any{"photo-card:" + row.assetID}, row.hasMatchingSearchRow, "card search row"},
						{`delete from photo_update_asset_outcome where asset_id=? and outcome_kind='card_stored'`, []any{row.assetID}, row.hasMatchingStoredOutcome, "card-stored outcome"},
						{`delete from current_photo_card where asset_id=? and input_sha256=?`, []any{row.assetID, row.inputSHA256}, true, "current card"},
					} {
						result, err := transaction.ExecContext(ctx, deletion.query, deletion.arguments...)
						if err != nil {
							return err
						}
						changed, err := result.RowsAffected()
						if err != nil {
							return err
						}
						expected := int64(0)
						if deletion.expectedRows {
							expected = 1
						}
						if changed != expected {
							return fmt.Errorf("delete obsolete %s changed %d rows, expected %d", deletion.description, changed, expected)
						}
					}
				}
				result, err := transaction.ExecContext(ctx, `delete from photo_card_generation where asset_id=? and input_sha256=? and substr(request_text, 1, length(?))=?`, row.assetID, row.inputSHA256, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix)
				if err != nil {
					return err
				}
				changed, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if changed != 1 {
					return fmt.Errorf("delete obsolete semantic-card generation changed %d rows, expected 1", changed)
				}
			}
			return nil
		}
		if plan.addRejectedResponseColumns {
			for _, statement := range []string{
				`alter table photo_text_extraction rename to photo_text_extraction_predecessor`,
				currentPhotoTextExtractionTableDDL,
				`drop table photo_text_extraction_predecessor`,
				`alter table photo_card_generation rename to photo_card_generation_predecessor`,
				currentPhotoCardGenerationTableDDL,
				`drop table photo_card_generation_predecessor`,
				`delete from observation_fts where id like 'photo-card:%'`,
				`delete from current_photo_card`,
				`delete from photo_update_asset_outcome where outcome_kind='card_stored'`,
			} {
				if _, err := transaction.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
			return nil
		}
		statements := []string{
			`alter table photo_card_generation rename to photo_card_generation_predecessor`,
			currentPhotoCardGenerationTableDDL,
			`drop table photo_card_generation_predecessor`,
			currentPhotoTextExtractionTableDDL,
			`alter table photo_card_generation_operation rename to photo_card_generation_operation_predecessor`,
			currentPhotoModelGenerationOperationTableDDL,
			`alter table photo_card_generation_transmission_attempt rename to photo_card_generation_transmission_attempt_predecessor`,
			currentPhotoModelGenerationTransmissionAttemptTableDDL,
		}
		for _, statement := range statements {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		operationInsert, err := transaction.PrepareContext(ctx, `insert into photo_model_generation_operation(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at) values (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, row := range plan.photoCardGenerationOperationRows {
			if _, err := operationInsert.ExecContext(ctx, row.assetID, row.inputSHA256, row.operationPhase, row.operationState, row.threadIdentifier, row.turnIdentifier, row.failureDetail, row.changedAt); err != nil {
				_ = operationInsert.Close()
				return err
			}
		}
		if err := operationInsert.Close(); err != nil {
			return err
		}
		attemptInsert, err := transaction.PrepareContext(ctx, `insert into photo_model_generation_transmission_attempt(attempt_id, asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, transmission_started_at, completed_at) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, row := range plan.photoCardGenerationAttemptRows {
			if _, err := attemptInsert.ExecContext(ctx, row.attemptID, row.assetID, row.inputSHA256, row.operationPhase, row.operationState, row.threadIdentifier, row.turnIdentifier, row.failureDetail, row.transmissionStartedAt, row.completedAt); err != nil {
				_ = attemptInsert.Close()
				return err
			}
		}
		if err := attemptInsert.Close(); err != nil {
			return err
		}
		for _, statement := range []string{
			`drop table photo_card_generation_operation_predecessor`,
			`drop table photo_card_generation_transmission_attempt_predecessor`,
			`delete from observation_fts where id like 'photo-card:%'`,
			`delete from current_photo_card`,
			`delete from photo_update_asset_outcome where outcome_kind='card_stored'`,
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
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
	if err := validateSQLiteIntegrity(ctx, openedStore.DB(), "reprojected archive"); err != nil {
		return err
	}
	if err := validateCurrentPhotoModelSchema(ctx, openedStore.DB()); err != nil {
		return err
	}
	if plan.invalidateObsoleteSemanticCards {
		for tableName, expectedFingerprint := range plan.preservedTableFingerprints {
			observedFingerprint, err := fingerprintTable(ctx, openedStore.DB(), tableName)
			if err != nil {
				return err
			}
			if observedFingerprint != expectedFingerprint {
				return fmt.Errorf("preserved table %s changed during semantic-card contract reprojection", tableName)
			}
		}
		var obsoleteSemanticRows int
		if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from photo_card_generation where substr(request_text, 1, length(?))=?`, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix).Scan(&obsoleteSemanticRows); err != nil {
			return err
		}
		if obsoleteSemanticRows != 0 {
			return errors.New("obsolete semantic-card generations remain after contract reprojection")
		}
		matchingCurrentCardAssetIDs := make([]string, 0, len(plan.obsoleteSemanticCardRows))
		for _, row := range plan.obsoleteSemanticCardRows {
			if row.hasMatchingCurrentCard {
				matchingCurrentCardAssetIDs = append(matchingCurrentCardAssetIDs, row.assetID)
			}
			var matchingRows int
			if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from current_photo_card where asset_id=? and input_sha256=?`, row.assetID, row.inputSHA256).Scan(&matchingRows); err != nil {
				return err
			}
			if matchingRows != 0 {
				return errors.New("obsolete current card remains after semantic-card contract reprojection")
			}
		}
		observedRetainedGenerationFingerprint, err := fingerprintQuery(ctx, openedStore.DB(), `select * from photo_card_generation where not (substr(request_text, 1, length(?))=?) order by rowid`, obsoleteSemanticCardPromptPrefix, obsoleteSemanticCardPromptPrefix)
		if err != nil {
			return err
		}
		if observedRetainedGenerationFingerprint != plan.retainedProjectionFingerprints["photo_card_generation"] {
			return errors.New("unrelated semantic-card generations changed during contract reprojection")
		}
		for tableName, projection := range map[string]struct {
			query          string
			excludedColumn string
			excludedValues []string
		}{
			"current_photo_card":         {`select * from current_photo_card`, "asset_id", matchingCurrentCardAssetIDs},
			"observation_fts":            {`select * from observation_fts`, "id", prefixedPhotoCardIdentifiers(matchingCurrentCardAssetIDs)},
			"photo_update_asset_outcome": {`select * from photo_update_asset_outcome`, "asset_id", matchingCurrentCardAssetIDs},
		} {
			query, arguments := excludingColumnValuesQuery(projection.query, projection.excludedColumn, projection.excludedValues)
			observedFingerprint, err := fingerprintQuery(ctx, openedStore.DB(), query+` order by rowid`, arguments...)
			if err != nil {
				return err
			}
			if observedFingerprint != plan.retainedProjectionFingerprints[tableName] {
				return fmt.Errorf("unrelated %s rows changed during semantic-card contract reprojection", tableName)
			}
		}
		observedSchemaFingerprint, err := photoModelSchemaFingerprint(ctx, openedStore.DB(), []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
		if err != nil {
			return err
		}
		if observedSchemaFingerprint != plan.currentModelSchemaFingerprint {
			return errors.New("model schema changed during semantic-card contract reprojection")
		}
		fmt.Printf("Current model schema fingerprint: %x.\n", observedSchemaFingerprint)
		return nil
	}
	if plan.addRejectedResponseColumns {
		for tableName, expectedFingerprint := range plan.preservedTableFingerprints {
			observedFingerprint, err := fingerprintTable(ctx, openedStore.DB(), tableName)
			if err != nil {
				return err
			}
			if observedFingerprint != expectedFingerprint {
				return fmt.Errorf("preserved table %s changed during rejected-response schema reprojection", tableName)
			}
		}
		for _, query := range []string{
			`select count(*) from photo_text_extraction`,
			`select count(*) from photo_card_generation`,
			`select count(*) from current_photo_card`,
			`select count(*) from observation_fts where id like 'photo-card:%'`,
			`select count(*) from photo_update_asset_outcome where outcome_kind='card_stored'`,
		} {
			var rows int
			if err := openedStore.DB().QueryRowContext(ctx, query).Scan(&rows); err != nil {
				return err
			}
			if rows != 0 {
				return errors.New("obsolete model product remains after rejected-response schema reprojection")
			}
		}
		var retainedUpdateOutcomes int
		if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from photo_update_asset_outcome`).Scan(&retainedUpdateOutcomes); err != nil || retainedUpdateOutcomes != plan.retainedPhotoUpdateOutcomeRows {
			return errors.New("non-card photo update outcomes changed during rejected-response schema reprojection")
		}
		retainedUpdateOutcomeDigest, err := fingerprintQuery(ctx, openedStore.DB(), `select * from photo_update_asset_outcome where outcome_kind<>'card_stored' order by asset_id`)
		if err != nil {
			return err
		}
		if retainedUpdateOutcomeDigest != plan.retainedPhotoUpdateOutcomeDigest {
			return errors.New("non-card photo update outcomes changed during rejected-response schema reprojection")
		}
		retainedObservationSearchDigest, err := fingerprintQuery(ctx, openedStore.DB(), `select * from observation_fts where id not like 'photo-card:%' order by rowid`)
		if err != nil {
			return err
		}
		if retainedObservationSearchDigest != plan.retainedObservationSearchDigest {
			return errors.New("non-card search observations changed during rejected-response schema reprojection")
		}
		plan.currentModelSchemaFingerprint, err = photoModelSchemaFingerprint(ctx, openedStore.DB(), []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
		if err != nil {
			return err
		}
		expectedFingerprint, err := expectedCurrentPhotoModelSchemaFingerprint(ctx)
		if err != nil {
			return err
		}
		if plan.currentModelSchemaFingerprint != expectedFingerprint {
			return errors.New("reprojected photo model tables do not match the exact live schema")
		}
		fmt.Printf("Current model schema fingerprint: %x.\n", plan.currentModelSchemaFingerprint)
		return nil
	}
	if err := validateCurrentReprojectionResultIsEmpty(ctx, openedStore.DB()); err != nil {
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
	if err := compareRetainedModelHistory(ctx, openedStore.DB(), plan); err != nil {
		return err
	}
	var retainedUpdateOutcomes int
	if err := openedStore.DB().QueryRowContext(ctx, `select count(*) from photo_update_asset_outcome`).Scan(&retainedUpdateOutcomes); err != nil || retainedUpdateOutcomes != plan.retainedPhotoUpdateOutcomeRows {
		return errors.New("non-card photo update outcomes changed during schema reprojection")
	}
	retainedUpdateOutcomeDigest, err := fingerprintQuery(ctx, openedStore.DB(), `select * from photo_update_asset_outcome where outcome_kind<>'card_stored' order by asset_id`)
	if err != nil {
		return err
	}
	if retainedUpdateOutcomeDigest != plan.retainedPhotoUpdateOutcomeDigest {
		return errors.New("non-card photo update outcomes changed during schema reprojection")
	}
	retainedObservationSearchDigest, err := fingerprintQuery(ctx, openedStore.DB(), `select * from observation_fts where id not like 'photo-card:%' order by rowid`)
	if err != nil {
		return err
	}
	if retainedObservationSearchDigest != plan.retainedObservationSearchDigest {
		return errors.New("non-card search observations changed during schema reprojection")
	}
	plan.currentModelSchemaFingerprint, err = photoModelSchemaFingerprint(ctx, openedStore.DB(), []string{"photo_text_extraction", "photo_card_generation", "photo_model_generation_operation", "photo_model_generation_transmission_attempt"})
	if err != nil {
		return err
	}
	expectedFingerprint, err := expectedCurrentPhotoModelSchemaFingerprint(ctx)
	if err != nil {
		return err
	}
	if plan.currentModelSchemaFingerprint != expectedFingerprint {
		return errors.New("reprojected photo model tables do not match the exact live schema")
	}
	fmt.Printf("Current model schema fingerprint: %x.\n", plan.currentModelSchemaFingerprint)
	return nil
}

func compareRetainedModelHistory(ctx context.Context, database *sql.DB, plan *schemaReprojectionPlan) error {
	operationFingerprint, err := fingerprintQuery(ctx, database, `select asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at from photo_model_generation_operation order by asset_id, input_sha256, operation_phase`)
	if err != nil {
		return err
	}
	expectedOperationFingerprint := sha256.New()
	for _, row := range plan.photoCardGenerationOperationRows {
		for _, value := range []any{row.assetID, row.inputSHA256, int64(row.operationPhase), int64(row.operationState), row.threadIdentifier, row.turnIdentifier, row.failureDetail, row.changedAt} {
			if err := writeFingerprintValue(expectedOperationFingerprint, value); err != nil {
				return err
			}
		}
	}
	if !bytes.Equal(operationFingerprint[:], expectedOperationFingerprint.Sum(nil)) {
		return errors.New("retained model operations changed during schema reprojection")
	}
	attemptFingerprint, err := fingerprintQuery(ctx, database, `select attempt_id, asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, input_tokens, cached_input_tokens, output_tokens, reasoning_output_tokens, total_tokens, transmission_started_at, completed_at from photo_model_generation_transmission_attempt order by attempt_id`)
	if err != nil {
		return err
	}
	expectedAttemptFingerprint := sha256.New()
	for _, row := range plan.photoCardGenerationAttemptRows {
		for _, value := range []any{row.attemptID, row.assetID, row.inputSHA256, int64(row.operationPhase), int64(row.operationState), row.threadIdentifier, row.turnIdentifier, row.failureDetail, nil, nil, nil, nil, nil, row.transmissionStartedAt, nullableStringValue(row.completedAt)} {
			if err := writeFingerprintValue(expectedAttemptFingerprint, value); err != nil {
				return err
			}
		}
	}
	if !bytes.Equal(attemptFingerprint[:], expectedAttemptFingerprint.Sum(nil)) {
		return errors.New("retained model transmission attempts changed during schema reprojection")
	}
	return nil
}

func nullableStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func validateSQLiteIntegrity(ctx context.Context, database *sql.DB, label string) error {
	var integrity string
	if err := database.QueryRowContext(ctx, `pragma integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("%s failed integrity check", label)
	}
	return nil
}
