// Package photos is the read-only Apple Photos trawler. It runs five steps
// over one archive: read Apple's library, fetch each picture, look up the
// places near each camera spot, write each card model input and write each
// photo card. See AGENTS.md.
package photos

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/applelibrary"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"google.golang.org/protobuf/proto"
)

type Crawler struct {
	photosConfiguration PhotosConfiguration
	runCommandFlags     photosStepRunCommandFlags
}

type PhotosConfiguration struct {
	ApplePhotosLibraryPath          string  `toml:"apple_photos_library_path"`
	KnownPlacesFilePath             string  `toml:"known_places_file_path"`
	AppleMapSearchRadiusMetres      float64 `toml:"apple_map_search_radius_metres"`
	AppleMapSearchQuery             string  `toml:"apple_map_search_query"`
	OpenStreetMapSearchRadiusMetres float64 `toml:"openstreetmap_search_radius_metres"`
	OpenStreetMapSearchQuery        string  `toml:"openstreetmap_search_query"`
}

var (
	_ trawlkit.Trawler = (*Crawler)(nil)
	_ trawlkit.Updater = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		SearchMatchesResolvedPersonDisplayNames: true,
		RegisteredTrawler:                       trawlkit.NewRegisteredTrawlerIdentity("photos"),
		RegisteredTrawlerCommandName:            "photos",
		RegisteredTrawlerDisplayName:            "Photos",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Your Apple Photos library's database: what Photos already knows about each photo.",
			LeavesMachine:   "Nothing. Updates stay on your Mac.",
			NetworkRequests: "None. Updates use only local data.",
		},
	}
}

func (c *Crawler) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	loadedConfiguration := c.photosConfiguration
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &loadedConfiguration); err != nil {
		return fmt.Errorf("load Photos configuration: %w", err)
	}
	c.photosConfiguration = loadedConfiguration
	return nil
}

func (c *Crawler) applePhotosLibraryPath() (string, error) {
	if configuredPath := strings.TrimSpace(c.photosConfiguration.ApplePhotosLibraryPath); configuredPath != "" {
		return configuredPath, nil
	}
	return applelibrary.DefaultApplePhotosLibraryPath()
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			TrawlerCommandName:                       "run",
			TrawlerCommandHelpDescription:            "Run one step and print its exact input and output",
			TrawlerCommandPositionalArgumentNames:    []string{"STEP"},
			TrawlerCommandDiscoveryPlacement:         trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandChangesArchive:             true,
			RegisterTrawlerCommandFlags:              c.registerPhotosStepRunCommandFlags,
			TrawlerCommandArchiveAccessWithoutWrites: c.photosStepRunArchiveAccessWithoutWrites,
			ExecuteTrawlerCommand:                    c.runPhotosStepCommand,
		},
	}
}

func (c *Crawler) photosStepRunArchiveAccessWithoutWrites(positionalArguments []string) (trawlkit.TrawlerCommandArchiveAccess, bool) {
	if !c.runCommandFlags.runWithoutStoringOutput {
		return trawlkit.TrawlerCommandArchiveAccessDefault, false
	}
	if len(positionalArguments) == 1 && positionalArguments[0] == photospipeline.PhotosStepCommandName(photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY) {
		return trawlkit.TrawlerCommandArchiveAccessOptional, true
	}
	return trawlkit.TrawlerCommandArchiveAccessRequired, true
}

func (c *Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	applePhotosLibraryPath, err := c.applePhotosLibraryPath()
	if err != nil {
		return nil, err
	}
	photosArchive, err := photosarchive.UsePhotosArchive(ctx, req.OpenedTrawlerArchiveStore)
	if err != nil {
		return nil, err
	}
	photosPipeline := photospipeline.NewPhotosPipeline(photosArchive, applePhotosLibraryPath)
	stepRunResults, err := photosPipeline.RunPhotosUpdateChain(ctx, req.TrawlerCommandLog)
	if err != nil {
		return nil, err
	}
	storeCounts := photosPipeline.LastAppleLibrarySnapshotStoreCounts()
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("apple_library_stored", fmt.Sprintf(
			"photos_added=%d photos_updated=%d photos_unchanged=%d photos_marked_absent=%d trips_written=%d trips_removed=%d",
			storeCounts.PhotosAdded, storeCounts.PhotosUpdated, storeCounts.PhotosUnchanged, storeCounts.PhotosMarkedAbsent,
			storeCounts.TripsWritten, storeCounts.TripsRemoved))
	}
	return &updatecontract.TrawlerArchiveUpdateReport{
		ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(storeCounts.PhotosAdded)),
		ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(storeCounts.PhotosUpdated)),
		ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(uint64(storeCounts.PhotosMarkedAbsent)),
		PhotosStepsNotBuilt:                   photosStepNamesStoppedBecauseNotImplemented(stepRunResults),
	}, nil
}

func photosStepNamesStoppedBecauseNotImplemented(stepRunResults []photospipeline.PhotosStepRunResult) []photosstep.PhotosStepName {
	var notImplementedStepCommandNames []photosstep.PhotosStepName
	for _, stepRunResult := range stepRunResults {
		if stepRunResult.StopReason == photosstep.PhotosStepRunStopReason_PHOTOS_STEP_RUN_STOP_REASON_STEP_NOT_IMPLEMENTED {
			notImplementedStepCommandNames = append(notImplementedStepCommandNames, stepRunResult.PhotosStepName)
		}
	}
	return notImplementedStepCommandNames
}
