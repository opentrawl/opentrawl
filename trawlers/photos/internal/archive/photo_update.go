package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	locationwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/location"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

type PhotoUpdateAsset struct {
	AssetID           PhotoAssetID
	SourceLibraryID   string
	SourceFingerprint PhotoSourceFingerprint
	LocalIdentifier   PhotosLocalIdentifier
	MediaType         PhotoMediaKind
	MediaSubtypes     string
	CreationTime      OptionalPhotosTimestamp
	ModificationTime  OptionalPhotosTimestamp
	PixelWidth        int64
	PixelHeight       int64
	CameraMake        string
	CameraModel       string
	LensModel         string
	FocalLengthMM     sql.NullFloat64
	Aperture          sql.NullFloat64
	ExposureSeconds   sql.NullFloat64
	ISO               sql.NullInt64
	OriginalResources []PhotoUpdateOriginalResource
}

type PhotoAssetID string
type PhotosLocalIdentifier string
type PhotoSourceFingerprint string
type PhotoMediaKind string

type OptionalPhotosTimestamp struct {
	Value   time.Time
	Present bool
}

const PhotoMediaKindImage PhotoMediaKind = "image"

type PhotoUpdateOriginalResource struct {
	Filename              string
	UniformTypeIdentifier string
	IndexedByteCount      int64
}

type photoUpdateAssetRowScanner interface {
	Scan(destinations ...any) error
}

func scanPhotoUpdateAssetProjection(scanner photoUpdateAssetRowScanner, trailingDestinations ...any) (PhotoUpdateAsset, sql.NullString, sql.NullString, sql.NullInt64, error) {
	var asset PhotoUpdateAsset
	var creationTimeText, modificationTimeText string
	var originalFilename, originalUniformTypeIdentifier sql.NullString
	var originalByteCount sql.NullInt64
	destinations := []any{
		&asset.AssetID, &asset.SourceLibraryID, &asset.SourceFingerprint, &asset.LocalIdentifier,
		&asset.MediaType, &asset.MediaSubtypes, &creationTimeText, &modificationTimeText,
		&asset.PixelWidth, &asset.PixelHeight, &asset.CameraMake, &asset.CameraModel, &asset.LensModel,
		&asset.FocalLengthMM, &asset.Aperture, &asset.ExposureSeconds, &asset.ISO,
		&originalFilename, &originalUniformTypeIdentifier, &originalByteCount,
	}
	destinations = append(destinations, trailingDestinations...)
	if err := scanner.Scan(destinations...); err != nil {
		return PhotoUpdateAsset{}, sql.NullString{}, sql.NullString{}, sql.NullInt64{}, err
	}
	var err error
	asset.CreationTime, err = parseOptionalPhotosTimestamp(creationTimeText)
	if err != nil {
		return PhotoUpdateAsset{}, sql.NullString{}, sql.NullString{}, sql.NullInt64{}, fmt.Errorf("parse Photos creation time for asset %q: %w", asset.AssetID, err)
	}
	asset.ModificationTime, err = parseOptionalPhotosTimestamp(modificationTimeText)
	if err != nil {
		return PhotoUpdateAsset{}, sql.NullString{}, sql.NullString{}, sql.NullInt64{}, fmt.Errorf("parse Photos modification time for asset %q: %w", asset.AssetID, err)
	}
	return asset, originalFilename, originalUniformTypeIdentifier, originalByteCount, nil
}

func appendPhotoUpdateOriginalResource(asset *PhotoUpdateAsset, filename, uniformTypeIdentifier sql.NullString, byteCount sql.NullInt64) {
	if asset == nil || !filename.Valid {
		return
	}
	asset.OriginalResources = append(asset.OriginalResources, PhotoUpdateOriginalResource{
		Filename: filename.String, UniformTypeIdentifier: uniformTypeIdentifier.String, IndexedByteCount: byteCount.Int64,
	})
}

type PhotoUpdateResultKind string

const (
	PhotoUpdateResultCardStored       PhotoUpdateResultKind = "card_stored"
	PhotoUpdateResultMediaUnavailable PhotoUpdateResultKind = "media_unavailable"
	PhotoUpdateResultUnsupportedMedia PhotoUpdateResultKind = "unsupported_media"
)

type RetainedPhotoCardGeneration struct {
	InputSHA256                        []byte
	RequestText                        string
	ResponseBody                       []byte
	ResponseRejected                   bool
	ModelIdentifier                    string
	ThreadIdentifier                   string
	TurnIdentifier                     string
	DescriptionsRepairRequestText      string
	DescriptionsRepairResponseBody     []byte
	DescriptionsRepairResponseRejected bool
	DescriptionsRepairThreadID         string
	DescriptionsRepairTurnID           string
}

type RetainedPhotoTextExtraction struct {
	InputSHA256      []byte
	RequestText      string
	ResponseBody     []byte
	ResponseRejected bool
	ModelIdentifier  string
	ThreadIdentifier string
	TurnIdentifier   string
}

type RetainedPhotoTextVerification struct {
	InputSHA256        []byte
	RequestText        string
	ResponseBody       []byte
	ResponseRejected   bool
	ModelIdentifier    string
	ThreadIdentifier   string
	TurnIdentifier     string
	ResponseRetainedAt string
}

type RetainedCurrentPhotoMediaEvidence struct {
	SourceFingerprint               PhotoSourceFingerprint
	ImmutableOriginalImageFacts     *mediawire.ImmutableOriginalImageFacts
	CurrentRenderedStillSHA256      []byte
	CurrentRenderedStillMediaType   string
	CurrentRenderedStillByteCount   uint64
	CurrentRenderedStillPixelWidth  uint64
	CurrentRenderedStillPixelHeight uint64
	CurrentRenderedStillOrientation mediawire.ImageOrientation
}

type PhotoModelGenerationTokenUsage struct {
	InputTokens           int64
	CachedInputTokens     int64
	OutputTokens          int64
	ReasoningOutputTokens int64
	TotalTokens           int64
}

