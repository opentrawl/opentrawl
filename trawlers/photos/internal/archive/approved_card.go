package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type ApprovedCardPrepareOptions struct {
	ArchivePath             string
	CacheDir                string
	SourceLibraryID         string
	AssetIDs                []string
	Model                   string
	ModelURL                string
	ProviderIdentity        string
	CredentialEnv           string
	Purpose                 string
	CallCap                 int
	PlaceEvidenceOperations []place.CheckedOperation
}

func OpenApprovedCardArchive(ctx context.Context, path string) (*store.Store, error) {
	return openArchive(ctx, path)
}

// PrepareApprovedCardBundle reads only the canonical archive and checked cache
// seams. No caller can pass CardInput, custody or provider request bytes.
func PrepareApprovedCardBundle(ctx context.Context, options ApprovedCardPrepareOptions) ([]byte, error) {
	if strings.TrimSpace(options.ArchivePath) == "" || strings.TrimSpace(options.CacheDir) == "" ||
		strings.TrimSpace(options.Model) == "" ||
		strings.TrimSpace(options.ModelURL) == "" || strings.TrimSpace(options.ProviderIdentity) == "" || strings.TrimSpace(options.CredentialEnv) == "" {
		return nil, errors.New("approved card prepare requires archive, cache, provider, model, model URL and credential reference")
	}
	if len(options.AssetIDs) == 0 {
		return nil, errors.New("approved card prepare requires at least one asset")
	}
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	if strings.TrimSpace(options.SourceLibraryID) == "" {
		if err := db.DB().QueryRowContext(ctx, `select source_library_id from asset where id = ?`, strings.TrimSpace(options.AssetIDs[0])).Scan(&options.SourceLibraryID); err != nil {
			return nil, fmt.Errorf("read approved card source: %w", err)
		}
	}
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, errors.New("approved card prepare requires a complete source snapshot")
	}
	classifier, err := newModelClassifier(options.Model, options.ModelURL, "")
	if err != nil {
		return nil, err
	}
	items := make([]*cardwire.ApprovedCardItem, 0, len(options.AssetIDs))
	seen := map[string]struct{}{}
	for _, assetID := range options.AssetIDs {
		assetID = strings.TrimSpace(assetID)
		if assetID == "" {
			return nil, errors.New("approved card asset id is required")
		}
		if _, found := seen[assetID]; found {
			return nil, errors.New("approved card assets must be unique")
		}
		seen[assetID] = struct{}{}
		item, err := prepareApprovedCardFromArchive(ctx, db.DB(), options, classifier, assetID, len(items)+1)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return marshalApprovedCardBundle(paidCallPurpose(options.Purpose), options.CallCap, options.ProviderIdentity, options.CredentialEnv, items)
}

func prepareApprovedCardFromArchive(ctx context.Context, db *sql.DB, options ApprovedCardPrepareOptions, classifier modelClassifier, assetID string, position int) (*cardwire.ApprovedCardItem, error) {
	input, err := loadCardInputAuditInput(ctx, db, options.SourceLibraryID, assetID)
	if err != nil {
		return nil, err
	}
	if input.SourceState != sourceStateCurrent || input.MediaType != "image" {
		return nil, errors.New("approved card asset is not current image content")
	}
	eligibility, err := firstCardEligibilityForAsset(ctx, db, assetID)
	if err != nil {
		return nil, err
	}
	if eligibility != firstCardEligible {
		return nil, fmt.Errorf("approved card asset is %s", eligibility)
	}
	original, _, _, ok, err := cardInputAuditCheckedOriginal(input, filepath.Join(options.CacheDir, "originals"))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("approved card immutable original is unavailable")
	}
	metadata, ok := imagemetadata.ReadCheckedArtifacts(filepath.Join(options.CacheDir, "image-metadata"), original.SHA256)
	if !ok {
		return nil, errors.New("approved card checked metadata is unavailable")
	}
	freshnessRequest, err := input.currentStillRequest()
	if err != nil {
		return nil, err
	}
	path, current, proofSHA256, ok := readApprovedCardCurrentStill(options.CacheDir, freshnessRequest)
	if !ok {
		return nil, errors.New("approved card checked current still is unavailable")
	}
	evidence, evidenceOK := checkedPlaceEvidence(options.CacheDir, input, options.PlaceEvidenceOperations)
	if !evidenceOK {
		return nil, errors.New(cardInputAuditStopMissingPlace)
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read approved card current still: %w", err)
	}
	source, artifacts := cardInputAuditFacts(input, original, metadata, current, proofSHA256, options.PlaceEvidenceOperations)
	item, err := prepareCard(preparedCard{
		source: source, artifacts: artifacts, evidence: evidence, classify: input, currentStill: image,
		classifier: classifier,
	}, position)
	if isPlaceEvidenceError(err) {
		return nil, errors.New(cardInputAuditStopMissingPlace)
	}
	return item, err
}

