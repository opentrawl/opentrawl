// Package photos is the read-only Apple Photos trawler. It runs four steps
// over one archive: read Apple's library, fetch each picture, look up the
// places near each camera spot and write each photo card. See AGENTS.md.
package photos

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/applelibrary"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/knownplacesfile"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photospipeline"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturefetch"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
)

type Crawler struct {
	photosConfiguration PhotosConfiguration
	runCommandFlags     photosStepRunCommandFlags
	photoListFlags      photoFieldListCommandFlags
}

type PhotosConfiguration struct {
	ApplePhotosLibraryPath string `toml:"apple_photos_library_path"`
	KnownPlacesFilePath    string `toml:"known_places_file_path"`
	// A local copy of the library, read before asking Apple Photos, which may
	// download from iCloud. See picturefetch.PictureMirrorFolder.
	PictureMirrorFolderPath string `toml:"picture_mirror_folder_path"`
	// A self-hosted Photon, for example http://localhost:2322, keeps camera
	// positions on your own machines and answers much faster. When set, trawl
	// update also runs look_up_places_near_spot automatically.
	PhotonBaseURL string `toml:"photon_base_url"`
	// Maximum long-edge pixel limit for upright JPEGs written by fetch_picture.
	// Zero uses picturefetch.DefaultPictureLongEdgePixelLimit (2576). Changing
	// this value re-fetches stored pictures and replays cards on --recard.
	PictureLongEdgePixelLimit int32 `toml:"picture_long_edge_pixel_limit"`
	// The hosted vision model that writes photo cards: card_model_provider is
	// "antigravity_acp", "anthropic_api", "claude_command_line", "ollama_cloud"
	// or "codex_command_line", card_model_name is the provider's model name, and
	// card_model_effort is the optional reasoning effort (for example "low",
	// "medium" or "high"). API-key providers read card_model_api_key_file, or
	// OLLAMA_API_KEY / ANTHROPIC_API_KEY.
	CardModelProvider   string `toml:"card_model_provider"`
	CardModelName       string `toml:"card_model_name"`
	CardModelEffort     string `toml:"card_model_effort"`
	CardModelAPIKeyFile string `toml:"card_model_api_key_file"`
}

var (
	_ trawlkit.Trawler                    = (*Crawler)(nil)
	_ trawlkit.Updater                    = (*Crawler)(nil)
	_ trawlkit.ArchiveIndexChangeExporter = (*Crawler)(nil)
	_ trawlkit.PluginCapabilityDeclarator = (*Crawler)(nil)
)

const photosStepRunWithoutWritesMaximumExecutionTime = 24 * time.Hour

func New() *Crawler {
	return &Crawler{}
}

func (*Crawler) DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return &plugincontract.PluginDeclaredCapabilities{
		DeclaredReadPaths:                []string{"~/Pictures/Photos Library.photoslibrary"},
		SupportsArchiveIndexChangeExport: true,
	}
}

func (c *Crawler) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	archive, err := c.readPhotoArchive(ctx, req)
	if err != nil {
		return err
	}
	return archive.ExportArchiveIndexChanges(ctx, sinceOpaqueCursor, emit)
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		SearchMatchesResolvedPersonDisplayNames: true,
		RegisteredTrawler:                       trawlkit.NewRegisteredTrawlerIdentity("photos"),
		RegisteredTrawlerCommandName:            "photos",
		RegisteredTrawlerDisplayName:            "Photos",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Your Apple Photos library's database: what Photos already knows about each photo. When you fetch pictures, each photo's picture.",
			LeavesMachine:   "When place lookup runs, camera positions rounded to about 55 metres go to the Photon service (photon.komoot.io unless photon_base_url names another). When you write photo cards, each photo's picture and its facts (time, place, camera position, nearby places, named people, Apple's text) go to the card model you configured.",
			NetworkRequests: "When you fetch pictures, Apple Photos downloads from iCloud any picture not on your Mac. Place lookup asks Photon for nearby named places once per camera spot. Writing a photo card makes one call to the configured card model per photo. Archive commands are local.",
		},
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:              "photo.on.rectangle.angled",
			AccentColor:             "#FF2D55",
			BundleIdentifier:        "com.apple.Photos",
			ArtworkBundleIdentifier: "com.apple.Photos",
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

// fetchPictureStep stores pictures in the cache beside the archive unless the
// run stores nothing.
func (c *Crawler) fetchPictureStep(photosArchivePath string, storesStepOutputs bool) picturefetch.FetchPictureStep {
	fetchPicture := picturefetch.FetchPictureStep{
		PictureLongEdgePixelLimit: c.photosConfiguration.PictureLongEdgePixelLimit,
	}
	if pictureMirrorFolderPath := strings.TrimSpace(c.photosConfiguration.PictureMirrorFolderPath); pictureMirrorFolderPath != "" {
		fetchPicture.PictureMirrorFolder = picturefetch.NewPictureMirrorFolder(config.ExpandHome(pictureMirrorFolderPath))
	}
	if storesStepOutputs {
		fetchPicture.PictureCache = picturecache.NewPictureCache(picturecache.PictureCacheFolderPath(photosArchivePath))
	}
	return fetchPicture
}