type RetainedPhotoModelGenerationOperation struct {
	State            PhotoModelGenerationState
	ThreadIdentifier string
	TurnIdentifier   string
}

type PhotoModelGenerationPhase int
type PhotoModelGenerationState int

const (
	PhotoModelGenerationPhaseTextExtraction      PhotoModelGenerationPhase = 1
	PhotoModelGenerationPhaseSemanticCard        PhotoModelGenerationPhase = 2
	PhotoModelGenerationPhaseDescriptionRepair   PhotoModelGenerationPhase = 3
	PhotoModelGenerationPhaseTextVerification    PhotoModelGenerationPhase = 4
	PhotoModelGenerationStateRequestRetained     PhotoModelGenerationState = 1
	PhotoModelGenerationStateTransmissionStarted PhotoModelGenerationState = 2
	PhotoModelGenerationStateResponseRetained    PhotoModelGenerationState = 3
	PhotoModelGenerationStateSucceeded           PhotoModelGenerationState = 4
	PhotoModelGenerationStateFailed              PhotoModelGenerationState = 5
)

func RetainPhotoModelGenerationOperationStage(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, phase PhotoModelGenerationPhase, state PhotoModelGenerationState, threadIdentifier, turnIdentifier, failureDetail string, changedAt time.Time) error {
	if len(inputSHA256) != sha256.Size || phase < PhotoModelGenerationPhaseTextExtraction || phase > PhotoModelGenerationPhaseTextVerification || state < PhotoModelGenerationStateRequestRetained || state > PhotoModelGenerationStateFailed {
		return errors.New("photo model generation operation stage is incomplete")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		changedAtText := changedAt.UTC().Format(time.RFC3339Nano)
		switch state {
		case PhotoModelGenerationStateTransmissionStarted:
			if _, err := tx.ExecContext(ctx, `insert into photo_model_generation_transmission_attempt(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, transmission_started_at) values (?, ?, ?, ?, ?, ?, ?)`, assetID, inputSHA256, phase, state, threadIdentifier, turnIdentifier, changedAtText); err != nil {
				return err
			}
		case PhotoModelGenerationStateResponseRetained, PhotoModelGenerationStateSucceeded, PhotoModelGenerationStateFailed:
			var completedAt any
			if state == PhotoModelGenerationStateSucceeded || state == PhotoModelGenerationStateFailed {
				completedAt = changedAtText
			}
			if _, err := tx.ExecContext(ctx, `update photo_model_generation_transmission_attempt set operation_state=?, failure_detail=?, completed_at=? where attempt_id=(select attempt_id from photo_model_generation_transmission_attempt where asset_id=? and input_sha256=? and operation_phase=? and completed_at is null order by attempt_id desc limit 1)`, state, strings.TrimSpace(failureDetail), completedAt, assetID, inputSHA256, phase); err != nil {
				return err
			}
		}
		if state == PhotoModelGenerationStateRequestRetained {
			_, err := tx.ExecContext(ctx, `
insert into photo_model_generation_operation(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at)
values (?, ?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id, input_sha256, operation_phase) do nothing`, assetID, inputSHA256, phase, state, threadIdentifier, turnIdentifier, strings.TrimSpace(failureDetail), changedAtText)
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into photo_model_generation_operation(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at)
values (?, ?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id, input_sha256, operation_phase) do update set operation_state=excluded.operation_state, thread_identifier=excluded.thread_identifier, turn_identifier=excluded.turn_identifier, failure_detail=excluded.failure_detail, changed_at=excluded.changed_at`, assetID, inputSHA256, phase, state, threadIdentifier, turnIdentifier, strings.TrimSpace(failureDetail), changedAtText)
		return err
	})
}

func LoadRetainedPhotoModelGenerationOperation(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, phase PhotoModelGenerationPhase) (RetainedPhotoModelGenerationOperation, bool, error) {
	var retained RetainedPhotoModelGenerationOperation
	err := openedStore.DB().QueryRowContext(ctx, `
select operation_state, thread_identifier, turn_identifier
from photo_model_generation_operation
where asset_id=? and input_sha256=? and operation_phase=?`, assetID, inputSHA256, phase).Scan(&retained.State, &retained.ThreadIdentifier, &retained.TurnIdentifier)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedPhotoModelGenerationOperation{}, false, nil
	}
	return retained, err == nil, err
}

func RetainPhotoModelGenerationTurnIdentifier(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, phase PhotoModelGenerationPhase, threadIdentifier, turnIdentifier string, changedAt time.Time) error {
	if len(inputSHA256) != sha256.Size || phase < PhotoModelGenerationPhaseTextExtraction || phase > PhotoModelGenerationPhaseTextVerification || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("photo model generation turn stage is incomplete")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update photo_model_generation_transmission_attempt
set thread_identifier=?, turn_identifier=?
where attempt_id=(
  select attempt_id from photo_model_generation_transmission_attempt
  where asset_id=? and input_sha256=? and operation_phase=? and completed_at is null
  order by attempt_id desc limit 1
)`, threadIdentifier, turnIdentifier, assetID, inputSHA256, phase)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("photo model generation attempt is missing for started turn")
		}
		result, err = tx.ExecContext(ctx, `
update photo_model_generation_operation
set thread_identifier=?, turn_identifier=?, changed_at=?
where asset_id=? and input_sha256=? and operation_phase=? and operation_state=?`, threadIdentifier, turnIdentifier, changedAt.UTC().Format(time.RFC3339Nano), assetID, inputSHA256, phase, PhotoModelGenerationStateTransmissionStarted)
		if err != nil {
			return err
		}
		changed, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("photo model generation operation changed before turn started")
		}
		return nil
	})
}

