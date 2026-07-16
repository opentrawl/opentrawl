package photoscrawl

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
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const (
	ollamaCloudBaseURL = "https://ollama.com/v1"
	ollamaAPIKeyEnv    = "OLLAMA_API_KEY"
	heartbeatEvery     = 30 * time.Second
)

type Crawler struct {
	cfg                        Config
	snapshotProvider           photos.Provider
	classifyLimit              trackedLimit
	classifyModel              string
	currentStillAsset          string
	currentStillSource         string
	currentStillExcludedAssets []string
}

type Config struct {
	LibraryPath string          `toml:"library_path"`
	CardModel   CardModelConfig `toml:"card_model"`
}

var (
	_ trawlkit.Crawler             = (*Crawler)(nil)
	_ trawlkit.Syncer              = (*Crawler)(nil)
	_ trawlkit.Searcher            = (*Crawler)(nil)
	_ trawlkit.Opener              = (*Crawler)(nil)
	_ trawlkit.ReadArchivePreparer = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) PrepareReadArchive(ctx context.Context, path string) error {
	return archiveReadCommandError(archive.PrepareArchive(ctx, path))
}

func (c *Crawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          "photos",
		Surface:     "photos",
		DisplayName: "Photos",
		Headlines:   []string{"photos"},
		Config:      &c.cfg,
		Privacy: control.Privacy{
			ExportsSecrets: false,
			LocalOnlyScopes: []string{
				"apple-photos",
				"sqlite",
				"media-metadata",
				"location-observations",
				"model-observations",
			},
		},
	}
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{
			Name:    "classify",
			Help:    "Write metadata, place and model-card observations.",
			Mutates: true,
			Flags:   c.classifyFlags,
			Run:     c.runClassify,
		},
		{
			Name:      "select-card-input-ready",
			Help:      "Select one PhotoKit-ready image before checked media acquisition.",
			Mutates:   false,
			Secondary: true,
			Store:     trawlkit.StoreNone,
			Flags:     c.currentStillReadinessFlags,
			Run:       c.runCardInputReadiness,
		},
		{
			Name:      "acquire-current-still",
			Help:      "Acquire one checked current still for an exact asset.",
			Mutates:   true,
			Secondary: true,
			Store:     trawlkit.StoreNone,
			Flags:     c.currentStillFlags,
			Run:       c.runCurrentStillAcquire,
		},
		{
			Name:      "prepare-card",
			Help:      "Prepare one Photos card request for review.",
			Args:      []string{"PHOTO"},
			Mutates:   true,
			Secondary: true,
			Store:     trawlkit.StoreNone,
			Run:       c.runPrepareCard,
		},
		{
			Name:      "create-card",
			Help:      "Create one approved Photos card.",
			Args:      []string{"APPROVAL"},
			Mutates:   true,
			Secondary: true,
			Store:     trawlkit.StoreNone,
			Run:       c.runCreateCard,
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
	fs.StringVar(&c.currentStillAsset, "asset", "", "exact Photos asset ID")
	fs.StringVar(&c.currentStillSource, "source-library", "", "exact Photos source library ID")
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	setup := c.photosSetupRequirements(ctx)
	paths := archivePaths(req)
	status, err := archive.Status(ctx, paths)
	if err != nil {
		out := control.NewStatus("photos", "Photos archive status cannot be read.")
		out.State = "error"
		out.ConfigPath = req.Paths.Config
		out.DatabasePath = req.Paths.Archive
		out.Errors = []string{err.Error()}
		out.SetupRequirements = setup
		return &out, nil
	}
	out := controlStatus(status, req.Paths.Config)
	out.SetupRequirements = setup
	return out, nil
}

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	libraryPath := strings.TrimSpace(c.cfg.LibraryPath)
	if libraryPath == "" {
		var err error
		libraryPath, err = archive.DefaultPhotosLibraryPath()
		if err != nil {
			return nil, err
		}
	}
	reportProgress(req, "sync", 0, 0, "syncing Photos library")
	var result archive.SyncResult
	err := withHeartbeat(ctx, func() {
		reportProgress(req, "sync", 0, 0, "syncing Photos library")
	}, func() error {
		var syncErr error
		result, syncErr = archive.SyncWithStore(ctx, req.Store, archivePaths(req), archive.SyncOptions{
			LibraryPath: libraryPath,
			Provider:    c.provider(),
		})
		return syncErr
	})
	if err != nil {
		return nil, syncCommandError(err)
	}
	reportProgress(req, "sync", int64(result.AssetsSeen), int64(result.AssetsSeen), "synced Photos library")
	if req.Log != nil {
		_ = req.Log.Info("sync_written", syncLogMessage(result))
	}
	return &trawlkit.SyncReport{
		Added:    int64(result.AssetsNew),
		Updated:  int64(result.AssetsChanged),
		Removed:  int64(result.PreviouslySeenMissing),
		Warnings: syncWarnings(result),
	}, nil
}

