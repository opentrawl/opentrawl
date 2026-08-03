package photos

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/media/mediawire"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/updatephotos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const heartbeatEvery = 30 * time.Second

type photosProgressPhase string
type photosLogEventName string
type photosObservationTemplateName string

const (
	photosProgressUpdate      photosProgressPhase = "update"
	photosProgressFoundations photosProgressPhase = "photos"

	photosLogHealth              photosLogEventName            = "photos_health"
	photosLogOperationCompleted  photosLogEventName            = "photos_operation_completed"
	photosLogOperationAttention  photosLogEventName            = "photos_operation_needs_attention"
	photosLogRenderFailed        photosLogEventName            = "photos_observation_failed"
	photosLogUpdateWritten       photosLogEventName            = "update_written"
	photosLogFoundationsWritten  photosLogEventName            = "photo_foundations_written"
	photosMessageUpdate          photosObservationTemplateName = "update-running"
	photosMessageSourceCopy      photosObservationTemplateName = "source-copying"
	photosMessageSourceRead      photosObservationTemplateName = "source-reading"
	photosMessageHealth          photosObservationTemplateName = "health"
	photosMessageOperation       photosObservationTemplateName = "operation"
	photosMessageSourceDone      photosObservationTemplateName = "source-completed"
	photosMessageFoundationsDone photosObservationTemplateName = "foundation-completed"
	photosMessageUpdateDone      photosObservationTemplateName = "update-completed"
)

//go:embed observation_messages.tmpl
var photosObservationTemplatesText string

var photosObservationTemplates, photosObservationTemplatesError = template.New("photos-observation").Funcs(template.FuncMap{
	"mediaDeferral": mediaDeferralName,
	"mediaFailure":  mediaFailureName,
}).Parse(photosObservationTemplatesText)

type photosObservationTemplateData struct {
	Snapshot   *updatephotos.OperationalSnapshot
	Outcome    *updatephotos.WorkOutcomeObservation
	Source     archive.UpdateResult
	Foundation updatephotos.Result
}

type Crawler struct {
	cfg                    Config
	snapshotProvider       photos.Provider
	maximumAssetsToProcess int
}

type Config struct {
	LibraryPath            string `toml:"library_path"`
	GeoapifyAPIKeyFilePath string `toml:"geoapify_api_key_file"`
}

var (
	_ trawlkit.Trawler  = (*Crawler)(nil)
	_ trawlkit.Updater  = (*Crawler)(nil)
	_ trawlkit.Searcher = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("photos"),
		RegisteredTrawlerCommandName: "photos",
		RegisteredTrawlerDisplayName: "Photos",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Your Apple Photos library's metadata and photos.",
			LeavesMachine:   "Photo coordinates and nearby-place requests go to Apple and Geoapify. Photo pixels and source facts stay on this Mac.",
			NetworkRequests: "Updates use Apple and Geoapify for location evidence.",
		},
	}
}

