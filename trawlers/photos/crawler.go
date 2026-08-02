package photos

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
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

type Crawler struct {
	cfg                    Config
	snapshotProvider       photos.Provider
	maximumAssetsToProcess int
}

type Config struct {
	LibraryPath            string `toml:"library_path"`
	GeoapifyAPIKeyFilePath string `toml:"geoapify_api_key_file"`
	CodexExecutablePath    string `toml:"codex_executable_path"`
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
			LeavesMachine:   "Photo coordinates and nearby-place requests go to Apple and Geoapify. Current rendered photos and useful source facts go to GPT-5.6 Luna through Codex-managed ChatGPT sign-in.",
			NetworkRequests: "Updates use Apple and Geoapify for location evidence and GPT-5.6 Luna for photo understanding.",
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
				flagSet.IntVar(&c.maximumAssetsToProcess, "maximum-assets", 0, "maximum pending photos to enrich and describe")
			},
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
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
	libraryPath := strings.TrimSpace(c.cfg.LibraryPath)
	if libraryPath == "" {
		var err error
		libraryPath, err = archive.DefaultPhotosLibraryPath()
		if err != nil {
			return nil, err
		}
	}
	reportProgress(req, "update", 0, 0, "updating Photos library")
	sourceUpdateStartedAt := time.Now()
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
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("photos_component", fmt.Sprintf("component=source outcome=succeeded duration=%s", time.Since(sourceUpdateStartedAt)))
	}
	photoUpdateResult, err := updatephotos.Run(ctx, updatephotos.Options{
		OpenedArchiveStore:     req.OpenedTrawlerArchiveStore,
		GeoapifyAPIKeyFilePath: c.cfg.GeoapifyAPIKeyFilePath,
		CodexExecutablePath:    c.cfg.CodexExecutablePath,
		WorkingDirectory:       filepath.Join(archivePaths(req).CacheDir, "luna-empty-working-directory"),
		MaximumAssetsToProcess: c.maximumAssetsToProcess,
		ReportProgress: func(completed, total int, message string) {
			reportProgress(req, "photos", int64(completed), int64(total), message)
		},
		ReportComponent: func(component, outcome string, duration time.Duration) {
			if req.TrawlerCommandLog != nil {
				_ = req.TrawlerCommandLog.Info("photos_component", fmt.Sprintf("component=%s outcome=%s duration=%s", component, outcome, duration))
			}
		},
	})
	if err != nil {
		return nil, err
	}
	reportProgress(req, "update", int64(result.AssetsSeen), int64(result.AssetsSeen), "updated Photos library and cards")
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("update_written", updateLogMessage(result))
		_ = req.TrawlerCommandLog.Info("photo_cards_written", fmt.Sprintf("pending=%d selected=%d cards=%d unavailable=%d unsupported=%d deferred_or_failed=%d", photoUpdateResult.PendingAssets, photoUpdateResult.SelectedAssets, photoUpdateResult.CardsStored, photoUpdateResult.MediaUnavailable, photoUpdateResult.UnsupportedMedia, photoUpdateResult.DeferredOrFailed))
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
		"provider=%s completeness=%s assets=%d new=%d changed=%d unchanged=%d missing=%d",
		result.Provider,
		result.SnapshotCompleteness,
		result.AssetsSeen,
		result.AssetsNew,
		result.AssetsChanged,
		result.AssetsUnchanged,
		result.PreviouslySeenMissing,
	)
}