func RejectRetainedPhotoModelGenerationResponse(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, phase PhotoModelGenerationPhase, threadIdentifier, turnIdentifier, failureDetail string, rejectedAt time.Time) error {
	if len(inputSHA256) != sha256.Size || phase < PhotoModelGenerationPhaseTextExtraction || phase > PhotoModelGenerationPhaseTextVerification || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" || strings.TrimSpace(failureDetail) == "" {
		return errors.New("rejected photo model generation response is incomplete")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		var result sql.Result
		var err error
		switch phase {
		case PhotoModelGenerationPhaseTextExtraction:
			result, err = tx.ExecContext(ctx, `update photo_text_extraction set response_rejected=1 where asset_id=? and input_sha256=? and thread_identifier=? and turn_identifier=? and response_body is not null`, assetID, inputSHA256, threadIdentifier, turnIdentifier)
		case PhotoModelGenerationPhaseSemanticCard:
			result, err = tx.ExecContext(ctx, `update photo_card_generation set response_rejected=1 where asset_id=? and input_sha256=? and thread_identifier=? and turn_identifier=? and response_body is not null`, assetID, inputSHA256, threadIdentifier, turnIdentifier)
		case PhotoModelGenerationPhaseDescriptionRepair:
			result, err = tx.ExecContext(ctx, `update photo_card_generation set descriptions_repair_response_rejected=1 where asset_id=? and input_sha256=? and descriptions_repair_thread_identifier=? and descriptions_repair_turn_identifier=? and descriptions_repair_response_body is not null`, assetID, inputSHA256, threadIdentifier, turnIdentifier)
		case PhotoModelGenerationPhaseTextVerification:
			result, err = tx.ExecContext(ctx, `update photo_text_verification set response_rejected=1 where asset_id=? and input_sha256=? and thread_identifier=? and turn_identifier=? and response_body is not null`, assetID, inputSHA256, threadIdentifier, turnIdentifier)
		}
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("retained photo model generation response changed before rejection")
		}
		rejectedAtText := rejectedAt.UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `update photo_model_generation_transmission_attempt set operation_state=?, failure_detail=?, completed_at=? where attempt_id=(select attempt_id from photo_model_generation_transmission_attempt where asset_id=? and input_sha256=? and operation_phase=? and completed_at is null order by attempt_id desc limit 1)`, PhotoModelGenerationStateFailed, strings.TrimSpace(failureDetail), rejectedAtText, assetID, inputSHA256, phase); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
insert into photo_model_generation_operation(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at)
values (?, ?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id, input_sha256, operation_phase) do update set operation_state=excluded.operation_state, thread_identifier=excluded.thread_identifier, turn_identifier=excluded.turn_identifier, failure_detail=excluded.failure_detail, changed_at=excluded.changed_at`, assetID, inputSHA256, phase, PhotoModelGenerationStateFailed, threadIdentifier, turnIdentifier, strings.TrimSpace(failureDetail), rejectedAtText)
		return err
	})
}

func retainPhotoModelGenerationOperationResponse(ctx context.Context, tx *sql.Tx, assetID PhotoAssetID, inputSHA256 []byte, phase PhotoModelGenerationPhase, threadIdentifier, turnIdentifier string, usage *PhotoModelGenerationTokenUsage, changedAt time.Time) error {
	if len(inputSHA256) != sha256.Size || phase < PhotoModelGenerationPhaseTextExtraction || phase > PhotoModelGenerationPhaseTextVerification || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("photo model generation response stage is incomplete")
	}
	if usage != nil && (usage.InputTokens < 0 || usage.CachedInputTokens < 0 || usage.OutputTokens < 0 || usage.ReasoningOutputTokens < 0 || usage.TotalTokens < 0) {
		return errors.New("photo model generation token usage is invalid")
	}
	var inputTokens, cachedInputTokens, outputTokens, reasoningOutputTokens, totalTokens any
	if usage != nil {
		inputTokens = usage.InputTokens
		cachedInputTokens = usage.CachedInputTokens
		outputTokens = usage.OutputTokens
		reasoningOutputTokens = usage.ReasoningOutputTokens
		totalTokens = usage.TotalTokens
	}
	result, err := tx.ExecContext(ctx, `update photo_model_generation_transmission_attempt set operation_state=?, thread_identifier=?, turn_identifier=?, input_tokens=?, cached_input_tokens=?, output_tokens=?, reasoning_output_tokens=?, total_tokens=? where attempt_id=(select attempt_id from photo_model_generation_transmission_attempt where asset_id=? and input_sha256=? and operation_phase=? and completed_at is null order by attempt_id desc limit 1)`, PhotoModelGenerationStateResponseRetained, threadIdentifier, turnIdentifier, inputTokens, cachedInputTokens, outputTokens, reasoningOutputTokens, totalTokens, assetID, inputSHA256, phase)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New("photo model generation attempt is missing for retained response")
	}
	_, err = tx.ExecContext(ctx, `
insert into photo_model_generation_operation(asset_id, input_sha256, operation_phase, operation_state, thread_identifier, turn_identifier, failure_detail, changed_at)
values (?, ?, ?, ?, ?, ?, '', ?)
on conflict(asset_id, input_sha256, operation_phase) do update set operation_state=excluded.operation_state, thread_identifier=excluded.thread_identifier, turn_identifier=excluded.turn_identifier, failure_detail='', changed_at=excluded.changed_at`, assetID, inputSHA256, phase, PhotoModelGenerationStateResponseRetained, threadIdentifier, turnIdentifier, changedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func LoadCurrentImmutableOriginalFacts(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset) (*mediawire.ImmutableOriginalImageFacts, bool, error) {
	var encoded []byte
	err := openedStore.DB().QueryRowContext(ctx, `select immutable_original_facts_proto from current_photo_media_evidence where asset_id=? and source_fingerprint=?`, asset.AssetID, asset.SourceFingerprint).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	facts := new(mediawire.ImmutableOriginalImageFacts)
	if err := proto.Unmarshal(encoded, facts); err != nil {
		return nil, false, err
	}
	return facts, true, nil
}

func LoadRetainedCurrentPhotoMediaEvidence(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (RetainedCurrentPhotoMediaEvidence, bool, error) {
	var retained RetainedCurrentPhotoMediaEvidence
	var encodedOriginal []byte
	err := openedStore.DB().QueryRowContext(ctx, `