func (c *Crawler) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	loadedPhotosConfiguration := c.cfg
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &loadedPhotosConfiguration); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := loadedPhotosConfiguration.Validate(); err != nil {
		return err
	}
	c.cfg = loadedPhotosConfiguration
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
			RegisterTrawlerCommandFlags: func(flagSet *flag.FlagSet) {
				flagSet.IntVar(&c.maximumAssetsToProcess, "maximum-assets", 0, "maximum pending photos to process")
			},
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			TrawlerCommandName:                    "debug",
			TrawlerCommandHelpDescription:         "Inspect one production Photos operation",
			TrawlerCommandPositionalArgumentNames: []string{"[NODE]", "[PHOTO]"},
			TrawlerCommandChangesArchive:          false,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			ExecuteTrawlerCommand:                 c.debugProductionNode,
		},
		{
			TrawlerCommandName:                    "run",
			TrawlerCommandHelpDescription:         "Run one production Photos operation",
			TrawlerCommandPositionalArgumentNames: []string{"NODE", "[PHOTO]"},
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			ExecuteTrawlerCommand:                 c.runProductionNode,
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	archiveStatus, err := archive.Status(ctx, archivePaths(req))
	if err != nil {
		return nil, err
	}
	if !archiveStatus.ArchiveExists {
		return response, nil
	}
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "photos", ArchiveContentKindDisplayName: "photos", ArchiveContentCount: uint64(archiveStatus.Photos)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime); err == nil {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (c *Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	if c.maximumAssetsToProcess < 0 {
		return nil, output.UsageError{Err: errors.New("update photos --maximum-assets must be 0 or greater")}
	}
	reportProgress(req, string(photosProgressUpdate), 0, 0, renderPhotosObservation(req, photosMessageUpdate, photosObservationTemplateData{}))
	result, err := c.updatePhotosSourceIndex(ctx, req)
	if err != nil {
		return nil, err
	}
	photoUpdateResult, err := updatephotos.Run(ctx, updatephotos.Options{
		OpenedArchiveStore:     req.OpenedTrawlerArchiveStore,
		GeoapifyAPIKeyFilePath: c.cfg.GeoapifyAPIKeyFilePath,
		PhotosWorkingRoot:      filepath.Join(archivePaths(req).CacheDir, "photos-working"),
		MaximumAssetsToProcess: c.maximumAssetsToProcess,
		Observe:                observePhotosUpdate(req),
	})
	if err != nil {
		return nil, err
	}
	reportProgress(req, string(photosProgressUpdate), int64(result.AssetsSeen), int64(result.AssetsSeen), renderPhotosObservation(req, photosMessageUpdateDone, photosObservationTemplateData{}))
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info(string(photosLogUpdateWritten), renderPhotosObservation(req, photosMessageSourceDone, photosObservationTemplateData{Source: result}))
		_ = req.TrawlerCommandLog.Info(string(photosLogFoundationsWritten), renderPhotosObservation(req, photosMessageFoundationsDone, photosObservationTemplateData{Foundation: photoUpdateResult}))
	}
	completedPhotoEnrichmentOutcomes := photoUpdateResult.FoundationsStored
	return &updatecontract.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(result.AssetsNew)),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(result.AssetsChanged + completedPhotoEnrichmentOutcomes)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(uint64(result.PreviouslySeenMissing)),
	}, nil
}

