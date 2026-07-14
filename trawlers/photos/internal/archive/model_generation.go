package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

var (
	errModelGenerationUncertain        = errors.New("model generation transmission may have started; automatic retry is stopped")
	errModelGenerationStoppedUncertain = errors.New("model generation is stopped because transmission is uncertain")
)

type modelGenerationFaultStage string

const (
	modelGenerationFaultBeforeSend  modelGenerationFaultStage = "before_send"
	modelGenerationFaultAfterSend   modelGenerationFaultStage = "after_send"
	modelGenerationFaultAfterRetain modelGenerationFaultStage = "after_retain"
)

// injectModelGenerationFault is a narrow crash seam for the restart tests.
// Production leaves it nil.
var injectModelGenerationFault func(modelGenerationFaultStage, model.RawResult) error

func modelGenerationFault(stage modelGenerationFaultStage, raw model.RawResult) error {
	if injectModelGenerationFault == nil {
		return nil
	}
	return injectModelGenerationFault(stage, raw)
}

type modelGenerationDecision struct {
	GenerationID string
	Call         model.Call
	Fresh        bool
}

func prepareModelGeneration(
	ctx context.Context,
	db *store.Store,
	assetID, promptVersion, parserVersion string,
	request model.ProviderRequest,
	now time.Time,
) (modelGenerationDecision, error) {
	var decision modelGenerationDecision
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		decision, err = prepareModelGenerationTx(ctx, tx, assetID, promptVersion, parserVersion, request, now)
		return err
	})
	return decision, err
}

func prepareModelGenerationForPreparedCard(
	ctx context.Context,
	db *store.Store,
	executionID, assetID string,
	prepared preparedCardRequest,
	now time.Time,
) (modelGenerationDecision, error) {
	var decision modelGenerationDecision
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		decision, err = prepareModelGenerationTx(ctx, tx, assetID, prepared.PromptVersion, prepared.ParserVersion, prepared.Request, now)
		if err != nil {
			return err
		}
		return retainPreparedCardRequest(ctx, tx, executionID, assetID, decision.GenerationID, prepared)
	})
	return decision, err
}

func retainPreparedModelBoundaryTx(ctx context.Context, tx *sql.Tx, executionID, assetID string, prepared preparedCardRequest, now time.Time) (string, error) {
	digest := prepared.Request.Digest()
	digestText := hex.EncodeToString(digest[:])
	generationID := "model_generation:" + digestText[:32]
	if _, err := tx.ExecContext(ctx, `
insert into model_generation(id, request_sha256, request_route, model_id, request_body, created_at)
values (?, ?, ?, ?, ?, ?)
on conflict(id) do nothing
`, generationID, digestText, prepared.Request.Route(), prepared.Request.Model(), prepared.Request.Body(), now.UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("persist prepared model request: %w", err)
	}
	storedRequest, storedDigest, err := readModelGenerationRequest(ctx, tx, generationID)
	if err != nil {
		return "", err
	}
	if storedDigest != digestText || storedRequest.Route() != prepared.Request.Route() || storedRequest.Model() != prepared.Request.Model() || !bytes.Equal(storedRequest.Body(), prepared.Request.Body()) {
		return "", fmt.Errorf("%w: persisted provider request", errPreparedCardMismatch)
	}
	if _, err := tx.ExecContext(ctx, `
insert into model_generation_asset(generation_id, asset_id, prompt_version, parser_version)
values (?, ?, ?, ?)
on conflict(generation_id, asset_id) do nothing
`, generationID, assetID, prepared.PromptVersion, prepared.ParserVersion); err != nil {
		return "", fmt.Errorf("persist prepared model asset: %w", err)
	}
	if err := retainPreparedCardRequest(ctx, tx, executionID, assetID, generationID, prepared); err != nil {
		return "", err
	}
	return generationID, nil
}

