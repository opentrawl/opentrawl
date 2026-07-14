package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	cardInputAuditStopSnapshotIncomplete  = "snapshot_incomplete"
	cardInputAuditStopProhibited          = "first_card_prohibited"
	cardInputAuditStopSourceNotCurrent    = "source_not_current"
	cardInputAuditStopUnsupportedMedia    = "unsupported_media"
	cardInputAuditStopMissingMetadata     = "metadata_not_checked"
	cardInputAuditStopMissingCurrentStill = "full_current_not_checked"
	cardInputAuditStopMissingPlace        = "place_evidence_not_checked"
	cardInputAuditSchemaVersion           = 13
)

// CardInputAuditInventoryOptions identifies one immutable source snapshot.
// The archive is opened read-only and no artifact root is touched here.
type CardInputAuditInventoryOptions struct {
	ArchivePath     string
	SourceLibraryID string
}

type CardInputAuditInventory struct {
	SourceLibraryID string                       `json:"source_library_id"`
	SnapshotID      string                       `json:"snapshot_id"`
	Complete        bool                         `json:"complete"`
	Assets          []CardInputAuditInventoryRow `json:"assets"`
}

// CardInputAuditInventoryRow contains only structural data. In particular it
// never reads image bytes, cached artifacts or a rendered request.
type CardInputAuditInventoryRow struct {
	AssetID       string   `json:"asset_id"`
	SourceState   string   `json:"source_state"`
	QueueState    string   `json:"queue_state"`
	MediaType     string   `json:"media_type"`
	ResourceRoles []string `json:"resource_roles"`
	HasMetadata   bool     `json:"has_metadata"`
	HasLocation   bool     `json:"has_location"`
	AlbumCount    int      `json:"album_count"`
	Favorite      bool     `json:"favorite"`
	Hidden        bool     `json:"hidden"`
	BurstMember   bool     `json:"burst_member"`
	Eligibility   string   `json:"eligibility"`
	StopReasons   []string `json:"stop_reasons,omitempty"`
}

type CardInputAuditInspectOptions struct {
	CardInputAuditInventoryOptions
	CacheDir string
	AssetIDs []string
	Model    string
	ModelURL string
}

type CardInputAuditInspection struct {
	AssetID          string          `json:"asset_id"`
	StopReason       string          `json:"stop_reason,omitempty"`
	Preflight        any             `json:"preflight"`
	CardInput        json.RawMessage `json:"card_input,omitempty"`
	CardInputWire    string          `json:"card_input_protobuf_base64,omitempty"`
	RenderedRequest  json.RawMessage `json:"rendered_request,omitempty"`
	RenderedRoute    string          `json:"rendered_route,omitempty"`
	RenderedModel    string          `json:"rendered_model,omitempty"`
	CurrentStillPath string          `json:"-"`
}

func ReadCardInputAuditInventory(ctx context.Context, options CardInputAuditInventoryOptions) (CardInputAuditInventory, error) {
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return CardInputAuditInventory{}, err
	}
	defer db.Close()
	return readCardInputAuditInventory(ctx, db.DB(), options.SourceLibraryID)
}