func (c *Crawler) updatePhotosSourceIndex(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (archive.UpdateResult, error) {
	libraryPath := strings.TrimSpace(c.cfg.LibraryPath)
	if libraryPath == "" {
		var err error
		libraryPath, err = archive.DefaultPhotosLibraryPath()
		if err != nil {
			return archive.UpdateResult{}, err
		}
	}
	var sourceProgressMutex sync.Mutex
	latestSourceProgress := photos.SnapshotProgress{}
	var result archive.UpdateResult
	err := withHeartbeat(ctx, func() {
		sourceProgressMutex.Lock()
		progress := latestSourceProgress
		sourceProgressMutex.Unlock()
		messageName := photosMessageSourceRead
		if progress.Phase == photos.SnapshotProgressCopyingDatabase {
			messageName = photosMessageSourceCopy
		}
		reportProgress(req, string(photosProgressUpdate), int64(progress.AssetsRead), int64(progress.ExpectedAssets), renderPhotosObservation(req, messageName, photosObservationTemplateData{}))
	}, func() error {
		var updateErr error
		result, updateErr = archive.UpdateWithStore(ctx, req.OpenedTrawlerArchiveStore, archivePaths(req), archive.UpdateOptions{
			LibraryPath: libraryPath,
			Provider:    c.provider(),
			ReportProgress: func(progress photos.SnapshotProgress) {
				sourceProgressMutex.Lock()
				latestSourceProgress = progress
				sourceProgressMutex.Unlock()
			},
		})
		return updateErr
	})
	if err != nil {
		return archive.UpdateResult{}, updateCommandError(err)
	}
	return result, nil
}

func (c *Crawler) provider() photos.Provider {
	if c.snapshotProvider != nil {
		return c.snapshotProvider
	}
	return photos.NewProvider()
}

func updateCommandError(err error) error {
	var incomplete *archive.SnapshotIncompleteError
	if !errors.As(err, &incomplete) {
		return err
	}
	return commandError{
		Code:    "snapshot_incomplete",
		Message: incomplete.Error(),
	}
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	if strings.TrimSpace(query.Text) == "" {
		return nil, output.UsageError{Err: output.HumanFacingErrorMessage("Photos search needs words.")}
	}
	archiveSearchResponse, err := archive.SearchWithStore(ctx, req.OpenedTrawlerArchiveStore, archive.SearchOptions{
		Query:         query.Text,
		Limit:         query.Limit,
		BoundedTotals: query.SearchTotalIsLowerBoundWhenResultLimitIsReached,
		After:         queryTime(query.After),
		Before:        queryTime(query.Before),
	})
	if err != nil {
		return nil, archiveReadCommandError(err)
	}
	trawlerSearchMatches := make([]*search.TrawlerSearchMatch, 0, len(archiveSearchResponse.Results))
	for _, archiveSearchHit := range archiveSearchResponse.Results {
		searchMatch, err := photoTrawlerSearchMatch(archiveSearchHit)
		if err != nil {
			return nil, err
		}
		trawlerSearchMatches = append(trawlerSearchMatches, searchMatch)
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("search_written", fmt.Sprintf("returned=%d total=%d truncated=%t", len(archiveSearchResponse.Results), archiveSearchResponse.TotalMatches, archiveSearchResponse.Truncated))
	}
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
		TotalSearchMatches:                 uint64(archiveSearchResponse.TotalMatches),
		TotalSearchMatchesIsLowerBound:     archiveSearchResponse.TotalIsLowerBound,
		MoreSearchMatchesExist:             archiveSearchResponse.Truncated,
	}, nil
}

func archivePaths(req *trawlkit.TrawlerCommandExecutionRequest) archive.Paths {
	base := filepath.Dir(req.TrawlerArchivePaths.TrawlerArchivePath)
	return archive.Paths{
		ConfigPath: string(req.TrawlerArchivePaths.TrawlerConfigurationPath),
		DataDir:    base,
		Database:   req.TrawlerArchivePaths.TrawlerArchivePath,
		CacheDir:   filepath.Join(base, "cache"),
		LogDir:     req.TrawlerArchivePaths.TrawlerLogDirectoryPath,
		ShareDir:   filepath.Join(base, "share"),
	}
}

func photoTrawlerSearchMatch(archiveSearchHit archive.SearchHit) (*search.TrawlerSearchMatch, error) {
	var associatedExactTime time.Time
	if timeText := strings.TrimSpace(archiveSearchHit.Time); timeText != "" {
		parsed, err := time.Parse(time.RFC3339, timeText)
		if err != nil {
			return nil, fmt.Errorf("parse search hit time: %w", err)
		}
		associatedExactTime = parsed
	}
	searchMatchPresentation := &search.SearchMatchPresentation{
		MatchingRecordDisplayName: strings.TrimSpace(archiveSearchHit.Title),
	}
	if searchMatchPresentation.MatchingRecordDisplayName == "" {
		searchMatchPresentation.MatchingRecordDisplayName = "Photo"
	}
	if !associatedExactTime.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(associatedExactTime)},
		}
	}
	for _, matchedField := range archiveSearchHit.Matches {
		matchingPhotoText := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			photoSearchFieldDisplayName(matchedField.Field),
			matchedField.Runs,
		)
		if matchingPhotoText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = append(
				searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder,
				matchingPhotoText,
			)
		}
	}
	if len(searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder) == 0 {
		if matchingPhotoText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Photo", archiveSearchHit.Snippet); matchingPhotoText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = append(searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder, matchingPhotoText)
		}
	}
	if locationContext := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Location", archiveSearchHit.Where); locationContext != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = append(searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder, locationContext)
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchHit.Ref),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(archiveSearchHit.AnchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}, nil
}