select source_fingerprint, immutable_original_facts_proto, current_rendered_still_sha256,
       current_rendered_still_uniform_type_identifier, current_rendered_still_byte_count,
       current_rendered_still_pixel_width, current_rendered_still_pixel_height,
       current_rendered_still_orientation
from current_photo_media_evidence where asset_id=?`, assetID).Scan(
		&retained.SourceFingerprint, &encodedOriginal, &retained.CurrentRenderedStillSHA256,
		&retained.CurrentRenderedStillMediaType, &retained.CurrentRenderedStillByteCount,
		&retained.CurrentRenderedStillPixelWidth, &retained.CurrentRenderedStillPixelHeight,
		&retained.CurrentRenderedStillOrientation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedCurrentPhotoMediaEvidence{}, false, nil
	}
	if err != nil {
		return RetainedCurrentPhotoMediaEvidence{}, false, err
	}
	retained.ImmutableOriginalImageFacts = new(mediawire.ImmutableOriginalImageFacts)
	if err := proto.Unmarshal(encodedOriginal, retained.ImmutableOriginalImageFacts); err != nil {
		return RetainedCurrentPhotoMediaEvidence{}, false, err
	}
	return retained, true, nil
}

func StoreCurrentPhotoMediaEvidence(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, immutableOriginal *mediawire.ImmutableOriginalImageFacts, currentRenderedStill *mediawire.CurrentRenderedStillLease) error {
	if immutableOriginal == nil || currentRenderedStill == nil || len(immutableOriginal.GetSha256()) != sha256.Size || len(currentRenderedStill.GetSha256()) != sha256.Size {
		return errors.New("current photo media evidence is incomplete")
	}
	encodedOriginal, err := proto.Marshal(immutableOriginal)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `
insert into current_photo_media_evidence(asset_id, source_fingerprint, immutable_original_facts_proto, current_rendered_still_sha256, current_rendered_still_uniform_type_identifier, current_rendered_still_byte_count, current_rendered_still_pixel_width, current_rendered_still_pixel_height, current_rendered_still_orientation)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id) do update set
  source_fingerprint=excluded.source_fingerprint,
  immutable_original_facts_proto=excluded.immutable_original_facts_proto,
  current_rendered_still_sha256=excluded.current_rendered_still_sha256,
  current_rendered_still_uniform_type_identifier=excluded.current_rendered_still_uniform_type_identifier,
  current_rendered_still_byte_count=excluded.current_rendered_still_byte_count,
  current_rendered_still_pixel_width=excluded.current_rendered_still_pixel_width,
  current_rendered_still_pixel_height=excluded.current_rendered_still_pixel_height,
  current_rendered_still_orientation=excluded.current_rendered_still_orientation`, asset.AssetID, asset.SourceFingerprint, encodedOriginal, currentRenderedStill.GetSha256(), currentRenderedStill.GetUniformTypeIdentifier(), currentRenderedStill.GetByteCount(), currentRenderedStill.GetPixelWidth(), currentRenderedStill.GetPixelHeight(), currentRenderedStill.GetImageOrientation())
	return err
}

func SelectPhotoUpdateAssets(ctx context.Context, openedStore *store.Store, knownPlaceConfigurationSHA256 []byte) ([]PhotoUpdateAsset, error) {
	if err := prepareStore(ctx, openedStore); err != nil {
		return nil, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `
select asset.id, asset.source_library_id, seen.source_fingerprint, asset.local_identifier,
       asset.media_type, asset.media_subtypes, asset.creation_date, asset.modification_date,
       asset.width, asset.height, asset.camera_make, asset.camera_model, asset.lens_model,
       asset.focal_length_mm, asset.aperture, asset.shutter_speed, asset.iso,
	       resource.original_filename, resource.uti_projection, resource.file_size,
	       outcome.asset_id, outcome.source_fingerprint, outcome.outcome_kind,
	       current_location.asset_id, current_location.known_place_configuration_sha256,
	       current_location.outcome_proto, capture_location.asset_id
from asset
join crawl_seen_asset seen on seen.asset_id = asset.id and seen.source_library_id = asset.source_library_id
left join photo_update_asset_outcome outcome on outcome.asset_id = asset.id
left join asset_resource resource on resource.asset_id = asset.id and resource.resource_type_projection = 'photo'
left join current_photo_location_evidence current_location on current_location.asset_id = asset.id and current_location.source_fingerprint = seen.source_fingerprint
left join (select distinct asset_id from location_observation) capture_location on capture_location.asset_id = asset.id
	where asset.source_state = 'current'
	  and (outcome.asset_id is null or outcome.source_fingerprint <> seen.source_fingerprint or outcome.outcome_kind = 'media_unavailable' or current_location.asset_id is not null or capture_location.asset_id is not null)
	order by asset.creation_date, asset.id, resource.photos_sqlite_resource_primary_key`)
	if err != nil {
		return nil, fmt.Errorf("select Photos update assets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	assets := []PhotoUpdateAsset{}
	var currentAsset *PhotoUpdateAsset
	var scannedAssetID PhotoAssetID
	for rows.Next() {
		var outcomeAssetID, outcomeSourceFingerprint, outcomeKind sql.NullString
		var currentLocationAssetID, captureLocationAssetID sql.NullString
		var currentLocationKnownConfigurationSHA256, currentLocationOutcomeBytes []byte
		asset, originalFilename, originalUniformTypeIdentifier, originalByteCount, err := scanPhotoUpdateAssetProjection(rows,
			&outcomeAssetID, &outcomeSourceFingerprint, &outcomeKind,
			&currentLocationAssetID, &currentLocationKnownConfigurationSHA256, &currentLocationOutcomeBytes, &captureLocationAssetID,
		)
		if err != nil {
			return nil, fmt.Errorf("read Photos update asset: %w", err)
		}
		if scannedAssetID != asset.AssetID {
			scannedAssetID = asset.AssetID
			currentAsset = nil
			pending := !outcomeAssetID.Valid || outcomeSourceFingerprint.String != string(asset.SourceFingerprint) || outcomeKind.String == string(PhotoUpdateResultMediaUnavailable)
			if asset.MediaType == PhotoMediaKindImage && captureLocationAssetID.Valid {
				locationCurrent := currentLocationAssetID.Valid && bytes.Equal(currentLocationKnownConfigurationSHA256, knownPlaceConfigurationSHA256)
				if locationCurrent {
					composed := new(locationwire.ComposePhotoLocationEvidenceOutcome)
					locationCurrent = proto.Unmarshal(currentLocationOutcomeBytes, composed) == nil && composedPhotoLocationEvidenceIsCurrent(composed)
				}
				pending = pending || !locationCurrent
			}
			if pending {
				assets = append(assets, asset)
				currentAsset = &assets[len(assets)-1]
			}
		}
		appendPhotoUpdateOriginalResource(currentAsset, originalFilename, originalUniformTypeIdentifier, originalByteCount)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Photos update assets: %w", err)
	}
	return assets, nil
}

func LoadPhotoUpdateAsset(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID) (PhotoUpdateAsset, error) {
	if err := prepareStore(ctx, openedStore); err != nil {
		return PhotoUpdateAsset{}, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `
