package archive

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestMigrationPaidCallLedgerPreservesVersion12GenerationRows(t *testing.T) {
	ctx := context.Background()
	path, before := paidCallVersion12Fixture(t)
	logPaidCallBoundary(t, "paid_call_migration_input", before)

	upgraded, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if version, err := upgraded.SchemaVersion(ctx); err != nil || version != SchemaVersion {
		t.Fatalf("upgraded schema version = %d, %v", version, err)
	}
	after := readPaidCallMigrationRows(t, upgraded)
	assertPaidCallMigrationRowsEqual(t, before, after)
	assertEmptyPaidCallLedger(t, upgraded)
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	reopenedRows := readPaidCallMigrationRows(t, reopened)
	assertPaidCallMigrationRowsEqual(t, before, reopenedRows)
	assertEmptyPaidCallLedger(t, reopened)
	logPaidCallBoundary(t, "paid_call_migration_output", map[string]any{
		"schema_version": 14,
		"rows":           reopenedRows,
		"ledger_rows":    0,
	})
}

func TestMigrationPaidCallLedgerResumesAfterInjectedStop(t *testing.T) {
	ctx := context.Background()
	path, before := paidCallVersion12Fixture(t)
	db, err := store.Open(ctx, store.Options{Path: path, SchemaVersion: 12})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("synthetic stop after first paid-call table")
	stopErr := func() error {
		if _, err := db.DB().ExecContext(ctx, paidCallSchemaBlock(t, "paid_call_stage")); err != nil {
			return err
		}
		return injected
	}()
	if !errors.Is(stopErr, injected) {
		t.Fatalf("injected migration stop = %v", stopErr)
	}
	afterStop := readPaidCallMigrationRows(t, db)
	assertPaidCallMigrationRowsEqual(t, before, afterStop)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	afterUpgrade := readPaidCallMigrationRows(t, upgraded)
	assertPaidCallMigrationRowsEqual(t, before, afterUpgrade)
	assertEmptyPaidCallLedger(t, upgraded)
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openArchive(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	afterReopen := readPaidCallMigrationRows(t, reopened)
	assertPaidCallMigrationRowsEqual(t, before, afterReopen)
	assertEmptyPaidCallLedger(t, reopened)
	logPaidCallBoundary(t, "paid_call_migration_restart_output", map[string]any{
		"injected_error": injected.Error(),
		"rows":           afterReopen,
		"ledger_rows":    0,
	})
}

type paidCallMigrationRows struct {
	GenerationID           string
	RequestSHA256          string
	RequestRoute           string
	ModelID                string
	RequestBody            []byte
	GenerationAt           string
	AssetID                string
	PromptVersion          string
	ParserVersion          string
	CompletedAt            string
	ParseFailure           []byte
	ParseFailedAt          string
	AttemptStartedAt       string
	AttemptResponse        []byte
	AttemptFailure         []byte
	HTTPStatus             int
	HTTPStatusText         string
	ProviderRequest        string
	Transmission           int
	RetainedAt             string
	ObservationID          string
	ObservationAsset       string
	ObservationType        string
	ObservationValue       string
	ObservationJSON        string
	Confidence             float64
	ObservationSource      string
	ObservationModel       string
	ObservationPrompt      string
	ObservationGeneration  string
	ObservationEvidence    string
	ObservationStale       string
	ObservationStaleReason string
	ObservationSuperseded  string
}

func paidCallVersion12Fixture(t *testing.T) (string, paidCallMigrationRows) {
	t.Helper()
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        paidCallVersion12Schema(t),
		SchemaVersion: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:paid-call-migration', '/tmp/Synthetic.photoslibrary', 'sqlite:crawl_snapshot/synthetic', '2026-07-11T08:00:00Z', 'fixture', '{}');
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values ('asset:paid-call-migration', 'paid-call-migration', 'image', '0', '2026-07-11T07:00:00Z', '2026-07-11T07:00:00Z',
  '2026-07-11T07:00:00Z', 'UTC', 2, 2, 0, 0, 0, '', 0, '', '', '', 'source:paid-call-migration', '{}');
insert into model_generation(id, request_sha256, request_route, model_id, request_body, created_at)
values ('model_generation:paid-call-migration', 'fixture-request-sha', 'https://models.example.com/api/generate', 'fixture-model', X'000102ff', '2026-07-11T08:01:00Z');
insert into model_generation_asset(generation_id, asset_id, prompt_version, parser_version, completed_at)
values ('model_generation:paid-call-migration', 'asset:paid-call-migration', 'fixture-prompt-v1', 'fixture-parser-v1', '2026-07-11T08:03:00Z');
insert into model_generation_attempt(generation_id, started_at, response_body, failure_body, http_status, http_status_text, provider_request_id, transmission_started, retained_at)
values ('model_generation:paid-call-migration', '2026-07-11T08:02:00Z', X'102030', X'', 200, '200 OK', 'fixture-request-id', 1, '2026-07-11T08:02:30Z');
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, generation_id, evidence_id)
values ('observation:paid-call-migration', 'asset:paid-call-migration', 'card_summary', 'Synthetic migration card.', '{}', 1, 'photo_card', 'fixture-model', 'fixture-prompt-v1', 'model_generation:paid-call-migration', '');
`); err != nil {
		t.Fatal(err)
	}
	rows := readPaidCallMigrationRows(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return paths.Database, rows
}

func readPaidCallMigrationRows(t *testing.T, db *store.Store) paidCallMigrationRows {
	t.Helper()
	ctx := context.Background()
	var row paidCallMigrationRows
	if err := db.DB().QueryRowContext(ctx, `
select g.id, g.request_sha256, g.request_route, g.model_id, g.request_body, g.created_at,
       r.asset_id, r.prompt_version, r.parser_version, coalesce(r.completed_at, ''),
       coalesce(r.parse_failure, X''), coalesce(r.parse_failed_at, ''),
       a.started_at, a.response_body, a.failure_body, a.http_status, a.http_status_text,
       a.provider_request_id, a.transmission_started, coalesce(a.retained_at, ''),
       o.id, o.asset_id, o.observation_type, o.value_text, o.value_json, o.confidence,
       o.source, o.model_id, o.prompt_version, o.generation_id, o.evidence_id,
       coalesce(o.stale_since, ''), coalesce(o.stale_reason, ''), coalesce(o.superseded_at, '')
from model_generation g
join model_generation_asset r on r.generation_id = g.id
join model_generation_attempt a on a.generation_id = g.id
join model_observation o on o.generation_id = g.id
where g.id = 'model_generation:paid-call-migration'
`).Scan(&row.GenerationID, &row.RequestSHA256, &row.RequestRoute, &row.ModelID, &row.RequestBody,
		&row.GenerationAt, &row.AssetID, &row.PromptVersion, &row.ParserVersion, &row.CompletedAt,
		&row.ParseFailure, &row.ParseFailedAt, &row.AttemptStartedAt, &row.AttemptResponse,
		&row.AttemptFailure, &row.HTTPStatus, &row.HTTPStatusText, &row.ProviderRequest,
		&row.Transmission, &row.RetainedAt, &row.ObservationID, &row.ObservationAsset,
		&row.ObservationType, &row.ObservationValue, &row.ObservationJSON, &row.Confidence,
		&row.ObservationSource, &row.ObservationModel, &row.ObservationPrompt,
		&row.ObservationGeneration, &row.ObservationEvidence, &row.ObservationStale,
		&row.ObservationStaleReason, &row.ObservationSuperseded); err != nil {
		t.Fatal(err)
	}
	return row
}

func assertPaidCallMigrationRowsEqual(t *testing.T, want, got paidCallMigrationRows) {
	t.Helper()
	if !reflect.DeepEqual(want, got) || !bytes.Equal(want.RequestBody, got.RequestBody) ||
		!bytes.Equal(want.AttemptResponse, got.AttemptResponse) || !bytes.Equal(want.AttemptFailure, got.AttemptFailure) {
		t.Fatalf("generation rows changed during migration:\nwant=%#v\ngot=%#v", want, got)
	}
}

func assertEmptyPaidCallLedger(t *testing.T, db *store.Store) {
	t.Helper()
	ctx := context.Background()
	for _, table := range []string{"paid_call_stage", "paid_call_stage_item", "paid_call_claim"} {
		var count int
		if err := db.DB().QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", table, count)
		}
	}
}

func paidCallVersion12Schema(t *testing.T) string {
	t.Helper()
	old := Schema
	for _, table := range []string{"paid_call_stage", "paid_call_stage_item", "paid_call_claim"} {
		old = strings.Replace(old, paidCallSchemaBlock(t, table), "", 1)
	}
	old = strings.Replace(old, "create index if not exists paid_call_stage_item_asset_idx on paid_call_stage_item(asset_id, stage_id);\n", "", 1)
	if old == Schema || strings.Contains(old, "paid_call_stage") {
		t.Fatal("version-12 schema fixture still contains paid-call ledger")
	}
	return old
}

func paidCallSchemaBlock(t *testing.T, table string) string {
	t.Helper()
	start := "create table if not exists " + table + " ("
	startIndex := strings.Index(Schema, start)
	if startIndex < 0 {
		t.Fatalf("missing schema block for %s", table)
	}
	remainder := Schema[startIndex:]
	endIndex := strings.Index(remainder, "\n);\n")
	if endIndex < 0 {
		t.Fatalf("unterminated schema block for %s", table)
	}
	return remainder[:endIndex+4]
}