func readCardInputAuditInventory(ctx context.Context, db *sql.DB, sourceLibraryID string) (CardInputAuditInventory, error) {
	snapshotID, complete, err := cardInputAuditSnapshot(ctx, db, sourceLibraryID)
	if err != nil {
		return CardInputAuditInventory{}, err
	}
	rows, err := db.QueryContext(ctx, `
select a.id, a.source_state, coalesce(q.state, ''), a.media_type,
       a.metadata_json, a.favorite, a.hidden, a.burst_identifier,
       exists(select 1 from location_observation where asset_id=a.id),
       (select count(*) from album_membership where asset_id=a.id),
       a.first_card_blocked_at is not null and a.first_card_blocked_snapshot_id is not null
from asset a
left join classification_queue q on q.asset_id=a.id
where a.source_library_id=?
order by a.creation_date, a.id`, strings.TrimSpace(sourceLibraryID))
	if err != nil {
		return CardInputAuditInventory{}, fmt.Errorf("read card-input audit inventory: %w", err)
	}
	result := CardInputAuditInventory{SourceLibraryID: strings.TrimSpace(sourceLibraryID), SnapshotID: snapshotID, Complete: complete}
	blockedByAsset := map[string]bool{}
	for rows.Next() {
		var row CardInputAuditInventoryRow
		var metadata string
		var favorite, hidden, hasLocation, firstCardBlocked int
		var burst string
		if err := rows.Scan(&row.AssetID, &row.SourceState, &row.QueueState, &row.MediaType, &metadata, &favorite, &hidden, &burst, &hasLocation, &row.AlbumCount, &firstCardBlocked); err != nil {
			return CardInputAuditInventory{}, err
		}
		row.Favorite, row.Hidden, row.BurstMember, row.HasLocation = favorite != 0, hidden != 0, strings.TrimSpace(burst) != "", hasLocation != 0
		row.HasMetadata = strings.TrimSpace(metadata) != "" && strings.TrimSpace(metadata) != "{}"
		result.Assets = append(result.Assets, row)
		blockedByAsset[row.AssetID] = firstCardBlocked != 0
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return CardInputAuditInventory{}, err
	}
	if err := rows.Close(); err != nil {
		return CardInputAuditInventory{}, err
	}
	rolesByAsset, err := cardInputAuditResourceRolesByAsset(ctx, db, sourceLibraryID)
	if err != nil {
		return CardInputAuditInventory{}, err
	}
	cardedByAsset, err := cardInputAuditCardedAssets(ctx, db, sourceLibraryID)
	if err != nil {
		return CardInputAuditInventory{}, err
	}
	for index := range result.Assets {
		row := &result.Assets[index]
		row.ResourceRoles = rolesByAsset[row.AssetID]
		if !complete {
			row.StopReasons = append(row.StopReasons, cardInputAuditStopSnapshotIncomplete)
		}
		eligibility := firstCardEligible
		if !cardedByAsset[row.AssetID] && blockedByAsset[row.AssetID] {
			eligibility = firstCardProhibitedDeletedBeforeCard
		}
		row.Eligibility = string(eligibility)
		if eligibility == firstCardProhibitedDeletedBeforeCard {
			row.StopReasons = append(row.StopReasons, cardInputAuditStopProhibited)
		}
		if row.SourceState != sourceStateCurrent {
			row.StopReasons = append(row.StopReasons, cardInputAuditStopSourceNotCurrent)
		}
		if row.MediaType != "image" {
			row.StopReasons = append(row.StopReasons, cardInputAuditStopUnsupportedMedia)
		}
	}
	return result, nil
}