select asset.id, asset.source_library_id, seen.source_fingerprint, asset.local_identifier,
       asset.media_type, asset.media_subtypes, asset.creation_date, asset.modification_date,
       asset.width, asset.height, asset.camera_make, asset.camera_model, asset.lens_model,
       asset.focal_length_mm, asset.aperture, asset.shutter_speed, asset.iso,
       resource.original_filename, resource.uti_projection, resource.file_size
from asset
join crawl_seen_asset seen on seen.asset_id = asset.id and seen.source_library_id = asset.source_library_id
left join asset_resource resource on resource.asset_id = asset.id and resource.resource_type_projection = 'photo'
where asset.id = ? and asset.source_state = 'current'
order by resource.photos_sqlite_resource_primary_key`, assetID)
	if err != nil {
		return PhotoUpdateAsset{}, fmt.Errorf("load Photos update asset: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var loaded PhotoUpdateAsset
	for rows.Next() {
		asset, filename, uniformTypeIdentifier, byteCount, scanErr := scanPhotoUpdateAssetProjection(rows)
		if scanErr != nil {
			return PhotoUpdateAsset{}, fmt.Errorf("read Photos update asset: %w", scanErr)
		}
		if loaded.AssetID == "" {
			loaded = asset
		}
		appendPhotoUpdateOriginalResource(&loaded, filename, uniformTypeIdentifier, byteCount)
	}
	if err := rows.Err(); err != nil {
		return PhotoUpdateAsset{}, fmt.Errorf("read Photos update asset: %w", err)
	}
	if loaded.AssetID == "" {
		return PhotoUpdateAsset{}, fmt.Errorf("Photos asset %q is not indexed as current", assetID)
	}
	return loaded, nil
}

func parseOptionalPhotosTimestamp(value string) (OptionalPhotosTimestamp, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return OptionalPhotosTimestamp{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return OptionalPhotosTimestamp{}, err
	}
	return OptionalPhotosTimestamp{Value: parsed, Present: true}, nil
}

func InvalidatePhotoCardsWithInsufficientLocationEvidence(ctx context.Context, openedStore *store.Store, knownPlaceConfigurationSHA256 []byte) (int, error) {
	rows, err := openedStore.DB().QueryContext(ctx, `
select card.asset_id, current_location.known_place_configuration_sha256, current_location.outcome_proto
from current_photo_card card
left join current_photo_location_evidence current_location on current_location.asset_id=card.asset_id and current_location.source_fingerprint=card.source_fingerprint
where exists (select 1 from location_observation capture_location where capture_location.asset_id=card.asset_id)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	invalidAssetIDs := []string{}
	for rows.Next() {
		var assetID string
		var retainedConfigurationSHA256, encodedOutcome []byte
		if err := rows.Scan(&assetID, &retainedConfigurationSHA256, &encodedOutcome); err != nil {
			return 0, err
		}
		outcome := new(locationwire.ComposePhotoLocationEvidenceOutcome)
		if !bytes.Equal(retainedConfigurationSHA256, knownPlaceConfigurationSHA256) || proto.Unmarshal(encodedOutcome, outcome) != nil || !composedPhotoLocationEvidenceIsCurrent(outcome) {
			invalidAssetIDs = append(invalidAssetIDs, assetID)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(invalidAssetIDs) == 0 {
		return 0, nil
	}
	err = openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		for _, assetID := range invalidAssetIDs {
			if _, err := tx.ExecContext(ctx, `delete from observation_fts where id=?`, "photo-card:"+assetID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `delete from current_photo_card where asset_id=?`, assetID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `delete from photo_update_asset_outcome where asset_id=? and outcome_kind='card_stored'`, assetID); err != nil {
				return err
			}
		}
		return nil
	})
	return len(invalidAssetIDs), err
}

