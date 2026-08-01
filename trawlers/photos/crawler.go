package photos

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	ollamaCloudBaseURL = "https://ollama.com/v1"
	ollamaAPIKeyEnv    = "OLLAMA_API_KEY"
	heartbeatEvery     = 30 * time.Second
)

type Crawler struct {
	cfg                             Config
	snapshotProvider                photos.Provider
	classifyLimit                   trackedLimit
	classifyModel                   string
	currentStillAsset               string
	currentStillSource              string
	currentStillAllowICloudDownload bool
	currentStillExcludedAssets      []string
}

type Config struct {
	LibraryPath string          `toml:"library_path"`
	CardModel   CardModelConfig `toml:"card_model"`
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
			Reads:           "Your Apple Photos library's metadata and, when you explicitly use model-powered features, selected photos.",
			LeavesMachine:   "Nothing during a normal update. Model-powered classification or an approved photo card sends the selected photo and its details to the model provider.",
			NetworkRequests: "Normal updates are local. Classification may ask Apple for place details; model-powered features request analysis from Ollama Cloud or the model provider you configured.",
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
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			TrawlerCommandName:               "classify",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:    "Write metadata, place and model-card observations.",
			TrawlerCommandChangesArchive:     true,
			RegisterTrawlerCommandFlags:      c.classifyFlags,
			ExecuteTrawlerCommand:            c.runClassify,
		},
		{
			TrawlerCommandName:               "select-card-input-ready",
			TrawlerCommandHelpDescription:    "Select one PhotoKit-ready image before checked media acquisition.",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessNone,
			RegisterTrawlerCommandFlags:      c.currentStillReadinessFlags,
			ExecuteTrawlerCommand:            c.runCardInputReadiness,
		},
		{
			TrawlerCommandName:               "acquire-current-still",
			TrawlerCommandHelpDescription:    "Acquire one checked current still for an exact asset.",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessNone,
			RegisterTrawlerCommandFlags:      c.currentStillFlags,
			ExecuteTrawlerCommand:            c.runCurrentStillAcquire,
		},
		{
			TrawlerCommandName:                    "prepare-card",
			TrawlerCommandHelpDescription:         "Prepare one Photos card request for review.",
			TrawlerCommandPositionalArgumentNames: []string{"PHOTO"},
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessNone,
			ExecuteTrawlerCommand:                 c.runPrepareCard,
		},
		{
			TrawlerCommandName:                    "create-card",
			TrawlerCommandHelpDescription:         "Create one approved Photos card.",
			TrawlerCommandPositionalArgumentNames: []string{"APPROVAL"},
			TrawlerCommandChangesArchive:          true,
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessNone,
			ExecuteTrawlerCommand:                 c.runCreateCard,
		},
	}
}

func (c *Crawler) currentStillReadinessFlags(fs *flag.FlagSet) {
	c.currentStillSource = ""
	c.currentStillExcludedAssets = nil
	fs.StringVar(&c.currentStillSource, "source-library", "", "exact Photos source library ID")
	fs.Func("exclude-asset", "exact stopped asset identity to exclude; repeat for each asset", func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return errors.New("excluded asset identity is required")
		}
		c.currentStillExcludedAssets = append(c.currentStillExcludedAssets, value)
		return nil
	})
}

func (c *Crawler) classifyFlags(fs *flag.FlagSet) {
	c.classifyLimit = trackedLimit{value: 100}
	c.classifyModel = ""
	fs.Var(&c.classifyLimit, "limit", "max pending assets to classify")
	fs.StringVar(&c.classifyModel, "model", "", "Ollama Cloud vision model for content observations; requires OLLAMA_API_KEY")
}

func (c *Crawler) currentStillFlags(fs *flag.FlagSet) {
	c.currentStillAsset = ""
	c.currentStillSource = ""
	c.currentStillAllowICloudDownload = false
	fs.StringVar(&c.currentStillAsset, "asset", "", "exact Photos asset ID")
	fs.StringVar(&c.currentStillSource, "source-library", "", "exact Photos source library ID")
	fs.BoolVar(&c.currentStillAllowICloudDownload, "allow-icloud-download", false, "download this photo from iCloud when it is not on this Mac")
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	archiveStatus, err := archive.Status(ctx, archivePaths(req))
	if err != nil || !archiveStatus.ArchiveExists {
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
	libraryPath := strings.TrimSpace(c.cfg.LibraryPath)
	if libraryPath == "" {
		var err error
		libraryPath, err = archive.DefaultPhotosLibraryPath()
		if err != nil {
			return nil, err
		}
	}
	reportProgress(req, "update", 0, 0, "updating Photos library")
	var result archive.UpdateResult
	err := withHeartbeat(ctx, func() {
		reportProgress(req, "update", 0, 0, "updating Photos library")
	}, func() error {
		var updateErr error
		result, updateErr = archive.UpdateWithStore(ctx, req.OpenedTrawlerArchiveStore, archivePaths(req), archive.UpdateOptions{
			LibraryPath: libraryPath,
			Provider:    c.provider(),
		})
		return updateErr
	})
	if err != nil {
		return nil, updateCommandError(err)
	}
	reportProgress(req, "update", int64(result.AssetsSeen), int64(result.AssetsSeen), "updated Photos library")
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("update_written", updateLogMessage(result))
	}
	return &updatecontract.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(result.AssetsNew)),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(result.AssetsChanged)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(uint64(result.PreviouslySeenMissing)),
	}, nil
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