func cardInputAuditCardedAssets(ctx context.Context, db *sql.DB, sourceLibraryID string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
select distinct observation.asset_id
from model_observation observation
join asset on asset.id = observation.asset_id
where asset.source_library_id = ? and observation.observation_type = ?`, strings.TrimSpace(sourceLibraryID), modelObservationCardSummary)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	carded := map[string]bool{}
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return nil, err
		}
		carded[assetID] = true
	}
	return carded, rows.Err()
}

func cardInputAuditResourceRolesByAsset(ctx context.Context, db *sql.DB, sourceLibraryID string) (map[string][]string, error) {
	rows, err := db.QueryContext(ctx, `
select resource.asset_id, resource.resource_type
from asset_resource resource
join asset on asset.id = resource.asset_id
where asset.source_library_id = ?
order by resource.asset_id, resource.resource_type, resource.original_filename`, strings.TrimSpace(sourceLibraryID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := map[string][]string{}
	for rows.Next() {
		var assetID, role string
		if err := rows.Scan(&assetID, &role); err != nil {
			return nil, err
		}
		roles[assetID] = append(roles[assetID], role)
	}
	return roles, rows.Err()
}

func InspectCardInputs(ctx context.Context, options CardInputAuditInspectOptions) ([]CardInputAuditInspection, error) {
	if strings.TrimSpace(options.Model) == "" || strings.TrimSpace(options.ModelURL) == "" {
		return nil, errors.New("audit card-input inspection requires model and model URL only to render the request")
	}
	db, err := openCardInputAuditArchive(ctx, options.ArchivePath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	_, complete, err := cardInputAuditSnapshot(ctx, db.DB(), options.SourceLibraryID)
	if err != nil {
		return nil, err
	}
	classifier, err := newModelClassifier(options.Model, options.ModelURL, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	inspections := make([]CardInputAuditInspection, 0, len(options.AssetIDs))
	for _, assetID := range options.AssetIDs {
		assetID = strings.TrimSpace(assetID)
		if assetID == "" || seen[assetID] {
			continue
		}
		seen[assetID] = true
		inspection, err := inspectCardInput(ctx, db.DB(), complete, options, classifier, assetID)
		if err != nil {
			return nil, err
		}
		inspections = append(inspections, inspection)
	}
	return inspections, nil
}

func inspectCardInput(ctx context.Context, db *sql.DB, complete bool, options CardInputAuditInspectOptions, classifier modelClassifier, assetID string) (CardInputAuditInspection, error) {
	input, err := loadCardInputAuditInput(ctx, db, options.SourceLibraryID, assetID)
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	inspection := CardInputAuditInspection{AssetID: assetID, Preflight: input}
	if !complete {
		inspection.StopReason = cardInputAuditStopSnapshotIncomplete
		return inspection, nil
	}
	eligibility, err := firstCardEligibilityForAsset(ctx, db, assetID)
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	if eligibility == firstCardProhibitedDeletedBeforeCard {
		inspection.StopReason = cardInputAuditStopProhibited
		return inspection, nil
	}
	if input.SourceState != sourceStateCurrent {
		inspection.StopReason = cardInputAuditStopSourceNotCurrent
		return inspection, nil
	}
	if input.MediaType != "image" {
		inspection.StopReason = cardInputAuditStopUnsupportedMedia
		return inspection, nil
	}
	stored, found, err := readStoredCardInputAuditBoundary(ctx, db, assetID)
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	if found {
		stored.AssetID = assetID
		stored.Preflight = input
		return stored, nil
	}
	original, _, _, ok, err := cardInputAuditCheckedOriginal(input, filepath.Join(options.CacheDir, "originals"))
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	if !ok {
		inspection.StopReason = cardInputAuditStopMissingMetadata
		return inspection, nil
	}
	metadata, ok := imagemetadata.ReadCheckedArtifacts(filepath.Join(options.CacheDir, "image-metadata"), original.SHA256)
	if !ok {
		inspection.StopReason = cardInputAuditStopMissingMetadata
		return inspection, nil
	}
	freshnessRequest, err := input.currentStillRequest()
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	path, current, proofSHA256, ok := photos.ReadCachedCurrentStill(filepath.Join(options.CacheDir, "originals"), freshnessRequest.SourceLibraryID, freshnessRequest.AssetUUID, freshnessRequest.Freshness)
	if !ok {
		inspection.StopReason = cardInputAuditStopMissingCurrentStill
		return inspection, nil
	}
	if input.HasLocation {
		inspection.StopReason = cardInputAuditStopMissingPlace
		return inspection, nil
	}
	source, artifacts := cardInputAuditFacts(input, original, metadata, current, proofSHA256)
	image, err := os.ReadFile(path)
	if err != nil {
		return CardInputAuditInspection{}, fmt.Errorf("read checked current still: %w", err)
	}
	prepared, err := renderPreparedCardRequest(source, artifacts, nil, image, classifier)
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	cardJSON, err := protojson.MarshalOptions{Indent: "  "}.Marshal(prepared.Input.Input)
	if err != nil {
		return CardInputAuditInspection{}, err
	}
	inspection.CardInput = cardJSON
	inspection.CardInputWire = base64.StdEncoding.EncodeToString(prepared.Input.Bytes)
	inspection.RenderedRequest = prepared.Request.Body()
	inspection.RenderedRoute, inspection.RenderedModel, inspection.CurrentStillPath = prepared.Request.Route(), prepared.Request.Model(), path
	return inspection, nil
}

func openCardInputAuditArchive(ctx context.Context, archivePath string) (*store.Store, error) {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return nil, errors.New("audit card-input archive path is required")
	}
	db, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return nil, err
	}
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version == cardInputAuditSchemaVersion || version == SchemaVersion {
		return db, nil
	}
	_ = db.Close()
	return nil, ArchiveIncompatibleError{}
}

func cardInputAuditSnapshot(ctx context.Context, db *sql.DB, sourceLibraryID string) (string, bool, error) {
	var id, state string
	err := db.QueryRowContext(ctx, `select id, completeness_state from crawl_snapshot where source_library_id=? order by completed_at desc, id desc limit 1`, strings.TrimSpace(sourceLibraryID)).Scan(&id, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, errors.New("audit card-input source library has no snapshot")
	}
	if err != nil {
		return "", false, err
	}
	return id, state == "complete", nil
}

func cardInputAuditResourceRoles(ctx context.Context, db *sql.DB, assetID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `select resource_type from asset_resource where asset_id=? order by resource_type, original_filename`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func loadCardInputAuditInput(ctx context.Context, db *sql.DB, sourceLibraryID, assetID string) (classifyInput, error) {
	var input classifyInput
	var needsDownload, favorite, hidden, hasLocation int
	err := db.QueryRowContext(ctx, `
	select coalesce(q.id, ''), a.id, a.source_library_id, a.local_identifier, coalesce(q.needs_download, 0), a.media_type, a.media_subtypes, a.creation_date, a.modification_date,
	coalesce((select seen.source_fingerprint from crawl_seen_asset seen join crawl_snapshot snapshot on snapshot.id=seen.last_seen_snapshot_id where seen.asset_id=a.id and seen.source_library_id=a.source_library_id and snapshot.completeness_state='complete'), ''),
a.timezone_name, a.width, a.height, a.duration_seconds, a.favorite, a.hidden, a.burst_identifier, a.metadata_json, a.camera_make, a.camera_model, a.lens_model,
coalesce(a.focal_length_mm,0), coalesce(a.focal_length_35mm,0), coalesce(a.aperture,0), coalesce(a.shutter_speed,0), coalesce(a.iso,0),
exists(select 1 from location_observation where asset_id=a.id), a.source_state
	from asset a left join classification_queue q on q.asset_id=a.id where a.id=? and a.source_library_id=?`, assetID, strings.TrimSpace(sourceLibraryID)).Scan(
		&input.QueueID, &input.AssetID, &input.SourceLibraryID, &input.LocalIdentifier, &needsDownload, &input.MediaType, &input.MediaSubtypes, &input.CreationDate, &input.ModificationDate, &input.SourceFingerprint, &input.TimezoneName, &input.Width, &input.Height, &input.DurationSeconds, &favorite, &hidden, &input.BurstIdentifier, &input.MetadataJSON, &input.CameraMake, &input.CameraModel, &input.LensModel, &input.FocalLengthMM, &input.FocalLength35MM, &input.Aperture, &input.ShutterSpeed, &input.ISO, &hasLocation, &input.SourceState)
	if errors.Is(err, sql.ErrNoRows) {
		return classifyInput{}, fmt.Errorf("audit card-input asset %q is not in source library", assetID)
	}
	if err != nil {
		return classifyInput{}, err
	}
	input.NeedsDownload, input.Favorite, input.Hidden, input.HasLocation = needsDownload != 0, favorite != 0, hidden != 0, hasLocation != 0
	if input.HasLocation {
		if err := loadClassifyLocation(ctx, db, &input); err != nil {
			return classifyInput{}, err
		}
	}
	resources, err := loadCardInputAuditResources(ctx, db, assetID)
	if err != nil {
		return classifyInput{}, err
	}
	input.Resources = resources
	albums, err := loadCardInputAuditAlbums(ctx, db, assetID)
	if err != nil {
		return classifyInput{}, err
	}
	input.Albums = albums
	return input, nil
}

func loadCardInputAuditResources(ctx context.Context, db *sql.DB, assetID string) ([]classifyResource, error) {
	rows, err := db.QueryContext(ctx, `select id, resource_type, uti, original_filename, local_path, file_size, available_locally, needs_download, sha256 from asset_resource where asset_id=? order by resource_type, original_filename`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var resources []classifyResource
	for rows.Next() {
		var resource classifyResource
		var available, needs int
		if err := rows.Scan(&resource.ID, &resource.ResourceType, &resource.UTI, &resource.OriginalFilename, &resource.LocalPath, &resource.FileSize, &available, &needs, &resource.SHA256); err != nil {
			return nil, err
		}
		resource.AvailableLocally, resource.NeedsDownload = available != 0, needs != 0
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func loadCardInputAuditAlbums(ctx context.Context, db *sql.DB, assetID string) ([]classifyAlbum, error) {
	rows, err := db.QueryContext(ctx, `select album_title, album_kind from album_membership where asset_id=? order by album_title, album_kind`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var albums []classifyAlbum
	for rows.Next() {
		var album classifyAlbum
		if err := rows.Scan(&album.AlbumTitle, &album.AlbumKind); err != nil {
			return nil, err
		}
		albums = append(albums, album)
	}
	return albums, rows.Err()
}

func cardInputAuditOriginal(resources []classifyResource) (classifyResource, bool) {
	for _, resource := range resources {
		if resource.ResourceType == "photo" && len(strings.TrimSpace(resource.SHA256)) == sha256.Size*2 {
			return resource, true
		}
	}
	return classifyResource{}, false
}

// cardInputAuditCheckedPackageOriginal reopens the same unique package-local
// original that prepare used. Archive resource digests are observations, so
// the metadata lookup and CardInput fact use the verified bytes instead.
func cardInputAuditCheckedPackageOriginal(input classifyInput) (classifyResource, bool, error) {
	candidate, ok := photos.UniquePackageOriginal(input.originalRequest().PackageCandidates)
	if !ok {
		return classifyResource{}, false, nil
	}
	info, digest, err := photos.InspectOriginalFile(candidate.Path)
	if err != nil {
		return classifyResource{}, false, nil
	}
	for _, resource := range input.Resources {
		if resource.ResourceType != "local_original" || filepath.Clean(resource.LocalPath) != filepath.Clean(candidate.Path) {
			continue
		}
		resource.AvailableLocally = true
		resource.NeedsDownload = false
		resource.FileSize = info.Size()
		resource.SHA256 = hex.EncodeToString(digest[:])
		return resource, true, nil
	}
	return classifyResource{}, false, errors.New("unique package-local immutable original has no matching resource")
}

func cardInputAuditCheckedOriginal(input classifyInput, cacheRoot string) (classifyResource, string, string, bool, error) {
	if resource, ok, err := cardInputAuditCheckedPackageOriginal(input); err != nil || ok {
		path := ""
		if ok {
			path = resource.LocalPath
		}
		return resource, path, photos.OriginalSourcePackage, ok, err
	}
	request := input.originalRequest()
	path, size, digest, ok := photos.ReadCachedOriginal(cacheRoot, request)
	if !ok {
		return classifyResource{}, "", "", false, nil
	}
	for _, resource := range input.Resources {
		if resource.ResourceType != "photo" || resource.OriginalFilename != request.Query.OriginalFilename {
			continue
		}
		resource.AvailableLocally = true
		resource.NeedsDownload = false
		resource.LocalPath = path
		resource.FileSize = size
		resource.SHA256 = digest
		return resource, path, photos.OriginalSourceCache, true, nil
	}
	return classifyResource{}, "", "", false, errors.New("checked PhotoKit original has no matching archive resource")
}

func cardInputAuditFacts(input classifyInput, original classifyResource, metadata imagemetadata.Artifacts, current photos.CurrentStillFact, proofSHA256 string) (cardinput.SourceFacts, cardinput.CheckedArtifacts) {
	originalFact := cardinput.ImmutableOriginalFact{ResourceType: original.ResourceType, UTI: original.UTI, Filename: original.OriginalFilename, Availability: original.Availability(), SizeBytes: original.FileSize, SHA256: strings.ToLower(original.SHA256)}
	metadataFact := cardinput.MetadataFact{RecordSHA256: metadata.Proof.RecordSHA256, ProjectionSHA256: metadata.Proof.ProjectionSHA256, ProjectionLines: metadata.Projection.Lines}
	currentFact := cardinput.FullCurrentFact{Role: "full_current", MediaType: current.MediaType, Orientation: current.Orientation, PixelWidth: current.PixelWidth, PixelHeight: current.PixelHeight, SizeBytes: current.Size, SHA256: current.SHA256}
	source := cardinput.SourceFacts{AssetID: input.AssetID, SourceID: input.SourceLibraryID, CaptureTime: input.CreationDate, MediaType: input.MediaType, MediaSubtypes: splitSubtypes(input.MediaSubtypes), PixelWidth: input.Width, PixelHeight: input.Height, DurationSeconds: input.DurationSeconds, ImmutableOriginal: originalFact, Favorite: input.Favorite, Hidden: input.Hidden, BurstMember: strings.TrimSpace(input.BurstIdentifier) != "", Metadata: metadataFact, FullCurrent: currentFact}
	if strings.TrimSpace(input.TimezoneName) != "" {
		timezone := input.TimezoneName
		source.Timezone = &timezone
	}
	for _, album := range input.Albums {
		source.Albums = append(source.Albums, cardinput.AlbumFact{Title: album.AlbumTitle, Kind: album.AlbumKind})
	}
	if input.CameraMake != "" || input.CameraModel != "" || input.LensModel != "" || input.FocalLengthMM > 0 || input.FocalLength35MM > 0 || input.Aperture > 0 || input.ShutterSpeed > 0 || input.ISO > 0 {
		camera := &cardinput.CameraFact{Make: input.CameraMake, Model: input.CameraModel, LensModel: input.LensModel}
		if input.FocalLengthMM > 0 {
			value := input.FocalLengthMM
			camera.FocalLengthMM = &value
		}
		if input.FocalLength35MM > 0 {
			value := input.FocalLength35MM
			camera.FocalLength35MM = &value
		}
		if input.Aperture > 0 {
			value := input.Aperture
			camera.Aperture = &value
		}
		if input.ShutterSpeed > 0 {
			value := input.ShutterSpeed
			camera.ShutterSpeed = &value
		}
		if input.ISO > 0 {
			value := input.ISO
			camera.ISO = &value
		}
		source.Camera = camera
	}
	artifacts := cardinput.CheckedArtifacts{ImmutableOriginal: cardinput.CheckedImmutableOriginal{Fact: originalFact, ResourceID: original.ID}, Metadata: cardinput.CheckedMetadata{Fact: metadataFact, RecordID: "image_metadata:" + metadata.Proof.RecordSHA256, ProjectionID: "image_metadata_projection:" + metadata.Proof.ProjectionSHA256}, FullCurrent: cardinput.CheckedFullCurrent{Fact: currentFact, ProofSHA256: proofSHA256}}
	return source, artifacts
}

func StableCardInputAuditBackstop(snapshotID string, assets []string, count int) []string {
	if count <= 0 {
		return nil
	}
	candidates := append([]string(nil), assets...)
	sort.Slice(candidates, func(i, j int) bool {
		left := sha256.Sum256([]byte(snapshotID + "\x00" + candidates[i]))
		right := sha256.Sum256([]byte(snapshotID + "\x00" + candidates[j]))
		return hex.EncodeToString(left[:]) < hex.EncodeToString(right[:])
	})
	if count > len(candidates) {
		count = len(candidates)
	}
	return candidates[:count]
}