func StorePhotoUpdateOutcome(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, kind PhotoUpdateResultKind, humanDescription string, completedAt time.Time) error {
	switch kind {
	case PhotoUpdateResultCardStored, PhotoUpdateResultMediaUnavailable, PhotoUpdateResultUnsupportedMedia:
	default:
		return fmt.Errorf("unknown Photos update result kind %q", kind)
	}
	if strings.TrimSpace(string(asset.AssetID)) == "" || strings.TrimSpace(string(asset.SourceFingerprint)) == "" || strings.TrimSpace(humanDescription) == "" {
		return errors.New("Photos update terminal outcome is incomplete")
	}
	_, err := openedStore.DB().ExecContext(ctx, `
insert into photo_update_asset_outcome(asset_id, source_fingerprint, outcome_kind, human_description, completed_at)
values (?, ?, ?, ?, ?)
on conflict(asset_id) do update set
  source_fingerprint=excluded.source_fingerprint,
  outcome_kind=excluded.outcome_kind,
  human_description=excluded.human_description,
  completed_at=excluded.completed_at`, asset.AssetID, asset.SourceFingerprint, string(kind), strings.TrimSpace(humanDescription), completedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func RetainPhotoTextExtractionRequest(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, requestText string) error {
	if strings.TrimSpace(string(assetID)) == "" || len(inputSHA256) != sha256.Size || strings.TrimSpace(requestText) == "" {
		return errors.New("photo text extraction request is incomplete")
	}
	_, err := openedStore.DB().ExecContext(ctx, `
insert into photo_text_extraction(asset_id, input_sha256, request_text)
values (?, ?, ?)
on conflict(asset_id) do update set
  input_sha256=excluded.input_sha256,
  request_text=excluded.request_text,
  response_body=null,
  response_rejected=0,
  response_retained_at=null,
  model_identifier=null,
  thread_identifier=null,
  turn_identifier=null
where photo_text_extraction.input_sha256 <> excluded.input_sha256`, assetID, inputSHA256, requestText)
	return err
}

func LoadRetainedPhotoTextExtraction(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte) (RetainedPhotoTextExtraction, bool, error) {
	var retained RetainedPhotoTextExtraction
	err := openedStore.DB().QueryRowContext(ctx, `
select input_sha256, request_text, coalesce(response_body, x''), response_rejected, coalesce(model_identifier, ''), coalesce(thread_identifier, ''), coalesce(turn_identifier, '')
from photo_text_extraction where asset_id=? and input_sha256=?`, assetID, inputSHA256).Scan(&retained.InputSHA256, &retained.RequestText, &retained.ResponseBody, &retained.ResponseRejected, &retained.ModelIdentifier, &retained.ThreadIdentifier, &retained.TurnIdentifier)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedPhotoTextExtraction{}, false, nil
	}
	return retained, err == nil, err
}

func RetainPhotoTextExtractionResponse(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, threadIdentifier, turnIdentifier string, response []byte, usage *PhotoModelGenerationTokenUsage, retainedAt time.Time) error {
	if len(response) == 0 || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("photo text extraction response is empty")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update photo_text_extraction
set response_body=?, response_rejected=0, response_retained_at=?, model_identifier='gpt-5.6-luna', thread_identifier=?, turn_identifier=?
where asset_id=? and input_sha256=?`, response, retainedAt.UTC().Format(time.RFC3339Nano), threadIdentifier, turnIdentifier, assetID, inputSHA256)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("photo text extraction request changed before response retention")
		}
		return retainPhotoModelGenerationOperationResponse(ctx, tx, assetID, inputSHA256, PhotoModelGenerationPhaseTextExtraction, threadIdentifier, turnIdentifier, usage, retainedAt)
	})
}

func RetainPhotoTextVerificationRequest(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, requestText string) error {
	if strings.TrimSpace(string(assetID)) == "" || len(inputSHA256) != sha256.Size || strings.TrimSpace(requestText) == "" {
		return errors.New("photo text verification request is incomplete")
	}
	_, err := openedStore.DB().ExecContext(ctx, `
insert into photo_text_verification(asset_id, input_sha256, request_text)
values (?, ?, ?)
on conflict(asset_id) do update set
  input_sha256=excluded.input_sha256,
  request_text=excluded.request_text,
  response_body=null,
  response_rejected=0,
  model_identifier=null,
  thread_identifier=null,
  turn_identifier=null,
  response_retained_at=null
where photo_text_verification.input_sha256 <> excluded.input_sha256`, assetID, inputSHA256, requestText)
	return err
}

func LoadRetainedPhotoTextVerification(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte) (RetainedPhotoTextVerification, bool, error) {
	var retained RetainedPhotoTextVerification
	err := openedStore.DB().QueryRowContext(ctx, `
select input_sha256, request_text, coalesce(response_body, x''), response_rejected, coalesce(model_identifier, ''), coalesce(thread_identifier, ''), coalesce(turn_identifier, ''), coalesce(response_retained_at, '')
from photo_text_verification where asset_id=? and input_sha256=?`, assetID, inputSHA256).Scan(&retained.InputSHA256, &retained.RequestText, &retained.ResponseBody, &retained.ResponseRejected, &retained.ModelIdentifier, &retained.ThreadIdentifier, &retained.TurnIdentifier, &retained.ResponseRetainedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedPhotoTextVerification{}, false, nil
	}
	return retained, err == nil, err
}

func RetainPhotoTextVerificationResponse(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, threadIdentifier, turnIdentifier string, response []byte, usage *PhotoModelGenerationTokenUsage, retainedAt time.Time) error {
	if len(response) == 0 || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("photo text verification response is empty")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update photo_text_verification
set response_body=?, response_rejected=0, model_identifier='gpt-5.6-luna', thread_identifier=?, turn_identifier=?, response_retained_at=?
where asset_id=? and input_sha256=?`, response, threadIdentifier, turnIdentifier, retainedAt.UTC().Format(time.RFC3339Nano), assetID, inputSHA256)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("photo text verification request changed before response retention")
		}
		return retainPhotoModelGenerationOperationResponse(ctx, tx, assetID, inputSHA256, PhotoModelGenerationPhaseTextVerification, threadIdentifier, turnIdentifier, usage, retainedAt)
	})
}

func RejectRetainedPhotoTextVerificationResponse(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, threadIdentifier, turnIdentifier, failureDetail string, rejectedAt time.Time) error {
	return RejectRetainedPhotoModelGenerationResponse(ctx, openedStore, assetID, inputSHA256, PhotoModelGenerationPhaseTextVerification, threadIdentifier, turnIdentifier, failureDetail, rejectedAt)
}