func (c *Crawler) provider() photos.Provider {
	if c.snapshotProvider != nil {
		return c.snapshotProvider
	}
	return photos.NewProvider()
}

func syncCommandError(err error) error {
	var incomplete *archive.SnapshotIncompleteError
	if !errors.As(err, &incomplete) {
		return err
	}
	return commandError{
		Code:    "snapshot_incomplete",
		Message: incomplete.Error(),
		Remedy:  "restore complete Photos access or wait for the snapshot to finish, then rerun sync",
	}
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	result, err := archive.SearchWithStore(ctx, req.Store, archive.SearchOptions{
		Query:         query.Text,
		Limit:         query.Limit,
		BoundedTotals: query.BoundedTotals,
		After:         queryTime(query.After),
		Before:        queryTime(query.Before),
	})
	if err != nil {
		return trawlkit.SearchResult{}, archiveReadCommandError(err)
	}
	hits := make([]trawlkit.Hit, 0, len(result.Results))
	for _, hit := range result.Results {
		converted, err := searchHit(hit)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		hits = append(hits, converted)
	}
	if req.Log != nil {
		_ = req.Log.Info("search_written", fmt.Sprintf("returned=%d total=%d truncated=%t", len(result.Results), result.TotalMatches, result.Truncated))
	}
	return trawlkit.SearchResult{
		Results:           hits,
		TotalMatches:      result.TotalMatches,
		TotalIsLowerBound: result.TotalIsLowerBound,
		Truncated:         result.Truncated,
	}, nil
}

func (c *Crawler) runClassify(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return output.UsageError{Err: fmt.Errorf("classify takes flags only")}
	}
	limit, err := flags.Limit(c.classifyLimit.value, c.classifyLimit.set)
	if err != nil {
		return output.UsageError{Err: err}
	}
	reportProgress(req, "classify", 0, int64(limit), "classifying queued photos")
	var result archive.ClassifyResult
	err = withHeartbeat(ctx, func() {
		reportProgress(req, "classify", 0, int64(limit), "classifying queued photos")
	}, func() error {
		var classifyErr error
		result, classifyErr = archive.ClassifyWithStore(ctx, req.Store, archivePaths(req), archive.ClassifyOptions{
			Limit:       limit,
			Model:       c.classifyModel,
			ModelURL:    ollamaCloudBaseURL,
			ModelKeyEnv: ollamaAPIKeyEnv,
			LogSink:     req.Log,
		})
		return classifyErr
	})
	if err != nil {
		return err
	}
	reportProgress(req, "classify", int64(result.Processed), int64(result.Processed), "classified queued photos")
	if req.Log != nil {
		_ = req.Log.Info("classify_written", fmt.Sprintf("processed=%d metadata=%d content=%d failures=%d", result.Processed, result.MetadataClassified, result.ContentClassified, result.ContentClassificationFailures))
	}
	return output.Write(req.Out, req.Format, "classify", result)
}

func (c *Crawler) runCurrentStillAcquire(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return output.UsageError{Err: errors.New("acquire-current-still takes flags only")}
	}
	if strings.TrimSpace(c.currentStillAsset) == "" || strings.TrimSpace(c.currentStillSource) == "" {
		return output.UsageError{Err: errors.New("acquire-current-still requires --asset and --source-library")}
	}
	result, err := archive.AcquireCardInputCurrentStill(ctx, archive.CardInputCurrentStillOptions{
		CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{
			ArchivePath:     req.Paths.Archive,
			SourceLibraryID: strings.TrimSpace(c.currentStillSource),
		},
		CacheDir:     archivePaths(req).CacheDir,
		AssetID:      strings.TrimSpace(c.currentStillAsset),
		AllowNetwork: false,
	})
	if err != nil {
		return err
	}
	return output.Write(req.Out, req.Format, "current_still_acquisition", result)
}

func (c *Crawler) runCardInputReadiness(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return output.UsageError{Err: errors.New("select-card-input-ready takes flags only")}
	}
	if strings.TrimSpace(c.currentStillSource) == "" {
		return output.UsageError{Err: errors.New("select-card-input-ready requires --source-library")}
	}
	result, err := archive.SelectCardInputReadyAsset(ctx, archive.CardInputReadinessOptions{
		CardInputAuditInventoryOptions: archive.CardInputAuditInventoryOptions{
			ArchivePath:     req.Paths.Archive,
			SourceLibraryID: strings.TrimSpace(c.currentStillSource),
		},
		ExcludedAssetIDs: append([]string(nil), c.currentStillExcludedAssets...),
	})
	if err != nil {
		return err
	}
	return output.Write(req.Out, req.Format, "card_input_readiness", result)
}

