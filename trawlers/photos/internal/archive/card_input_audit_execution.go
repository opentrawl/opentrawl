package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// readStoredCardInputAuditBoundary reopens the exact checked input and request
// retained by the production execution seam. It does not reconstruct checked
// artefacts from source-library resource metadata.
func readStoredCardInputAuditBoundary(ctx context.Context, db *sql.DB, assetID string) (CardInputAuditInspection, bool, error) {
	var cardInputID, requestSHA256, route, modelID string
	var inputBytes, requestBody []byte
	err := db.QueryRowContext(ctx, `
select execution.card_input_id, execution.card_input,
       generation.request_sha256, generation.request_route,
       generation.model_id, generation.request_body
from card_execution execution
join model_generation generation on generation.id = execution.generation_id
where execution.asset_id = ?
  and exists (
    select 1 from model_observation observation
    where observation.generation_id = execution.generation_id
      and observation.asset_id = execution.asset_id
      and observation.observation_type = ?
      and observation.stale_since is null
      and observation.superseded_at is null
  )
order by execution.completed_at desc, execution.id desc
limit 1`, assetID, modelObservationCardSummary).Scan(&cardInputID, &inputBytes, &requestSHA256, &route, &modelID, &requestBody)
	if errors.Is(err, sql.ErrNoRows) {
		return CardInputAuditInspection{}, false, nil
	}
	if err != nil {
		return CardInputAuditInspection{}, false, fmt.Errorf("read stored card-input audit boundary: %w", err)
	}
	inputDigest := sha256.Sum256(inputBytes)
	if cardInputID != "card_input:"+hex.EncodeToString(inputDigest[:]) {
		return CardInputAuditInspection{}, false, errors.New("stored card-input audit identity does not match its bytes")
	}
	input := new(cardwire.CardInput)
	if err := proto.Unmarshal(inputBytes, input); err != nil {
		return CardInputAuditInspection{}, false, fmt.Errorf("decode stored card-input audit boundary: %w", err)
	}
	cardJSON, err := protojson.MarshalOptions{Indent: "  "}.Marshal(input)
	if err != nil {
		return CardInputAuditInspection{}, false, fmt.Errorf("render stored card-input audit boundary: %w", err)
	}
	request, err := model.RestoreProviderRequest(route, modelID, requestBody)
	if err != nil {
		return CardInputAuditInspection{}, false, fmt.Errorf("restore stored card-input audit request: %w", err)
	}
	requestDigest := request.Digest()
	if requestSHA256 != hex.EncodeToString(requestDigest[:]) {
		return CardInputAuditInspection{}, false, errors.New("stored card-input audit request identity does not match its bytes")
	}
	return CardInputAuditInspection{
		CardInput:       cardJSON,
		CardInputWire:   base64.StdEncoding.EncodeToString(inputBytes),
		RenderedRequest: request.Body(),
		RenderedRoute:   request.Route(),
		RenderedModel:   request.Model(),
	}, true, nil
}