func RetainPhotoCardGenerationRequest(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, requestText string) error {
	if strings.TrimSpace(string(assetID)) == "" || len(inputSHA256) != sha256.Size || strings.TrimSpace(requestText) == "" {
		return errors.New("PhotoCard generation request is incomplete")
	}
	_, err := openedStore.DB().ExecContext(ctx, `
insert into photo_card_generation(asset_id, input_sha256, request_text)
values (?, ?, ?)
on conflict(asset_id) do update set
  input_sha256=excluded.input_sha256,
  request_text=excluded.request_text,
  response_body=null,
  response_rejected=0,
  response_retained_at=null,
  model_identifier=null,
  thread_identifier=null,
  turn_identifier=null,
  descriptions_repair_request_text=null,
  descriptions_repair_response_body=null,
  descriptions_repair_response_rejected=0,
  descriptions_repair_response_retained_at=null,
  descriptions_repair_thread_identifier=null,
  descriptions_repair_turn_identifier=null,
  completed_at=null
where photo_card_generation.input_sha256 <> excluded.input_sha256`, assetID, inputSHA256, requestText)
	return err
}

func LoadRetainedPhotoCardGeneration(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte) (RetainedPhotoCardGeneration, bool, error) {
	var retained RetainedPhotoCardGeneration
	err := openedStore.DB().QueryRowContext(ctx, `
select input_sha256, request_text, coalesce(response_body, x''), response_rejected, coalesce(model_identifier, ''), coalesce(thread_identifier, ''), coalesce(turn_identifier, ''),
       coalesce(descriptions_repair_request_text, ''), coalesce(descriptions_repair_response_body, x''), descriptions_repair_response_rejected,
       coalesce(descriptions_repair_thread_identifier, ''), coalesce(descriptions_repair_turn_identifier, '')
from photo_card_generation where asset_id = ? and input_sha256 = ?`, assetID, inputSHA256).Scan(&retained.InputSHA256, &retained.RequestText, &retained.ResponseBody, &retained.ResponseRejected, &retained.ModelIdentifier, &retained.ThreadIdentifier, &retained.TurnIdentifier, &retained.DescriptionsRepairRequestText, &retained.DescriptionsRepairResponseBody, &retained.DescriptionsRepairResponseRejected, &retained.DescriptionsRepairThreadID, &retained.DescriptionsRepairTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedPhotoCardGeneration{}, false, nil
	}
	return retained, err == nil, err
}

func RetainPhotoCardGenerationResponse(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, threadIdentifier, turnIdentifier string, response []byte, usage *PhotoModelGenerationTokenUsage, retainedAt time.Time) error {
	if len(response) == 0 || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("PhotoCard generation response is empty")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update photo_card_generation
set response_body=?, response_rejected=0, response_retained_at=?, model_identifier='gpt-5.6-luna', thread_identifier=?, turn_identifier=?
where asset_id=? and input_sha256=?`, response, retainedAt.UTC().Format(time.RFC3339Nano), threadIdentifier, turnIdentifier, assetID, inputSHA256)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("PhotoCard generation request changed before response retention")
		}
		return retainPhotoModelGenerationOperationResponse(ctx, tx, assetID, inputSHA256, PhotoModelGenerationPhaseSemanticCard, threadIdentifier, turnIdentifier, usage, retainedAt)
	})
}

func RetainPhotoCardDescriptionsRepair(ctx context.Context, openedStore *store.Store, assetID PhotoAssetID, inputSHA256 []byte, requestText, threadIdentifier, turnIdentifier string, response []byte, usage *PhotoModelGenerationTokenUsage, retainedAt time.Time) error {
	if strings.TrimSpace(requestText) == "" || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" || len(response) == 0 {
		return errors.New("PhotoCard descriptions repair is incomplete")
	}
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
update photo_card_generation
set descriptions_repair_request_text=?, descriptions_repair_response_body=?, descriptions_repair_response_rejected=0, descriptions_repair_response_retained_at=?,
    descriptions_repair_thread_identifier=?, descriptions_repair_turn_identifier=?
where asset_id=? and input_sha256=?`, requestText, response, retainedAt.UTC().Format(time.RFC3339Nano), threadIdentifier, turnIdentifier, assetID, inputSHA256)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return errors.New("PhotoCard generation changed before descriptions repair retention")
		}
		return retainPhotoModelGenerationOperationResponse(ctx, tx, assetID, inputSHA256, PhotoModelGenerationPhaseDescriptionRepair, threadIdentifier, turnIdentifier, usage, retainedAt)
	})
}

func StoreCurrentPhotoCard(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, inputSHA256, currentSHA256, locationSHA256 []byte, locationEvidence *locationwire.ComposePhotoLocationEvidenceOutcome, card *cardwire.PhotoCard, completedAt time.Time) error {
	if card == nil || card.Descriptions == nil || len(inputSHA256) != sha256.Size || len(currentSHA256) != sha256.Size {
		return errors.New("current PhotoCard is incomplete")
	}
	encodedCard, err := proto.Marshal(card)
	if err != nil {
		return err
	}
	photographedPlaceText := photoCardPlaceText(card)
	searchBody := photoCardSearchBody(card, photographedPlaceText, currentPhotoCaptureLocationProjectionFromEvidence(locationEvidence).SearchText)
	return openedStore.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
insert into current_photo_card(asset_id, source_fingerprint, input_sha256, current_rendered_still_sha256, location_evidence_sha256, card_proto, concise_description, detailed_description, photographed_place_text, completed_at)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id) do update set
  source_fingerprint=excluded.source_fingerprint,
  input_sha256=excluded.input_sha256,
  current_rendered_still_sha256=excluded.current_rendered_still_sha256,
  location_evidence_sha256=excluded.location_evidence_sha256,
  card_proto=excluded.card_proto,
  concise_description=excluded.concise_description,
  detailed_description=excluded.detailed_description,
  photographed_place_text=excluded.photographed_place_text,
  completed_at=excluded.completed_at`, asset.AssetID, asset.SourceFingerprint, inputSHA256, currentSHA256, nullableBytes(locationSHA256), encodedCard, card.Descriptions.ConciseDescription, card.Descriptions.DetailedDescription, photographedPlaceText, completedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from observation_fts where id = ?`, "photo-card:"+asset.AssetID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, ?, ?)`, "photo-card:"+asset.AssetID, asset.AssetID, card.Descriptions.ConciseDescription, searchBody); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `update photo_card_generation set completed_at=? where asset_id=? and input_sha256=?`, completedAt.UTC().Format(time.RFC3339Nano), asset.AssetID, inputSHA256); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into photo_update_asset_outcome(asset_id, source_fingerprint, outcome_kind, human_description, completed_at)
values (?, ?, 'card_stored', 'Photo card stored', ?)
on conflict(asset_id) do update set source_fingerprint=excluded.source_fingerprint, outcome_kind=excluded.outcome_kind, human_description=excluded.human_description, completed_at=excluded.completed_at`, asset.AssetID, asset.SourceFingerprint, completedAt.UTC().Format(time.RFC3339Nano))
		return err
	})
}