func prepareModelGenerationTx(
	ctx context.Context,
	tx *sql.Tx,
	assetID, promptVersion, parserVersion string,
	request model.ProviderRequest,
	now time.Time,
) (modelGenerationDecision, error) {
	digest := request.Digest()
	digestText := hex.EncodeToString(digest[:])
	generationID := "model_generation:" + digestText[:32]
	timestamp := now.UTC().Format(time.RFC3339Nano)
	decision := modelGenerationDecision{GenerationID: generationID}
	if _, err := tx.ExecContext(ctx, `
insert into model_generation(id, request_sha256, request_route, model_id, request_body, created_at)
values (?, ?, ?, ?, ?, ?)
on conflict(id) do nothing
`, generationID, digestText, request.Route(), request.Model(), request.Body(), timestamp); err != nil {
		return decision, fmt.Errorf("persist model generation request: %w", err)
	}
	storedRequest, storedDigest, err := readModelGenerationRequest(ctx, tx, generationID)
	if err != nil {
		return decision, err
	}
	if storedDigest != digestText || storedRequest.Route() != request.Route() ||
		storedRequest.Model() != request.Model() || !bytes.Equal(storedRequest.Body(), request.Body()) {
		return decision, errors.New("model generation identity does not match its persisted request")
	}
	decision.Call.Request = storedRequest

	if _, err := tx.ExecContext(ctx, `
insert into model_generation_asset(generation_id, asset_id, prompt_version, parser_version)
values (?, ?, ?, ?)
on conflict(generation_id, asset_id) do nothing
`, generationID, assetID, promptVersion, parserVersion); err != nil {
		return decision, fmt.Errorf("persist model generation asset relation: %w", err)
	}
	var storedPromptVersion, storedParserVersion string
	var completedAt sql.NullString
	if err := tx.QueryRowContext(ctx, `
select prompt_version, parser_version, completed_at
from model_generation_asset
where generation_id = ? and asset_id = ?
`, generationID, assetID).Scan(&storedPromptVersion, &storedParserVersion, &completedAt); err != nil {
		return decision, fmt.Errorf("read model generation asset relation: %w", err)
	}
	if storedPromptVersion != promptVersion || storedParserVersion != parserVersion {
		return decision, errors.New("model generation versions do not match their persisted asset relation")
	}
	if completedAt.Valid {
		decision.Call.Reused = true
		return decision, nil
	}

	result, err := tx.ExecContext(ctx, `
insert into model_generation_attempt(generation_id, started_at)
values (?, ?)
on conflict(generation_id) do nothing
`, generationID, timestamp)
	if err != nil {
		return decision, fmt.Errorf("persist model generation attempt: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return decision, fmt.Errorf("read model generation attempt claim: %w", err)
	}
	if inserted == 1 {
		decision.Fresh = true
		return decision, nil
	}

	raw, retained, err := readModelGenerationAttempt(ctx, tx, generationID)
	if err != nil {
		return decision, err
	}
	if retained {
		decision.Call.Retained = &raw
		return decision, nil
	}
	return decision, errModelGenerationUncertain
}

func readModelGenerationRequest(ctx context.Context, tx *sql.Tx, generationID string) (model.ProviderRequest, string, error) {
	var digest, route, modelID string
	var body []byte
	if err := tx.QueryRowContext(ctx, `
select request_sha256, request_route, model_id, request_body
from model_generation
where id = ?
`, generationID).Scan(&digest, &route, &modelID, &body); err != nil {
		return model.ProviderRequest{}, "", fmt.Errorf("read model generation request: %w", err)
	}
	request, err := model.RestoreProviderRequest(route, modelID, body)
	if err != nil {
		return model.ProviderRequest{}, "", fmt.Errorf("restore model generation request: %w", err)
	}
	return request, digest, nil
}

func readModelGenerationAttempt(ctx context.Context, tx *sql.Tx, generationID string) (model.RawResult, bool, error) {
	var raw model.RawResult
	var started, retained int
	err := tx.QueryRowContext(ctx, `
select response_body, failure_body, http_status, http_status_text,
       provider_request_id, transmission_started, retained_at is not null
from model_generation_attempt
where generation_id = ?
`, generationID).Scan(
		&raw.Response,
		&raw.Failure,
		&raw.StatusCode,
		&raw.Status,
		&raw.ProviderRequestID,
		&started,
		&retained,
	)
	if err != nil {
		return model.RawResult{}, false, fmt.Errorf("read model generation attempt: %w", err)
	}
	raw.TransmissionStarted = started != 0
	return raw, retained != 0, nil
}

func retainModelGenerationResult(ctx context.Context, db *store.Store, generationID string, raw model.RawResult, now time.Time) error {
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update model_generation_attempt
set response_body = ?, failure_body = ?, http_status = ?, http_status_text = ?,
    provider_request_id = ?, transmission_started = ?, retained_at = ?
where generation_id = ? and retained_at is null
`, raw.Response, raw.Failure, raw.StatusCode, raw.Status, raw.ProviderRequestID,
			boolInt(raw.TransmissionStarted), now.UTC().Format(time.RFC3339Nano), generationID)
		if err != nil {
			return fmt.Errorf("retain model generation result: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read retained model generation result count: %w", err)
		}
		if updated == 1 {
			return nil
		}
		stored, retained, err := readModelGenerationAttempt(ctx, tx, generationID)
		if err != nil {
			return err
		}
		if retained && rawResultsEqual(stored, raw) {
			return nil
		}
		return errors.New("model generation result was already retained with different bytes")
	})
}

func markModelGenerationTransmissionStarted(ctx context.Context, tx *sql.Tx, generationID string) error {
	if !strings.HasPrefix(generationID, "model_generation:") {
		return errors.New("model generation id is required")
	}
	if _, err := tx.ExecContext(ctx, `
update model_generation_attempt
set transmission_started = 1
where generation_id = ? and retained_at is null
`, generationID); err != nil {
		return fmt.Errorf("mark model generation transmission started: %w", err)
	}
	return nil
}

func stopModelGenerationUncertain(ctx context.Context, tx *sql.Tx, queueID, generationID string, now time.Time) (bool, error) {
	if strings.TrimSpace(queueID) == "" || strings.TrimSpace(generationID) == "" {
		return false, errors.New("model generation queue and id are required")
	}
	result, err := tx.ExecContext(ctx, `
update classification_queue
set state = 'content_failed',
    reason = 'stopped_uncertain: model attempt has no retained result',
    updated_at = ?
where id = ?
  and exists (
    select 1 from model_generation_attempt
    where generation_id = ? and retained_at is null
  )
  and exists (
    select 1 from model_generation_asset
    where generation_id = ? and completed_at is null
  )
`, now.UTC().Format(time.RFC3339Nano), queueID, generationID, generationID)
	if err != nil {
		return false, fmt.Errorf("stop uncertain model generation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read stopped uncertain model generation count: %w", err)
	}
	return updated == 1, nil
}

func recordModelGenerationParseFailure(ctx context.Context, tx *sql.Tx, generationID, assetID string, err error, now time.Time) error {
	result, updateErr := tx.ExecContext(ctx, `
update model_generation_asset
set parse_failure = ?, parse_failed_at = ?
where generation_id = ? and asset_id = ? and completed_at is null
`, []byte(err.Error()), now.UTC().Format(time.RFC3339Nano), generationID, assetID)
	if updateErr != nil {
		return fmt.Errorf("retain model generation parse failure: %w", updateErr)
	}
	updated, updateErr := result.RowsAffected()
	if updateErr != nil {
		return fmt.Errorf("read model generation parse failure count: %w", updateErr)
	}
	if updated != 1 {
		return errors.New("model generation asset relation is missing for parse failure")
	}
	return nil
}

func parseRetainedModelGeneration(
	ctx context.Context,
	db *store.Store,
	generationID, assetID string,
	classifier modelClassifier,
	prepared preparedCardRequest,
	raw model.RawResult,
	now time.Time,
) (modelResult, error) {
	response, err := model.Parse(prepared.Request, raw)
	if err != nil {
		err = fmt.Errorf("%w: provider envelope: %v", errModelCardParse, err)
	} else {
		var parsed modelResult
		parsed, err = classifier.parseResult(response.Text, prepared)
		if err == nil {
			return parsed, nil
		}
	}
	if persistErr := db.WithTx(ctx, func(tx *sql.Tx) error {
		return recordModelGenerationParseFailure(ctx, tx, generationID, assetID, err, now)
	}); persistErr != nil {
		return modelResult{}, persistErr
	}
	return modelResult{}, err
}

func completeModelGeneration(ctx context.Context, tx *sql.Tx, generationID, assetID string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `
update model_generation_asset
set completed_at = ?, parse_failure = null, parse_failed_at = null
where generation_id = ? and asset_id = ? and completed_at is null
`, now.UTC().Format(time.RFC3339Nano), generationID, assetID)
	if err != nil {
		return fmt.Errorf("complete model generation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed model generation count: %w", err)
	}
	if updated != 1 {
		var completed int
		if err := tx.QueryRowContext(ctx, `
select completed_at is not null
from model_generation_asset
where generation_id = ? and asset_id = ?
`, generationID, assetID).Scan(&completed); err != nil {
			return errors.New("model generation asset relation is missing")
		}
		if completed == 0 {
			return errors.New("model generation asset relation was not completed")
		}
	}
	return nil
}

func rawResultsEqual(left, right model.RawResult) bool {
	return bytes.Equal(left.Response, right.Response) &&
		bytes.Equal(left.Failure, right.Failure) &&
		left.Status == right.Status &&
		left.StatusCode == right.StatusCode &&
		left.ProviderRequestID == right.ProviderRequestID &&
		left.TransmissionStarted == right.TransmissionStarted
}