func readApprovedCardCurrentStill(cacheDir string, request photos.CurrentStillRequest) (string, photos.CurrentStillFact, string, bool) {
	return photos.ReadCachedCurrentStill(filepath.Join(cacheDir, "originals"), request.SourceLibraryID, request.AssetUUID, request.Freshness)
}

// approvedCardTransport keeps configuration validation and the exact send on
// the same boundary. model.Client is the production implementation.
type approvedCardTransport interface {
	ValidateRequest(model.ProviderRequest) error
	Send(context.Context, model.ProviderRequest) (model.RawResult, error)
}

type preparedCard struct {
	source       cardinput.SourceFacts
	artifacts    cardinput.CheckedArtifacts
	evidence     []place.EvidenceRecord
	classify     classifyInput
	currentStill []byte
	classifier   modelClassifier
}

// prepareCard is the only route from checked archive facts to an approved-card
// item. It does not accept a preassembled CardInput, custody or request.
func prepareCard(value preparedCard, position int) (*cardwire.ApprovedCardItem, error) {
	if position < 1 || value.source.AssetID != value.classify.AssetID ||
		value.source.SourceID == "" || value.source.SourceID != value.classify.SourceLibraryID {
		return nil, errors.New("approved card source identities do not match")
	}
	prepared, err := renderPreparedCardRequest(value.source, value.artifacts, value.evidence, value.currentStill, value.classifier)
	if err != nil {
		return nil, err
	}
	executionID := approvedCardExecutionID(
		value.source.AssetID, prepared,
	)
	return &cardwire.ApprovedCardItem{
		Position:          uint32(position),
		AssetId:           value.source.AssetID,
		CardInputId:       prepared.Input.ID,
		CardInput:         prepared.Input.Bytes,
		Custody:           prepared.CustodyBytes,
		CustodySha256:     prepared.CustodySHA256,
		FullCurrentSha256: value.source.FullCurrent.SHA256,
		RequestRoute:      prepared.Request.Route(),
		ModelId:           prepared.Request.Model(),
		RequestBody:       prepared.Request.Body(),
		RequestBodyLength: uint64(len(prepared.Request.Body())),
		RequestSha256:     prepared.RequestSHA256,
		PromptVersion:     prepared.PromptVersion,
		ParserVersion:     prepared.ParserVersion,
		ExecutionId:       executionID,
	}, nil
}

func approvedCardExecutionID(assetID string, request preparedCardRequest) string {
	requestDigest := request.Request.Digest()
	return stableID("card_execution", assetID, request.Input.ID, request.CustodySHA256, hex.EncodeToString(requestDigest[:]), request.CardRequestID, request.PromptVersion, request.ParserVersion)
}

