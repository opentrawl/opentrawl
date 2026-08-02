package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer func() { _ = db.Close() }()
	return open(ctx, db, rowID)
}

// OpenWithStore opens a record from the runner-owned read-only Photos store.
func OpenWithStore(ctx context.Context, db *store.Store, rowID string) (OpenResult, error) {
	if err := validateReadStore(ctx, db); err != nil {
		return OpenResult{}, err
	}
	return open(ctx, db, rowID)
}

func open(ctx context.Context, db *store.Store, rowID string) (OpenResult, error) {
	rowID = AssetID(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, creation_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier,
       camera_make, camera_model, lens_model, focal_length_mm, focal_length_35mm, aperture, shutter_speed, iso,
       source_state, coalesce(first_missing_at, '') as first_missing_at, coalesce(source_deleted_at, '') as source_deleted_at,
       seen.source_fingerprint
from asset
join crawl_seen_asset seen on seen.asset_id=asset.id and seen.source_library_id=asset.source_library_id
where asset.id = ?
`, rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenResult{}, fmt.Errorf("asset not found: %s", rowID)
	}
	if err != nil {
		return OpenResult{}, err
	}
	resources, err := rows(ctx, db.DB(), `
select resource_type_projection as resource_type,
       uti_projection as uti,
       availability_projection as availability,
       original_filename, file_size, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type_projection, original_filename
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	locations, err := rows(ctx, db.DB(), `
select id, latitude, longitude, altitude, horizontal_accuracy
from location_observation
where asset_id = ?
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	albums, err := rows(ctx, db.DB(), `
select album_title, album_kind
from album_membership
where asset_id = ?
order by album_title, album_kind
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	result := newOpenResult(asset, resources, locations, albums, nil, nil)
	knownPlaceConfigurationSHA256, err := KnownPlaceConfigurationSHA256(ctx, db)
	if err != nil {
		return OpenResult{}, err
	}
	currentLocationEvidence, found, err := LoadCurrentPhotoLocationEvidence(ctx, db, PhotoUpdateAsset{
		AssetID:           PhotoAssetID(rowID),
		SourceFingerprint: PhotoSourceFingerprint(rowString(asset, "source_fingerprint")),
	}, knownPlaceConfigurationSHA256)
	if err != nil {
		return OpenResult{}, err
	}
	if found {
		locationProjection := currentPhotoCaptureLocationProjectionFromEvidence(currentLocationEvidence)
		result.Mechanical.Place = locationProjection.CaptureLocation
		result.Mechanical.KnownPlace = locationProjection.KnownPlace
	}
	if outcomeDescription, found, err := openPhotoUpdateOutcome(ctx, db, rowID); err != nil {
		return OpenResult{}, err
	} else if found {
		result.Mechanical.Flags = append(result.Mechanical.Flags, outcomeDescription)
	}
	if model, found, err := openTypedCard(ctx, db, rowID); err != nil {
		return OpenResult{}, err
	} else if found {
		result.Model = model
	}
	return result, nil
}

func openPhotoUpdateOutcome(ctx context.Context, db *store.Store, assetID string) (string, bool, error) {
	var outcomeKind, humanDescription string
	err := db.DB().QueryRowContext(ctx, `select outcome_kind, human_description from photo_update_asset_outcome where asset_id=?`, assetID).Scan(&outcomeKind, &humanDescription)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if outcomeKind == string(PhotoUpdateResultCardStored) {
		return "", false, nil
	}
	return strings.TrimSpace(humanDescription), strings.TrimSpace(humanDescription) != "", nil
}

func openTypedCard(ctx context.Context, db *store.Store, assetID string) (OpenModel, bool, error) {
	var cardBytes []byte
	var photographedPlaceText string
	err := db.DB().QueryRowContext(ctx, `
select card_proto, photographed_place_text
from current_photo_card
where asset_id = ?`, assetID).Scan(&cardBytes, &photographedPlaceText)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenModel{}, false, nil
	}
	if err != nil {
		return OpenModel{}, false, fmt.Errorf("read photo card: %w", err)
	}
	card := new(cardwire.PhotoCard)
	if err := proto.Unmarshal(cardBytes, card); err != nil {
		return OpenModel{}, false, fmt.Errorf("decode photo card: %w", err)
	}
	ocrLines := []string{}
	for _, region := range card.GetOpticalCharacterRecognition().GetRegionsInReadingOrder() {
		for _, line := range region.GetLinesInReadingOrder() {
			ocrLines = append(ocrLines, line.GetTranscribedText())
		}
	}
	for _, field := range card.GetOpticalCharacterRecognition().GetKeyValueFields() {
		ocrLines = append(ocrLines, field.GetKey()+": "+field.GetValue()+" ("+field.GetVisibleSource()+")")
	}
	for _, table := range card.GetOpticalCharacterRecognition().GetTables() {
		ocrLines = append(ocrLines, table.GetVisibleSource())
		for _, row := range table.GetRowsInReadingOrder() {
			ocrLines = append(ocrLines, strings.Join(row.GetCellsInReadingOrder(), " | "))
		}
	}
	uncertainties := []string{}
	for _, uncertainty := range card.GetUncertainties() {
		if uncertainty != nil {
			uncertainties = append(uncertainties, uncertainty.GetSubject()+": "+uncertainty.GetExplanation())
		}
	}
	locationKind := strings.ToLower(strings.TrimPrefix(card.GetPhotographedPlace().GetCertainty().String(), "PHOTOGRAPHED_PLACE_CERTAINTY_"))
	visibleFacts := []string{card.GetPrimaryDepictedSubject().GetHumanName(), card.GetVisibleContent().GetScene()}
	for _, person := range card.GetVisibleContent().GetPeople() {
		visibleFacts = append(visibleFacts, strings.Join(compactOpenText([]string{person.GetVisiblePositionOrRole(), person.GetVisibleAppearance(), person.GetVisibleActionOrPose()}), " — "))
	}
	visibleFacts = append(visibleFacts, card.GetVisibleContent().GetImportantObjects()...)
	visibleFacts = append(visibleFacts, card.GetVisibleContent().GetVisibleActions()...)
	model := OpenModel{
		ModelID:        "gpt-5.6-luna",
		Summary:        card.GetDescriptions().GetConciseDescription(),
		Description:    card.GetDescriptions().GetDetailedDescription(),
		OCRText:        strings.Join(ocrLines, "\n"),
		VisibleContent: strings.Join(compactOpenText(visibleFacts), "\n"),
		Uncertainties:  uncertainties,
		Location:       &OpenModelLocation{Name: photographedPlaceText, Kind: locationKind, Confidence: locationKind, Reason: card.GetPhotographedPlace().GetExplanation()},
	}
	return model, true, nil
}

func compactOpenText(values []string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}