func (c *Crawler) runClassify(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, output.UsageError{Err: fmt.Errorf("classify takes flags only")}
	}
	limit, err := flags.Limit(c.classifyLimit.value, c.classifyLimit.set)
	if err != nil {
		return nil, output.UsageError{Err: err}
	}
	reportProgress(req, "classify", 0, int64(limit), "classifying queued photos")
	var result archive.ClassifyResult
	err = withHeartbeat(ctx, func() {
		reportProgress(req, "classify", 0, int64(limit), "classifying queued photos")
	}, func() error {
		var classifyErr error
		result, classifyErr = archive.ClassifyWithStore(ctx, req.OpenedTrawlerArchiveStore, archivePaths(req), archive.ClassifyOptions{
			Limit:       limit,
			Model:       c.classifyModel,
			ModelURL:    ollamaCloudBaseURL,
			ModelKeyEnv: ollamaAPIKeyEnv,
			LogSink:     req.TrawlerCommandLog,
		})
		return classifyErr
	})
	if err != nil {
		return nil, err
	}
	reportProgress(req, "classify", int64(result.Processed), int64(result.Processed), "classified queued photos")
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("classify_written", fmt.Sprintf("processed=%d metadata=%d content=%d failures=%d", result.Processed, result.MetadataClassified, result.ContentClassified, result.ContentClassificationFailures))
	}
	return photosDetailCommandResponse("Photos classification complete",
		photosDetailUnsignedCountField("Processed", int64(result.Processed)),
		photosDetailUnsignedCountField("Metadata classified", int64(result.MetadataClassified)),
		photosDetailUnsignedCountField("Content classified", int64(result.ContentClassified)),
		photosDetailUnsignedCountField("Content classification failures", int64(result.ContentClassificationFailures))), nil
}

func (c *Crawler) runCurrentStillAcquire(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, output.UsageError{Err: errors.New("acquire-current-still takes flags only")}
	}
	if strings.TrimSpace(c.currentStillAsset) == "" || strings.TrimSpace(c.currentStillSource) == "" {
		return nil, output.UsageError{Err: errors.New("acquire-current-still requires --asset and --source-library")}
	}
	result, err := archive.AcquireCardInputCurrentStill(ctx, archive.CardInputCurrentStillOptions{
		CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{
			ArchivePath:     req.TrawlerArchivePaths.TrawlerArchivePath,
			SourceLibraryID: strings.TrimSpace(c.currentStillSource),
		},
		AssetID:      strings.TrimSpace(c.currentStillAsset),
		AllowNetwork: c.currentStillAllowICloudDownload,
	})
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse("Current still acquisition",
		photosDetailTextField("Photo", archive.AssetRef(result.AssetID)),
		photosDetailTextField("Stop reason", result.StopReason),
		photosDetailTextField("Current still source", result.CurrentStillSource),
		photosDetailUnsignedCountField("Original bytes", result.ImmutableOriginal.Size),
		photosDetailTextField("Current still format", result.CurrentStill.MediaType),
		photosDetailUnsignedCountField("Current still width", result.CurrentStill.PixelWidth),
		photosDetailUnsignedCountField("Current still height", result.CurrentStill.PixelHeight),
		photosDetailUnsignedCountField("Current still bytes", result.CurrentStill.Size),
		photosDetailUnsignedCountField("Original requests", int64(result.OriginalRequests)),
		photosDetailUnsignedCountField("Current still requests", int64(result.CurrentStillRequests))), nil
}

func (c *Crawler) runCardInputReadiness(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, output.UsageError{Err: errors.New("select-card-input-ready takes flags only")}
	}
	if strings.TrimSpace(c.currentStillSource) == "" {
		return nil, output.UsageError{Err: errors.New("select-card-input-ready requires --source-library")}
	}
	result, err := archive.SelectCardInputReadyAsset(ctx, archive.CardInputReadinessOptions{
		CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{
			ArchivePath:     req.TrawlerArchivePaths.TrawlerArchivePath,
			SourceLibraryID: strings.TrimSpace(c.currentStillSource),
		},
		ExcludedAssetIDs: append([]string(nil), c.currentStillExcludedAssets...),
	})
	if err != nil {
		return nil, err
	}
	return photosDetailCommandResponse("Card input ready",
		photosDetailTextField("Photo", archive.AssetRef(result.AssetID))), nil
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
	if matchingPhotoText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Photo", archiveSearchHit.Snippet); matchingPhotoText != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingPhotoText}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchHit.Ref),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(archiveSearchHit.AnchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}, nil
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

func updateLogMessage(result archive.UpdateResult) string {
	return fmt.Sprintf(
		"provider=%s completeness=%s assets=%d new=%d changed=%d unchanged=%d missing=%d "+
			"queued_for_classify=%d queued_needs_download=%d classification_queue_pending=%d "+
			"marked_stale_model_assets=%d marked_stale_model_rows=%d "+
			"marked_stale_place_assets=%d marked_stale_place_rows=%d",
		result.Provider,
		result.SnapshotCompleteness,
		result.AssetsSeen,
		result.AssetsNew,
		result.AssetsChanged,
		result.AssetsUnchanged,
		result.PreviouslySeenMissing,
		result.QueuedForClassify,
		result.QueuedNeedsDownload,
		result.ClassificationQueuePending,
		result.MarkedStaleModelAssets,
		result.MarkedStaleModelRows,
		result.MarkedStalePlaceAssets,
		result.MarkedStalePlaceRows,
	)
}

type trackedLimit struct {
	value int
	set   bool
}

func (l *trackedLimit) String() string {
	if l == nil || l.value == 0 {
		return "100"
	}
	return strconv.Itoa(l.value)
}

func (l *trackedLimit) Set(value string) error {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	l.value = parsed
	l.set = true
	return nil
}