func archivePaths(req *trawlkit.Request) archive.Paths {
	base := filepath.Dir(req.Paths.Archive)
	return archive.Paths{
		ConfigPath: req.Paths.Config,
		DataDir:    base,
		Database:   req.Paths.Archive,
		CacheDir:   filepath.Join(base, "cache"),
		LogDir:     req.Paths.Logs,
		ShareDir:   filepath.Join(base, "share"),
	}
}

func controlStatus(status archive.StatusResult, configPath string) *control.Status {
	out := control.NewStatus("photos", status.Summary)
	out.State = status.State
	if out.State == "ok" {
		out.Summary = "Recently synced."
	}
	out.ConfigPath = configPath
	out.DatabasePath = status.DatabasePath
	out.DatabaseBytes = status.DatabaseBytes
	out.LastImportAt = status.LastImportAt
	out.Counts = append([]control.Count(nil), status.Counts...)
	out.Databases = append([]control.Database(nil), status.Databases...)
	if status.Freshness != nil && status.Freshness.LastSync != "" {
		out.LastSyncAt = status.Freshness.LastSync
	}
	return &out
}

func searchHit(hit archive.SearchHit) (trawlkit.Hit, error) {
	var capturedAt time.Time
	if timeText := strings.TrimSpace(hit.Time); timeText != "" {
		parsed, err := time.Parse(time.RFC3339, timeText)
		if err != nil {
			return trawlkit.Hit{}, fmt.Errorf("parse search hit time: %w", err)
		}
		capturedAt = parsed
	}
	title := strings.TrimSpace(hit.Title)
	if title == "" {
		title = "Photo"
	}
	evidence := photoSearchEvidence(hit.Matches)
	if len(evidence) == 0 {
		return trawlkit.Hit{}, fmt.Errorf("photo search hit has no matched evidence")
	}
	return trawlkit.Hit{
		Ref:      hit.Ref,
		ShortRef: hit.ShortRef,
		Time:     capturedAt,
		AnchorID: hit.AnchorID,
		Summary:  trawlkit.ResultSummary{Title: title, Subtitle: hit.Where},
		Archive:  photoArchiveContext(hit.Matches),
		Evidence: evidence,
	}, nil
}

func photoArchiveContext(matches []archive.SearchMatch) []trawlkit.ArchiveContext {
	for _, match := range matches {
		if match.Field == "album" {
			return []trawlkit.ArchiveContext{{Kind: "album", Label: "In album"}}
		}
	}
	return []trawlkit.ArchiveContext{{Kind: "library", Label: "Photos library"}}
}

func photoSearchEvidence(matches []archive.SearchMatch) []trawlkit.EvidenceFragment {
	evidence := make([]trawlkit.EvidenceFragment, 0, len(matches))
	labels := map[string]string{
		"filename": "Original filename", "album": "Album", "media": "Media type",
		"summary": "Photo summary", "description": "Photo description", "ocr": "Text in photo",
		"asset": "Asset details", "metadata": "Photo signal", "place": "Place", "address": "Address",
		"known-place": "Known place", "venue": "Venue",
	}
	for _, match := range matches {
		runs := make([]trawlkit.TextRun, 0, len(match.Runs))
		for _, run := range match.Runs {
			runs = append(runs, trawlkit.TextRun{Text: run.Text, Matched: run.Matched})
		}
		evidence = append(evidence, trawlkit.EvidenceFragment{Label: labels[match.Field], Field: &trawlkit.FieldEvidence{Name: match.Field, Value: runs}})
	}
	return evidence
}

func queryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func reportProgress(req *trawlkit.Request, phase string, done, total int64, message string) {
	if req.Progress == nil {
		return
	}
	req.Progress(trawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
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

func syncWarnings(result archive.SyncResult) []string {
	var warnings []string
	warnings = addStaleWarning(warnings, "marked_stale_model_assets", result.MarkedStaleModelAssets)
	warnings = addStaleWarning(warnings, "marked_stale_model_rows", result.MarkedStaleModelRows)
	warnings = addStaleWarning(warnings, "marked_stale_place_assets", result.MarkedStalePlaceAssets)
	warnings = addStaleWarning(warnings, "marked_stale_place_rows", result.MarkedStalePlaceRows)
	return warnings
}

func addStaleWarning(warnings []string, field string, value int) []string {
	if value == 0 {
		return warnings
	}
	return append(warnings, field+"="+strconv.Itoa(value))
}

func syncLogMessage(result archive.SyncResult) string {
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
