package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
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
	ExecutionID       string
	Input             cardinput.Result
	Request           model.ProviderRequest
	RawResponse       model.RawResult
	PromptVersion     string
	ParserVersion     string
	Summary           string
	Description       string
	OCR               string
	Uncertainties     []string
	VenuePlausibility venuePlausibility
	Custody           *cardwire.CardExecutionCustody
	Reused            bool
}

func executeFixtureCard(ctx context.Context, db *store.Store, executionID string, prepare func() (fixtureCardPreparation, error), classifier modelClassifier, fixtureBytes []byte, now time.Time) (fixtureCardResult, error) {
	if strings.TrimSpace(executionID) == "" {
		return fixtureCardResult{}, errors.New("fixture card execution id is required")
	}
	if completed, found, err := readCompletedFixtureCard(ctx, db, executionID); err != nil || found {
		return completed, err
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
	imageDigest := sha256.Sum256(prepared.CurrentStill)
	if int64(len(prepared.CurrentStill)) != prepared.Source.FullCurrent.SizeBytes || hex.EncodeToString(imageDigest[:]) != prepared.Source.FullCurrent.SHA256 {
		return fixtureCardResult{}, errors.New("current-still bytes do not match checked CardInput facts")
	}
	request, image, err := classifier.buildRequestFromBytes(prepared.Classify, prepared.CurrentStill, prepared.MIMEType, imagemetadata.Projection{Lines: prepared.Source.Metadata.ProjectionLines})
	if err != nil {
		return fixtureCardResult{}, err
	}
	providerRequest, err := classifier.client.Render(request)
	if err != nil {
		return fixtureCardResult{}, err
	}
	wantExecutionID := fixtureCardExecutionID(prepared.Source.AssetID, input.ID, providerRequest, classifier.promptVersion, modelParserVersion)
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
	decision, err := prepareModelGeneration(ctx, db, prepared.Source.AssetID, classifier.promptVersion, modelParserVersion, providerRequest, now)
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
	response, err := model.Parse(providerRequest, raw)
	if err != nil {
		return fixtureCardResult{}, err
	}
	parsed, err := classifier.parseResult(response.Text, prepared.Classify, image)
	if err != nil {
		return fixtureCardResult{}, err
	}
	custody := executionCustody(prepared.Source, prepared.Artifacts, prepared.Evidence)
	custodyBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(custody)
	if err != nil {
		return fixtureCardResult{}, err
	}
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := writeModelClassification(ctx, tx, prepared.Classify, classifier, parsed, now, decision.GenerationID); err != nil {
			return err
		}
		if err := completeModelGeneration(ctx, tx, decision.GenerationID, prepared.Source.AssetID, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `insert into card_execution(id, asset_id, card_input_id, card_input, generation_id, custody, completed_at) values (?, ?, ?, ?, ?, ?, ?)`, executionID, prepared.Source.AssetID, input.ID, input.Bytes, decision.GenerationID, custodyBytes, now.UTC().Format(time.RFC3339Nano))
		return err
	})
	if err != nil {
		return fixtureCardResult{}, err
	}
	return fixtureCardResult{ExecutionID: executionID, Input: input, Request: providerRequest, RawResponse: raw, PromptVersion: classifier.promptVersion, ParserVersion: modelParserVersion, Summary: parsed.Observations[0].ValueText, Description: parsed.Observations[1].ValueText, OCR: cardValue(parsed.Observations, modelObservationCardOCR), Uncertainties: cardValues(parsed.Observations, modelObservationCardUncertainty), VenuePlausibility: parsed.VenuePlausibility, Custody: custody}, nil
}

func fixtureCardExecutionID(assetID, cardInputID string, request model.ProviderRequest, promptVersion, parserVersion string) string {
	digest := request.Digest()
	return stableID("card_execution", assetID, cardInputID, hex.EncodeToString(digest[:]), promptVersion, parserVersion)
}

func executionCustody(source cardinput.SourceFacts, artifacts cardinput.CheckedArtifacts, records []place.EvidenceRecord) *cardwire.CardExecutionCustody {
	custody := &cardwire.CardExecutionCustody{SourceId: source.SourceID, AssetId: source.AssetID, ImmutableOriginalResourceId: artifacts.ImmutableOriginal.ResourceID, MetadataRecordId: artifacts.Metadata.RecordID, MetadataProjectionId: artifacts.Metadata.ProjectionID, FullCurrentProofSha256: artifacts.FullCurrent.ProofSHA256}
	for _, record := range records {
		custody.Evidence = append(custody.Evidence, &cardwire.EvidenceLink{ProviderIdentity: record.ProviderIdentity, Operation: record.Operation, RawResponseSha256: record.RawResponseSHA256})
	}
	return custody
}

func readCompletedFixtureCard(ctx context.Context, db *store.Store, executionID string) (fixtureCardResult, bool, error) {
	var result fixtureCardResult
	var inputBytes, custodyBytes, requestBody []byte
	var route, modelID string
	err := db.DB().QueryRowContext(ctx, `select c.id, c.card_input_id, c.card_input, c.custody, g.request_route, g.model_id, g.request_body, ga.prompt_version, ga.parser_version, a.response_body, a.failure_body, a.http_status_text, a.http_status, a.provider_request_id, a.transmission_started from card_execution c join model_generation g on g.id = c.generation_id join model_generation_asset ga on ga.generation_id = g.id and ga.asset_id = c.asset_id join model_generation_attempt a on a.generation_id = g.id where c.id = ?`, executionID).Scan(&result.ExecutionID, &result.Input.ID, &inputBytes, &custodyBytes, &route, &modelID, &requestBody, &result.PromptVersion, &result.ParserVersion, &result.RawResponse.Response, &result.RawResponse.Failure, &result.RawResponse.Status, &result.RawResponse.StatusCode, &result.RawResponse.ProviderRequestID, &result.RawResponse.TransmissionStarted)
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
	rows, err := db.DB().QueryContext(ctx, `select observation_type, value_text from model_observation where generation_id = (select generation_id from card_execution where id = ?) order by rowid`, result.ExecutionID)
	if err != nil {
		return fixtureCardResult{}, false, err
	}
	defer rows.Close()
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
		case modelObservationCardOCR:
			result.OCR = value
		case modelObservationCardUncertainty:
			result.Uncertainties = append(result.Uncertainties, value)
		}
	}
	var venueJSON string
	err = db.DB().QueryRowContext(ctx, `select value_json from place_observation where generation_id = (select generation_id from card_execution where id = ?) and json_extract(value_json, '$.venue_plausibility.verdict') <> '' limit 1`, result.ExecutionID).Scan(&venueJSON)
	if err == nil {
		var value struct {
			Venue venuePlausibility `json:"venue_plausibility"`
		}
		if err := json.Unmarshal([]byte(venueJSON), &value); err != nil {
			return fixtureCardResult{}, false, err
		}
		result.VenuePlausibility = value.Venue
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fixtureCardResult{}, false, err
	}
	result.Reused = true
	return result, true, rows.Err()
}

func cardValue(observations []contentObservation, kind string) string {
	values := cardValues(observations, kind)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func cardValues(observations []contentObservation, kind string) []string {
	values := []string{}
	for _, observation := range observations {
		if observation.ObservationType == kind {
			values = append(values, strings.TrimSpace(observation.ValueText))
		}
	}
	return values
}
