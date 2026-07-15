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

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type fixtureCardPreparation struct {
	Source       cardinput.SourceFacts
	Artifacts    cardinput.CheckedArtifacts
	Evidence     []place.EvidenceRecord
	Classify     classifyInput
	CurrentStill []byte
	MIMEType     string
}

type fixtureCardResult struct {
	ExecutionID   string
	Input         cardinput.Result
	Request       model.ProviderRequest
	RawResponse   model.RawResult
	PromptVersion string
	ParserVersion string
	Summary       string
	Description   string
	VisibleText   string
	Uncertainties []string
	Location      modelLocation
	Custody       *cardwire.CardExecutionCustody
	Reused        bool
}

func executeFixtureCard(ctx context.Context, db *store.Store, executionID string, prepare func() (fixtureCardPreparation, error), classifier modelClassifier, fixtureBytes []byte, now time.Time) (fixtureCardResult, error) {
	if strings.TrimSpace(executionID) == "" {
		return fixtureCardResult{}, errors.New("fixture card execution id is required")
	}
	if completed, found, err := readCompletedFixtureCard(ctx, db, executionID, classifier.client); err != nil || found {
		return completed, err
	}
	retained, generationID, found, err := restoreRetainedPreparedCardRequest(ctx, db, executionID, classifier.client)
	if err != nil {
		return fixtureCardResult{}, err
	}
	if found {
		decision, err := prepareModelGeneration(ctx, db, retained.Custody.GetAssetId(), retained.PromptVersion, retained.ParserVersion, retained.Request, now)
		if err != nil {
			return fixtureCardResult{}, err
		}
		if decision.GenerationID != generationID || decision.Call.Retained == nil {
			return fixtureCardResult{}, errors.New("retained fixture card request has no retained model result")
		}
		return finishRetainedFixtureCard(ctx, db, executionID, generationID, retained, *decision.Call.Retained, now)
	}
	prepared, err := prepare()
	if err != nil {
		return fixtureCardResult{}, err
	}
	if prepared.Source.AssetID != prepared.Classify.AssetID {
		return fixtureCardResult{}, errors.New("fixture card asset identities do not match")
	}
	if strings.TrimSpace(prepared.Source.SourceID) == "" || prepared.Source.SourceID != prepared.Classify.SourceLibraryID {
		return fixtureCardResult{}, errors.New("fixture card source identities do not match")
	}
	input, err := cardinput.Build(prepared.Source, prepared.Artifacts, prepared.Evidence)
	if err != nil {
		return fixtureCardResult{}, err
	}
	request, err := renderPreparedCardRequest(prepared.Source, prepared.Artifacts, prepared.Evidence, prepared.CurrentStill, classifier)
	if err != nil {
		return fixtureCardResult{}, err
	}
	if request.Input.ID != input.ID {
		return fixtureCardResult{}, errors.New("fixture card prepared request does not match CardInput")
	}
	if err := validatePlaceEvidenceIdentity(prepared.Evidence, prepared.Classify); err != nil {
		return fixtureCardResult{}, err
	}
	wantExecutionID := fixtureCardExecutionID(prepared.Source.AssetID, request)
	if executionID != wantExecutionID {
		return fixtureCardResult{}, errors.New("fixture card execution identity does not match prepared input and request")
	}
	var fixture cardwire.FixtureResponse
	if len(fixtureBytes) == 0 {
		return fixtureCardResult{}, errors.New("fixture response protobuf is required")
	}
	if err := proto.Unmarshal(fixtureBytes, &fixture); err != nil {
		return fixtureCardResult{}, fmt.Errorf("decode fixture response protobuf: %w", err)
	}
	decision, err := prepareModelGenerationForPreparedCard(ctx, db, executionID, prepared.Source.AssetID, request, now)
	if err != nil {
		return fixtureCardResult{}, err
	}
	if !decision.Fresh {
		return fixtureCardResult{}, errors.New("fixture card generation exists without a completed card execution")
	}
	raw := model.RawResult{Response: bytes.Clone(fixture.Response), Failure: bytes.Clone(fixture.Failure), Status: fixture.Status, StatusCode: int(fixture.StatusCode), ProviderRequestID: fixture.ProviderRequestId, TransmissionStarted: fixture.TransmissionStarted}
	if err := retainModelGenerationResult(ctx, db, decision.GenerationID, raw, now); err != nil {
		return fixtureCardResult{}, err
	}
	parsed, err := parseRetainedModelGeneration(ctx, db, decision.GenerationID, prepared.Source.AssetID, classifier, request, raw, now)
	if err != nil {
		return fixtureCardResult{}, err
	}
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := writeModelClassification(ctx, tx, prepared.Classify, classifier, parsed, request, now, decision.GenerationID); err != nil {
			return err
		}
		if err := completeModelGeneration(ctx, tx, decision.GenerationID, prepared.Source.AssetID, now); err != nil {
			return err
		}
		return completePreparedCardRequest(ctx, tx, executionID, now.UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return fixtureCardResult{}, err
	}
	return fixtureCardResult{ExecutionID: executionID, Input: request.Input, Request: request.Request, RawResponse: raw, PromptVersion: request.PromptVersion, ParserVersion: request.ParserVersion, Summary: parsed.Card.Summary, Description: parsed.Card.Description, VisibleText: parsed.Card.VisibleText, Uncertainties: parsed.Card.Uncertainties, Location: parsed.Card.Location, Custody: request.Custody}, nil
}