func marshalApprovedCardBundle(purpose paidCallPurpose, callCap int, providerIdentity, credentialEnv string, items []*cardwire.ApprovedCardItem) ([]byte, error) {
	bundle := &cardwire.ApprovedCardBundle{
		Purpose: string(purpose), CallCap: uint32(callCap), Items: items,
		ProviderIdentity: strings.TrimSpace(providerIdentity), CredentialEnv: strings.TrimSpace(credentialEnv),
	}
	if err := validateApprovedCardBundle(bundle); err != nil {
		return nil, err
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(bundle)
}

func decodeApprovedCardBundle(data []byte) (*cardwire.ApprovedCardBundle, error) {
	if len(data) == 0 {
		return nil, errors.New("approved card bundle is required")
	}
	bundle := new(cardwire.ApprovedCardBundle)
	if err := proto.Unmarshal(data, bundle); err != nil {
		return nil, fmt.Errorf("decode approved card bundle: %w", err)
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(bundle)
	if err != nil {
		return nil, fmt.Errorf("marshal approved card bundle: %w", err)
	}
	if !bytes.Equal(data, canonical) {
		return nil, errors.New("approved card bundle is not canonical protobuf bytes")
	}
	if err := validateApprovedCardBundle(bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

func validateApprovedCardBundle(bundle *cardwire.ApprovedCardBundle) error {
	if bundle.GetPurpose() != string(paidCallPurposeCanary) && bundle.GetPurpose() != string(paidCallPurposeBackfill) {
		return errors.New("approved card purpose must be canary or backfill")
	}
	if bundle.GetCallCap() == 0 || int(bundle.GetCallCap()) > len(bundle.GetItems()) {
		return errors.New("approved card call cap is invalid")
	}
	if strings.TrimSpace(bundle.GetCredentialEnv()) == "" {
		return errors.New("approved card credential reference is required")
	}
	if strings.TrimSpace(bundle.GetProviderIdentity()) == "" {
		return errors.New("approved card provider identity is required")
	}
	seenAssets := map[string]struct{}{}
	for index, item := range bundle.GetItems() {
		if item.GetPosition() != uint32(index+1) {
			return errors.New("approved card item positions must be contiguous and start at 1")
		}
		if err := validateApprovedCardItem(item); err != nil {
			return fmt.Errorf("approved card item %d: %w", index+1, err)
		}
		if _, found := seenAssets[item.GetAssetId()]; found {
			return errors.New("approved card bundle contains the same asset twice")
		}
		seenAssets[item.GetAssetId()] = struct{}{}
	}
	return nil
}

func validateApprovedCardItem(item *cardwire.ApprovedCardItem) error {
	for name, value := range map[string]string{
		"asset id": item.GetAssetId(), "CardInput id": item.GetCardInputId(),
		"custody digest": item.GetCustodySha256(), "full-current digest": item.GetFullCurrentSha256(),
		"request route": item.GetRequestRoute(), "model id": item.GetModelId(),
		"request digest": item.GetRequestSha256(), "prompt version": item.GetPromptVersion(),
		"parser version": item.GetParserVersion(), "execution id": item.GetExecutionId(),
	} {
		if err := requireExactPaidCallValue(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"custody": item.GetCustodySha256(), "full-current": item.GetFullCurrentSha256(), "request": item.GetRequestSha256(),
	} {
		if err := validateSHA256(name, value); err != nil {
			return err
		}
	}
	input := new(cardwire.CardInput)
	if len(item.GetCardInput()) == 0 || proto.Unmarshal(item.GetCardInput(), input) != nil {
		return errors.New("CardInput bytes are invalid")
	}
	inputBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil || !bytes.Equal(inputBytes, item.GetCardInput()) {
		return errors.New("CardInput bytes are not canonical")
	}
	inputDigest := sha256.Sum256(item.GetCardInput())
	if item.GetCardInputId() != "card_input:"+hex.EncodeToString(inputDigest[:]) {
		return errors.New("CardInput identity does not match its bytes")
	}
	if input.GetFullCurrent() == nil || input.GetFullCurrent().GetSha256() != item.GetFullCurrentSha256() {
		return errors.New("CardInput full-current digest does not match the item")
	}
	custody := new(cardwire.CardExecutionCustody)
	if len(item.GetCustody()) == 0 || proto.Unmarshal(item.GetCustody(), custody) != nil || custody.GetAssetId() != item.GetAssetId() {
		return errors.New("custody bytes do not match the item asset")
	}
	custodyBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(custody)
	if err != nil || !bytes.Equal(custodyBytes, item.GetCustody()) {
		return errors.New("custody bytes are not canonical")
	}
	custodyDigest := sha256.Sum256(item.GetCustody())
	if item.GetCustodySha256() != hex.EncodeToString(custodyDigest[:]) {
		return errors.New("custody digest does not match its bytes")
	}
	if custody.GetCardInputSha256() != item.GetCardInputId()[len("card_input:"):] {
		return errors.New("custody CardInput digest does not match the item")
	}
	if uint64(len(item.GetRequestBody())) != item.GetRequestBodyLength() {
		return errors.New("request body length does not match its bytes")
	}
	request, err := model.RestoreProviderRequest(item.GetRequestRoute(), item.GetModelId(), item.GetRequestBody())
	if err != nil {
		return err
	}
	requestDigest := request.Digest()
	if item.GetRequestSha256() != hex.EncodeToString(requestDigest[:]) {
		return errors.New("request digest does not match its bytes")
	}
	if custody.GetRequestSha256() != item.GetRequestSha256() {
		return errors.New("custody request digest does not match the item")
	}
	prepared, err := restorePreparedCardRequestUnchecked(item)
	if err != nil {
		return err
	}
	if item.GetExecutionId() != approvedCardExecutionID(item.GetAssetId(), prepared) {
		return errors.New("execution identity does not match the prepared bytes")
	}
	return nil
}

func ValidateApprovedCardBundleFreshness(ctx context.Context, bundle []byte, options ApprovedCardPrepareOptions) error {
	stored, err := decodeApprovedCardBundle(bundle)
	if err != nil {
		return err
	}
	assets := make([]string, 0, len(stored.GetItems()))
	for _, item := range stored.GetItems() {
		assets = append(assets, item.GetAssetId())
	}
	options.AssetIDs, options.Purpose, options.CallCap = assets, stored.GetPurpose(), int(stored.GetCallCap())
	fresh, err := PrepareApprovedCardBundle(ctx, options)
	if err != nil {
		return fmt.Errorf("approved card request is stale: %w", err)
	}
	if !bytes.Equal(bundle, fresh) {
		return errors.New("approved card request is stale")
	}
	return nil
}