// photoCardWriting reads the known-places file and makes the configured card
// model client. With no card model configured its client is nil, and only
// write_photo_card fails.
func (c *Crawler) photoCardWriting(photosArchivePath string) (photospipeline.PhotoCardWriting, error) {
	cardWriting := photospipeline.PhotoCardWriting{FetchPictureIntoCache: c.fetchPictureStep(photosArchivePath, true)}
	if path := c.knownPlacesFilePath(); path != "" {
		knownPlacesFile, err := knownplacesfile.ReadKnownPlacesFile(config.ExpandHome(path))
		if err != nil {
			return cardWriting, err
		}
		cardWriting.KnownPlacesFile = knownPlacesFile
	}
	cardModelName := strings.TrimSpace(c.photosConfiguration.CardModelName)
	cardModelEffort := strings.TrimSpace(c.photosConfiguration.CardModelEffort)
	switch provider := strings.TrimSpace(c.photosConfiguration.CardModelProvider); provider {
	case "":
		return cardWriting, nil
	case "ollama_cloud":
		apiKey, err := c.readCardModelAPIKey("OLLAMA_API_KEY")
		if err != nil {
			return cardWriting, err
		}
		if apiKey == "" || cardModelName == "" {
			return cardWriting, render.PhotosCardModelConfigurationIsIncomplete(provider)
		}
		cardWriting.CardModelClient = photocard.NewOllamaCloudCardModelClient(cardModelName, apiKey)
	case "codex_command_line":
		if cardModelName == "" {
			return cardWriting, render.PhotosCardModelConfigurationIsIncomplete(provider)
		}
		cardWriting.CardModelClient = photocard.NewCodexCommandLineCardModelClient(cardModelName, cardModelEffort)
	case "antigravity_acp":
		if cardModelName == "" {
			return cardWriting, render.PhotosCardModelConfigurationIsIncomplete(provider)
		}
		cardWriting.CardModelClient = photocard.NewAntigravityACPCardModelClient(cardModelName, cardModelEffort)
	case "anthropic_api":
		apiKey, err := c.readCardModelAPIKey("ANTHROPIC_API_KEY")
		if err != nil {
			return cardWriting, err
		}
		if apiKey == "" || cardModelName == "" {
			return cardWriting, render.PhotosCardModelConfigurationIsIncomplete(provider)
		}
		cardWriting.CardModelClient = photocard.NewAnthropicAPICardModelClient(cardModelName, cardModelEffort, apiKey)
	case "claude_command_line":
		if cardModelName == "" {
			return cardWriting, render.PhotosCardModelConfigurationIsIncomplete(provider)
		}
		cardWriting.CardModelClient = photocard.NewClaudeCommandLineCardModelClient(cardModelName, cardModelEffort)
	default:
		return cardWriting, render.PhotosCardModelProviderIsUnknown(provider)
	}
	return cardWriting, nil
}

func (c *Crawler) readCardModelAPIKey(environmentVariableName string) (string, error) {
	if keyFilePath := strings.TrimSpace(c.photosConfiguration.CardModelAPIKeyFile); keyFilePath != "" {
		keyFileBytes, err := os.ReadFile(config.ExpandHome(keyFilePath))
		if err != nil {
			return "", render.PhotosCardModelAPIKeyFileCannotBeRead(keyFilePath, err)
		}
		return strings.TrimSpace(string(keyFileBytes)), nil
	}
	return strings.TrimSpace(os.Getenv(environmentVariableName)), nil
}

func (c *Crawler) hasConfiguredPhotonBaseURL() bool {
	return strings.TrimSpace(c.photosConfiguration.PhotonBaseURL) != ""
}

func (c *Crawler) photonBaseURL() string {
	if configuredURL := strings.TrimSpace(c.photosConfiguration.PhotonBaseURL); configuredURL != "" {
		return configuredURL
	}
	return placelookup.PublicPhotonBaseURL
}

// knownPlacesFilePath is empty when no known-places file is configured.
func (c *Crawler) knownPlacesFilePath() string {
	return strings.TrimSpace(c.photosConfiguration.KnownPlacesFilePath)
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{TrawlerCommandName: "photos", TrawlerCommandRunsAtNamespaceRoot: true, TrawlerCommandHelpDescription: "List photos by capture date, place, person, album, camera or favourite", TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp, RegisterTrawlerCommandFlags: c.registerPhotoFieldListCommandFlags, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, ExecuteTrawlerCommand: c.listPhotoFactsCommand},
		{TrawlerCommandName: "days", TrawlerCommandHelpDescription: "List local days and nearby photo stays with their start and end times", TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp, RegisterTrawlerCommandFlags: c.registerPhotoDaysCommandFlags, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, ExecuteTrawlerCommand: c.listPhotoLocalDaysCommand},
		{TrawlerCommandName: "inspect", TrawlerCommandHelpDescription: "Read one photo layer by link or Apple photo UUID: apple-facts, picture-metadata, nearby-places, card-model-input or card", TrawlerCommandPositionalArgumentNames: []string{"PHOTO", "[LAYER]"}, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessRequired, ExecuteTrawlerCommand: c.inspectPhotoLayerCommand},
		{
			TrawlerCommandName:                    "run",
			TrawlerCommandHelpDescription:         "Run one step and print its exact input and output",
			TrawlerCommandPositionalArgumentNames: []string{"STEP"},
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandChangesArchive:          true,
			// A run that stores nothing still asks Apple Photos, Photon or the
			// card model about each item, so it is not held to the two-minute
			// limit of commands that only read the archive.
			TrawlerCommandMaximumExecutionTime:       photosStepRunWithoutWritesMaximumExecutionTime,
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
	photosPipeline := photospipeline.NewPhotosPipeline(
		photosArchive,
		applePhotosLibraryPath,
		c.fetchPictureStep(req.TrawlerArchivePaths.TrawlerArchivePath, true),
		c.photonBaseURL(),
		c.hasConfiguredPhotonBaseURL(),
		photospipeline.PhotoCardWriting{},
	)
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
