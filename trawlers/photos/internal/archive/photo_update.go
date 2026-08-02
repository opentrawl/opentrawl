package archive

import (
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
	AssetID           string
	SourceLibraryID   string
	SourceFingerprint string
	LocalIdentifier   string
	MediaType         string
	MediaSubtypes     string
	CreationTime      string
	ModificationTime  string
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

type PhotoUpdateOriginalResource struct {
	Filename              string
	UniformTypeIdentifier string
	IndexedByteCount      int64
}

type PhotoUpdateResultKind string

const (
	PhotoUpdateResultCardStored       PhotoUpdateResultKind = "card_stored"
	PhotoUpdateResultMediaUnavailable PhotoUpdateResultKind = "media_unavailable"
	PhotoUpdateResultUnsupportedMedia PhotoUpdateResultKind = "unsupported_media"
)

type RetainedPhotoCardGeneration struct {
	InputSHA256                    []byte
	RequestText                    string
	ResponseBody                   []byte
	ModelIdentifier                string
	ThreadIdentifier               string
	TurnIdentifier                 string
	DescriptionsRepairRequestText  string
	DescriptionsRepairResponseBody []byte
	DescriptionsRepairThreadID     string
	DescriptionsRepairTurnID       string
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

func SelectPhotoUpdateAssets(ctx context.Context, openedStore *store.Store) ([]PhotoUpdateAsset, error) {
	if err := prepareStore(ctx, openedStore); err != nil {
		return nil, err
	}
	rows, err := openedStore.DB().QueryContext(ctx, `
select asset.id, asset.source_library_id, seen.source_fingerprint, asset.local_identifier,
       asset.media_type, asset.media_subtypes, asset.creation_date, asset.modification_date,
       asset.width, asset.height, asset.camera_make, asset.camera_model, asset.lens_model,
       asset.focal_length_mm, asset.aperture, asset.shutter_speed, asset.iso,
       resource.original_filename, resource.uti_projection, resource.file_size
from asset
join crawl_seen_asset seen on seen.asset_id = asset.id and seen.source_library_id = asset.source_library_id
left join photo_update_asset_outcome outcome on outcome.asset_id = asset.id
left join asset_resource resource on resource.asset_id = asset.id and resource.resource_type_projection = 'photo'
where asset.source_state = 'current'
  and (outcome.asset_id is null or outcome.source_fingerprint <> seen.source_fingerprint or outcome.outcome_kind = 'media_unavailable')
order by asset.creation_date, asset.id, resource.photos_sqlite_resource_primary_key`)
	if err != nil {
		return nil, fmt.Errorf("select Photos update assets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	assets := []PhotoUpdateAsset{}
	var currentAsset *PhotoUpdateAsset
	for rows.Next() {
		var asset PhotoUpdateAsset
		var originalFilename, originalUniformTypeIdentifier sql.NullString
		var originalByteCount sql.NullInt64
		if err := rows.Scan(
			&asset.AssetID, &asset.SourceLibraryID, &asset.SourceFingerprint, &asset.LocalIdentifier,
			&asset.MediaType, &asset.MediaSubtypes, &asset.CreationTime, &asset.ModificationTime,
			&asset.PixelWidth, &asset.PixelHeight, &asset.CameraMake, &asset.CameraModel, &asset.LensModel,
			&asset.FocalLengthMM, &asset.Aperture, &asset.ExposureSeconds, &asset.ISO,
			&originalFilename, &originalUniformTypeIdentifier, &originalByteCount,
		); err != nil {
			return nil, fmt.Errorf("read Photos update asset: %w", err)
		}
		if currentAsset == nil || currentAsset.AssetID != asset.AssetID {
			assets = append(assets, asset)
			currentAsset = &assets[len(assets)-1]
		}
		if originalFilename.Valid {
			currentAsset.OriginalResources = append(currentAsset.OriginalResources, PhotoUpdateOriginalResource{
				Filename:              originalFilename.String,
				UniformTypeIdentifier: originalUniformTypeIdentifier.String,
				IndexedByteCount:      originalByteCount.Int64,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read Photos update assets: %w", err)
	}
	return assets, nil
}

func StorePhotoUpdateOutcome(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, kind PhotoUpdateResultKind, humanDescription string, completedAt time.Time) error {
	switch kind {
	case PhotoUpdateResultCardStored, PhotoUpdateResultMediaUnavailable, PhotoUpdateResultUnsupportedMedia:
	default:
		return fmt.Errorf("unknown Photos update result kind %q", kind)
	}
	if strings.TrimSpace(asset.AssetID) == "" || strings.TrimSpace(asset.SourceFingerprint) == "" || strings.TrimSpace(humanDescription) == "" {
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

func RetainPhotoCardGenerationRequest(ctx context.Context, openedStore *store.Store, assetID string, inputSHA256 []byte, requestText string) error {
	if strings.TrimSpace(assetID) == "" || len(inputSHA256) != sha256.Size || strings.TrimSpace(requestText) == "" {
		return errors.New("PhotoCard generation request is incomplete")
	}
	_, err := openedStore.DB().ExecContext(ctx, `
insert into photo_card_generation(asset_id, input_sha256, request_text)
values (?, ?, ?)
on conflict(asset_id) do update set
  input_sha256=excluded.input_sha256,
  request_text=excluded.request_text,
  response_body=null,
  response_retained_at=null,
  model_identifier=null,
  thread_identifier=null,
  turn_identifier=null,
  descriptions_repair_request_text=null,
  descriptions_repair_response_body=null,
  descriptions_repair_response_retained_at=null,
  descriptions_repair_thread_identifier=null,
  descriptions_repair_turn_identifier=null,
  completed_at=null,
  failure_text=''
where photo_card_generation.input_sha256 <> excluded.input_sha256`, assetID, inputSHA256, requestText)
	return err
}

func LoadRetainedPhotoCardGeneration(ctx context.Context, openedStore *store.Store, assetID string, inputSHA256 []byte) (RetainedPhotoCardGeneration, bool, error) {
	var retained RetainedPhotoCardGeneration
	err := openedStore.DB().QueryRowContext(ctx, `
select input_sha256, request_text, coalesce(response_body, x''), coalesce(model_identifier, ''), coalesce(thread_identifier, ''), coalesce(turn_identifier, ''),
       coalesce(descriptions_repair_request_text, ''), coalesce(descriptions_repair_response_body, x''),
       coalesce(descriptions_repair_thread_identifier, ''), coalesce(descriptions_repair_turn_identifier, '')
from photo_card_generation where asset_id = ? and input_sha256 = ?`, assetID, inputSHA256).Scan(&retained.InputSHA256, &retained.RequestText, &retained.ResponseBody, &retained.ModelIdentifier, &retained.ThreadIdentifier, &retained.TurnIdentifier, &retained.DescriptionsRepairRequestText, &retained.DescriptionsRepairResponseBody, &retained.DescriptionsRepairThreadID, &retained.DescriptionsRepairTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return RetainedPhotoCardGeneration{}, false, nil
	}
	return retained, err == nil, err
}

func RetainPhotoCardGenerationResponse(ctx context.Context, openedStore *store.Store, assetID string, inputSHA256 []byte, threadIdentifier, turnIdentifier string, response []byte, retainedAt time.Time) error {
	if len(response) == 0 || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" {
		return errors.New("PhotoCard generation response is empty")
	}
	result, err := openedStore.DB().ExecContext(ctx, `
update photo_card_generation
set response_body=?, response_retained_at=?, model_identifier='gpt-5.6-luna', thread_identifier=?, turn_identifier=?, failure_text=''
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
	return nil
}

func RetainPhotoCardDescriptionsRepair(ctx context.Context, openedStore *store.Store, assetID string, inputSHA256 []byte, requestText, threadIdentifier, turnIdentifier string, response []byte, retainedAt time.Time) error {
	if strings.TrimSpace(requestText) == "" || strings.TrimSpace(threadIdentifier) == "" || strings.TrimSpace(turnIdentifier) == "" || len(response) == 0 {
		return errors.New("PhotoCard descriptions repair is incomplete")
	}
	result, err := openedStore.DB().ExecContext(ctx, `
update photo_card_generation
set descriptions_repair_request_text=?, descriptions_repair_response_body=?, descriptions_repair_response_retained_at=?,
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
	return nil
}

func StoreCurrentPhotoCard(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, inputSHA256, currentSHA256, locationSHA256 []byte, card *cardwire.PhotoCard, completedAt time.Time) error {
	if card == nil || card.Descriptions == nil || len(inputSHA256) != sha256.Size || len(currentSHA256) != sha256.Size {
		return errors.New("current PhotoCard is incomplete")
	}
	encodedCard, err := proto.Marshal(card)
	if err != nil {
		return err
	}
	photographedPlaceText := photoCardPlaceText(card)
	searchBody := photoCardSearchBody(card, photographedPlaceText)
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
		return nil, false, err
	}
	return outcome, true, nil
}

func StoreCurrentPhotoLocationEvidence(ctx context.Context, openedStore *store.Store, asset PhotoUpdateAsset, knownPlaceConfigurationSHA256 []byte, outcome *locationwire.ComposePhotoLocationEvidenceOutcome) error {
	if outcome == nil || outcome.Request == nil || outcome.Request.AssetId != asset.AssetID || strings.TrimSpace(asset.SourceFingerprint) == "" || len(knownPlaceConfigurationSHA256) != sha256.Size {
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

func photoCardSearchBody(card *cardwire.PhotoCard, photographedPlaceText string) string {
	parts := []string{card.Descriptions.ConciseDescription, card.Descriptions.DetailedDescription, card.PrimaryDepictedSubject.GetHumanName(), card.VisibleContent.GetScene(), photographedPlaceText}
	parts = append(parts, card.VisibleContent.GetImportantObjects()...)
	parts = append(parts, card.VisibleContent.GetVisibleActions()...)
	parts = append(parts, card.SearchableFacts...)
	for _, region := range card.OpticalCharacterRecognition.GetRegionsInReadingOrder() {
		for _, line := range region.GetLinesInReadingOrder() {
			parts = append(parts, line.GetTranscribedText())
		}
	}
	return strings.Join(parts, "\n")
}
