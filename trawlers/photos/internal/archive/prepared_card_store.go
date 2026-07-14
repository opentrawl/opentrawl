package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func retainPreparedCardRequest(ctx context.Context, tx *sql.Tx, executionID, assetID, generationID string, prepared preparedCardRequest) error {
	if err := validatePreparedCardRequest(prepared); err != nil {
		return err
	}
	if executionID == "" || assetID == "" || generationID == "" || prepared.Custody.GetAssetId() != assetID {
		return fmt.Errorf("%w: execution identity", errPreparedCardMismatch)
	}
	result, err := tx.ExecContext(ctx, `
insert into card_execution(id, asset_id, card_input_id, card_input, generation_id, custody, completed_at)
values (?, ?, ?, ?, ?, ?, '')
on conflict(id) do nothing
`, executionID, assetID, prepared.Input.ID, prepared.Input.Bytes, generationID, prepared.CustodyBytes)
	if err != nil {
		return fmt.Errorf("retain prepared card request: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read retained prepared card request count: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	var gotAssetID, gotCardInputID, gotGenerationID, completedAt string
	var gotCardInput, gotCustody []byte
	if err := tx.QueryRowContext(ctx, `select asset_id, card_input_id, card_input, generation_id, custody, completed_at from card_execution where id = ?`, executionID).
		Scan(&gotAssetID, &gotCardInputID, &gotCardInput, &gotGenerationID, &gotCustody, &completedAt); err != nil {
		return fmt.Errorf("read retained prepared card request: %w", err)
	}
	if gotAssetID != assetID || gotCardInputID != prepared.Input.ID || !bytes.Equal(gotCardInput, prepared.Input.Bytes) ||
		gotGenerationID != generationID || !bytes.Equal(gotCustody, prepared.CustodyBytes) {
		return fmt.Errorf("%w: retained prepared card request", errPreparedCardMismatch)
	}
	return nil
}

func completePreparedCardRequest(ctx context.Context, tx *sql.Tx, executionID string, completedAt string) error {
	result, err := tx.ExecContext(ctx, `update card_execution set completed_at = ? where id = ? and completed_at = ''`, completedAt, executionID)
	if err != nil {
		return fmt.Errorf("complete prepared card request: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read completed prepared card request count: %w", err)
	}
	if updated != 1 {
		return errors.New("prepared card request is missing or already complete")
	}
	return nil
}

func restoreRetainedPreparedCardRequest(ctx context.Context, db *store.Store, executionID string, client *model.Client) (preparedCardRequest, string, bool, error) {
	return restoreRetainedPreparedCardRequestWhere(ctx, db, `c.id = ?`, []any{executionID}, client)
}

func restoreRetainedPreparedCardRequestForAsset(ctx context.Context, db *store.Store, assetID string, client *model.Client) (preparedCardRequest, string, bool, error) {
	return restoreRetainedPreparedCardRequestWhere(ctx, db, `c.asset_id = ? and c.completed_at = ''`, []any{assetID}, client)
}

func restoreRetainedPreparedCardRequestWhere(ctx context.Context, db *store.Store, predicate string, args []any, client *model.Client) (preparedCardRequest, string, bool, error) {
	var item cardwire.ApprovedCardItem
	var generationID string
	row := db.DB().QueryRowContext(ctx, `
select c.id, c.asset_id, c.card_input_id, c.card_input, c.custody, g.request_route, g.model_id,
       g.request_body, g.request_sha256, ga.prompt_version, ga.parser_version, c.generation_id
from card_execution c
join model_generation g on g.id = c.generation_id
join model_generation_asset ga on ga.generation_id = g.id and ga.asset_id = c.asset_id
where `+predicate+`
order by c.rowid desc limit 1
`, args...)
	if err := row.Scan(&item.ExecutionId, &item.AssetId, &item.CardInputId, &item.CardInput, &item.Custody,
		&item.RequestRoute, &item.ModelId, &item.RequestBody, &item.RequestSha256, &item.PromptVersion,
		&item.ParserVersion, &generationID); errors.Is(err, sql.ErrNoRows) {
		return preparedCardRequest{}, "", false, nil
	} else if err != nil {
		return preparedCardRequest{}, "", false, fmt.Errorf("read retained prepared card request: %w", err)
	}
	item.RequestBodyLength = uint64(len(item.RequestBody))
	item.FullCurrentSha256 = cardInputFullCurrentSHA256(item.CardInput)
	item.CustodySha256 = digestBytes(item.Custody)
	prepared, err := restorePreparedCardRequest(&item, client)
	if err != nil {
		return preparedCardRequest{}, "", false, err
	}
	return prepared, generationID, true, nil
}

func cardInputFullCurrentSHA256(data []byte) string {
	input := new(cardwire.CardInput)
	if err := proto.Unmarshal(data, input); err == nil {
		return input.GetFullCurrent().GetSha256()
	}
	return ""
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