func finishRetainedFixtureCard(ctx context.Context, db *store.Store, executionID, generationID string, prepared preparedCardRequest, raw model.RawResult, now time.Time) (fixtureCardResult, error) {
	classifier := modelClassifier{modelID: prepared.Request.Model(), promptVersion: prepared.PromptVersion}
	parsed, err := parseRetainedModelGeneration(ctx, db, generationID, prepared.Custody.GetAssetId(), classifier, prepared, raw, now)
	if err != nil {
		return fixtureCardResult{}, err
	}
	queueID, err := approvedCardQueueID(ctx, db, prepared.Custody.GetAssetId())
	if err != nil {
		return fixtureCardResult{}, err
	}
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		input := classifyInput{AssetID: prepared.Custody.GetAssetId(), QueueID: queueID}
		if _, _, err := writeModelClassification(ctx, tx, input, classifier, parsed, prepared, now, generationID); err != nil {
			return err
		}
		if err := completeModelGeneration(ctx, tx, generationID, input.AssetID, now); err != nil {
			return err
		}
		return completePreparedCardRequest(ctx, tx, executionID, now.UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return fixtureCardResult{}, err
	}
	return fixtureCardResult{ExecutionID: executionID, Input: prepared.Input, Request: prepared.Request, RawResponse: raw, PromptVersion: prepared.PromptVersion, ParserVersion: prepared.ParserVersion, Summary: parsed.Card.Summary, Description: parsed.Card.Description, VisibleText: parsed.Card.VisibleText, Uncertainties: parsed.Card.Uncertainties, Location: parsed.Card.Location, Custody: prepared.Custody}, nil
}

// validatePlaceEvidenceIdentity keeps the checked records accepted by
// CardInput.Build and the already prepared request projection on one custody
// boundary. It deliberately compares identity only; place semantics stay in
// their existing canonical projections.
func validatePlaceEvidenceIdentity(records []place.EvidenceRecord, prompt classifyInput) error {
	identities := []string(nil)
	if prompt.Place != nil {
		identities = prompt.Place.EvidenceRawResponseSHA256
	}
	if len(identities) != len(records) {
		return errors.New("model input checked place evidence identities differ from CardInput")
	}
	for index, record := range records {
		if identities[index] != record.RawResponseSHA256 {
			return errors.New("model input checked place evidence identities differ from CardInput")
		}
	}
	return nil
}

func fixtureCardExecutionID(assetID string, request preparedCardRequest) string {
	requestDigest := request.Request.Digest()
	return stableID("card_execution", assetID, request.Input.ID, request.CustodySHA256, hex.EncodeToString(requestDigest[:]), request.CardRequestID, request.PromptVersion, request.ParserVersion)
}

func executionCustody(source cardinput.SourceFacts, artifacts cardinput.CheckedArtifacts, records []place.EvidenceRecord) *cardwire.CardExecutionCustody {
	custody := &cardwire.CardExecutionCustody{SourceId: source.SourceID, AssetId: source.AssetID, ImmutableOriginalResourceId: artifacts.ImmutableOriginal.ResourceID, MetadataRecordId: artifacts.Metadata.RecordID, MetadataProjectionId: artifacts.Metadata.ProjectionID, FullCurrentProofSha256: artifacts.FullCurrent.ProofSHA256}
	for _, record := range records {
		custody.Evidence = append(custody.Evidence, &cardwire.EvidenceLink{ProviderIdentity: record.ProviderIdentity, Operation: record.Operation, RawResponseSha256: record.RawResponseSHA256})
	}
	return custody
}