func LoadCurrentPhotoLocationEvidence(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte) (*locationwire.ComposePhotoLocationEvidenceOutcome, bool, error) {
	var encoded []byte
	err := openedStore.DB().QueryRowContext(ctx, `select outcome_proto from current_photo_location_evidence where asset_id=? and source_fingerprint=? and known_place_configuration_sha256=?`, asset.AssetID, asset.SourceFingerprint, knownPlaceConfigurationSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	outcome := new(locationwire.ComposePhotoLocationEvidenceOutcome)
	if err := proto.Unmarshal(encoded, outcome); err != nil {
		return nil, false, nil
	}
	if !composedPhotoLocationEvidenceIsCurrent(outcome) {
		return nil, false, nil
	}
	return outcome, true, nil
}

func composedPhotoLocationEvidenceIsCurrent(outcome *locationwire.ComposePhotoLocationEvidenceOutcome) bool {
	if outcome.GetState() != locationwire.OperationState_OPERATION_STATE_SUCCEEDED {
		return false
	}
	reusable := func(status *locationwire.LocationOperationTerminalStatus, allowKnownPlaceSkip bool) bool {
		switch status.GetState() {
		case locationwire.OperationState_OPERATION_STATE_SUCCEEDED, locationwire.OperationState_OPERATION_STATE_NO_RESULT:
			return status.GetFailure() == nil
		case locationwire.OperationState_OPERATION_STATE_SKIPPED_KNOWN_PLACE:
			return allowKnownPlaceSkip && status.GetFailure() == nil
		default:
			return false
		}
	}
	return reusable(outcome.GetKnownPlaceMatchStatus(), false) && reusable(outcome.GetAppleReverseGeocodingStatus(), false) && reusable(outcome.GetAppleNearbyPlaceStatus(), true) && reusable(outcome.GetGeoapifyPhotographedPlaceCandidateEvidenceStatus(), true)
}

func StoreCurrentPhotoLocationEvidence(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte, outcome *locationwire.ComposePhotoLocationEvidenceOutcome) error {
	if outcome == nil || outcome.Request == nil || outcome.Request.AssetId != string(asset.AssetID) || strings.TrimSpace(string(asset.SourceFingerprint)) == "" || len(knownPlaceConfigurationSHA256) != sha256.Size {
		return errors.New("current photo location evidence is incomplete")
	}
	encoded, err := proto.Marshal(outcome)
	if err != nil {
		return err
	}
	_, err = openedStore.DB().ExecContext(ctx, `
insert into current_photo_location_evidence(asset_id, source_fingerprint, known_place_configuration_sha256, outcome_proto)
values (?, ?, ?, ?)
on conflict(asset_id) do update set source_fingerprint=excluded.source_fingerprint, known_place_configuration_sha256=excluded.known_place_configuration_sha256, outcome_proto=excluded.outcome_proto`, asset.AssetID, asset.SourceFingerprint, knownPlaceConfigurationSHA256, encoded)
	return err
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func photoCardPlaceText(card *cardwire.PhotoCard) string {
	if card == nil || card.PhotographedPlace == nil {
		return ""
	}
	names := []string{}
	for _, candidate := range card.PhotographedPlace.SelectedSuppliedCandidates {
		if candidate != nil && strings.TrimSpace(candidate.HumanName) != "" {
			names = append(names, strings.TrimSpace(candidate.HumanName))
		}
	}
	for _, inferred := range card.PhotographedPlace.ImageInferredPlaces {
		if inferred != nil && strings.TrimSpace(inferred.HumanName) != "" {
			names = append(names, strings.TrimSpace(inferred.HumanName))
		}
	}
	return strings.Join(names, ", ")
}

func photoCardSearchBody(card *cardwire.PhotoCard, photographedPlaceText, captureLocationText string) string {
	parts := []string{card.Descriptions.ConciseDescription, card.Descriptions.DetailedDescription, card.PrimaryDepictedSubject.GetHumanName(), card.VisibleContent.GetScene(), photographedPlaceText, captureLocationText}
	parts = append(parts, card.VisibleContent.GetImportantObjects()...)
	parts = append(parts, card.VisibleContent.GetVisibleActions()...)
	for _, person := range card.VisibleContent.GetPeople() {
		parts = append(parts, person.GetVisiblePositionOrRole(), person.GetVisibleAppearance(), person.GetVisibleActionOrPose())
	}
	parts = append(parts, card.SearchableFacts...)
	for _, region := range card.OpticalCharacterRecognition.GetRegionsInReadingOrder() {
		for _, line := range region.GetLinesInReadingOrder() {
			parts = append(parts, line.GetTranscribedText())
		}
	}
	for _, field := range card.OpticalCharacterRecognition.GetKeyValueFields() {
		parts = append(parts, field.GetKey(), field.GetValue(), field.GetVisibleSource())
	}
	for _, table := range card.OpticalCharacterRecognition.GetTables() {
		parts = append(parts, table.GetVisibleSource())
		for _, row := range table.GetRowsInReadingOrder() {
			parts = append(parts, row.GetCellsInReadingOrder()...)
		}
	}
	return strings.Join(parts, "\n")
}