func photoSearchFieldDisplayName(field string) string {
	switch strings.TrimSpace(field) {
	case "filename":
		return "Filename"
	case "album":
		return "Album"
	case "capture-location":
		return "Capture location"
	default:
		return "Photo"
	}
}

func queryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func reportProgress(req *trawlkit.TrawlerCommandExecutionRequest, phase string, done, total int64, message string) {
	if req.ReportTrawlerCommandProgress == nil {
		return
	}
	req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
}

func withHeartbeat(ctx context.Context, progress func(), fn func() error) error {
	done := make(chan error, 1)
	go func() {
		done <- fn()
	}()
	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			progress()
		}
	}
}

func observePhotosUpdate(req *trawlkit.TrawlerCommandExecutionRequest) func(updatephotos.Observation) {
	return func(observation updatephotos.Observation) {
		switch typed := observation.(type) {
		case updatephotos.OperationalSnapshot:
			message := renderPhotosObservation(req, photosMessageHealth, photosObservationTemplateData{Snapshot: &typed})
			reportProgress(req, string(photosProgressFoundations), int64(typed.Completed), int64(typed.Total), message)
		case updatephotos.WorkOutcomeObservation:
			if req.TrawlerCommandLog == nil {
				return
			}
			message := renderPhotosObservation(req, photosMessageOperation, photosObservationTemplateData{Outcome: &typed})
			if typed.Disposition >= updatephotos.WorkRetried {
				_ = req.TrawlerCommandLog.Warn(string(photosLogOperationAttention), message)
			} else {
				_ = req.TrawlerCommandLog.Info(string(photosLogOperationCompleted), message)
			}
		}
	}
}

func renderPhotosObservation(req *trawlkit.TrawlerCommandExecutionRequest, name photosObservationTemplateName, data photosObservationTemplateData) string {
	if photosObservationTemplatesError != nil {
		if req.TrawlerCommandLog != nil {
			_ = req.TrawlerCommandLog.Error(string(photosLogRenderFailed), photosObservationTemplatesError)
		}
		return ""
	}
	var rendered strings.Builder
	if err := photosObservationTemplates.ExecuteTemplate(&rendered, string(name), data); err != nil {
		if req.TrawlerCommandLog != nil {
			_ = req.TrawlerCommandLog.Error(string(photosLogRenderFailed), err)
		}
		return ""
	}
	return rendered.String()
}

func mediaDeferralName(reason mediawire.PhotosMediaAdmissionDeferralReason) string {
	switch reason {
	case mediawire.PhotosMediaAdmissionDeferralReason_PHOTOS_MEDIA_ADMISSION_DEFERRAL_REASON_CACHE_CAPACITY:
		return "cache-capacity"
	case mediawire.PhotosMediaAdmissionDeferralReason_PHOTOS_MEDIA_ADMISSION_DEFERRAL_REASON_FILESYSTEM_FREE_SPACE_FLOOR:
		return "filesystem-free-space-floor"
	default:
		return "unknown"
	}
}

func mediaFailureName(kind mediawire.PhotosMediaOperationFailureKind) string {
	switch kind {
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_INVALID_REQUEST:
		return "invalid-request"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_CACHE_IO:
		return "cache-io"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_IPC_IO:
		return "ipc-io"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_INDEXED_SOURCE_CHANGED:
		return "indexed-source-changed"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_PHOTOS_TIMEOUT:
		return "photos-timeout"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_PHOTOS_CANCELLED:
		return "photos-cancelled"
	case mediawire.PhotosMediaOperationFailureKind_PHOTOS_MEDIA_OPERATION_FAILURE_KIND_PHOTOS_PROVIDER_FAILURE:
		return "photos-provider-failure"
	default:
		return "unknown"
	}
}