func readCompletedFixtureCard(ctx context.Context, db *store.Store, executionID string, client *model.Client) (fixtureCardResult, bool, error) {
	var result fixtureCardResult
	var inputBytes, custodyBytes, requestBody []byte
	var route, modelID string
	err := db.DB().QueryRowContext(ctx, `select c.id, c.card_input_id, c.card_input, c.custody, g.request_route, g.model_id, g.request_body, ga.prompt_version, ga.parser_version, a.response_body, a.failure_body, a.http_status_text, a.http_status, a.provider_request_id, a.transmission_started from card_execution c join model_generation g on g.id = c.generation_id join model_generation_asset ga on ga.generation_id = g.id and ga.asset_id = c.asset_id join model_generation_attempt a on a.generation_id = g.id where c.id = ? and c.completed_at <> ''`, executionID).Scan(&result.ExecutionID, &result.Input.ID, &inputBytes, &custodyBytes, &route, &modelID, &requestBody, &result.PromptVersion, &result.ParserVersion, &result.RawResponse.Response, &result.RawResponse.Failure, &result.RawResponse.Status, &result.RawResponse.StatusCode, &result.RawResponse.ProviderRequestID, &result.RawResponse.TransmissionStarted)
	if errors.Is(err, sql.ErrNoRows) {
		return fixtureCardResult{}, false, nil
	}
	if err != nil {
		return fixtureCardResult{}, false, fmt.Errorf("read completed card execution: %w", err)
	}
	result.Input.Bytes = inputBytes
	result.Input.Input = new(cardwire.CardInput)
	if err := proto.Unmarshal(inputBytes, result.Input.Input); err != nil {
		return fixtureCardResult{}, false, err
	}
	result.Custody = new(cardwire.CardExecutionCustody)
	if err := proto.Unmarshal(custodyBytes, result.Custody); err != nil {
		return fixtureCardResult{}, false, err
	}
	result.Request, err = model.RestoreProviderRequest(route, modelID, requestBody)
	if err != nil {
		return fixtureCardResult{}, false, err
	}
	if client == nil {
		return fixtureCardResult{}, false, errors.New("model client is required to reopen a completed card execution")
	}
	if err := client.ValidateRequest(result.Request); err != nil {
		return fixtureCardResult{}, false, err
	}
	rows, err := db.DB().QueryContext(ctx, `select observation_type, value_text from model_observation where generation_id = (select generation_id from card_execution where id = ?) order by rowid`, result.ExecutionID)
	if err != nil {
		return fixtureCardResult{}, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return fixtureCardResult{}, false, err
		}
		switch kind {
		case modelObservationCardSummary:
			result.Summary = value
		case modelObservationCardDescription:
			result.Description = value
		case modelObservationCardVisibleText:
			result.VisibleText = value
		case modelObservationCardUncertainty:
			result.Uncertainties = append(result.Uncertainties, value)
		}
	}
	if err := rows.Err(); err != nil {
		return fixtureCardResult{}, false, err
	}
	if err := rows.Close(); err != nil {
		return fixtureCardResult{}, false, err
	}
	var cardBytes []byte
	err = db.DB().QueryRowContext(ctx, `select card from photo_card where generation_id = (select generation_id from card_execution where id = ?)`, result.ExecutionID).Scan(&cardBytes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fixtureCardResult{}, false, fmt.Errorf("read completed typed photo card: %w", err)
	}
	if err == nil {
		card := new(cardwire.PhotoCard)
		if err := proto.Unmarshal(cardBytes, card); err != nil {
			return fixtureCardResult{}, false, fmt.Errorf("decode completed typed photo card: %w", err)
		}
		result.Location = modelLocation{
			Kind: card.GetLocation().GetKind(), CandidateID: card.GetLocation().GetCandidateId(),
			InferredName: card.GetLocation().GetInferredName(), Confidence: card.GetLocation().GetConfidence(),
			Reason: card.GetLocation().GetReason(),
		}
	}
	result.Reused = true
	return result, true, nil
}
