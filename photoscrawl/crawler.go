package photoscrawl

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/photoscrawl/internal/archive"
	"github.com/openclaw/photoscrawl/internal/photos"
)

const (
	ollamaCloudBaseURL = "https://ollama.com/api"
	ollamaAPIKeyEnv    = "OLLAMA_API_KEY"
	heartbeatEvery     = 30 * time.Second
)

type Crawler struct {
	cfg           Config
	classifyAll   bool
	classifyLimit trackedLimit
	classifyModel string
}

type Config struct {
	LibraryPath string `toml:"library_path"`
}

var (
	_ crawlkit.Crawler  = (*Crawler)(nil)
	_ crawlkit.Syncer   = (*Crawler)(nil)
	_ crawlkit.Searcher = (*Crawler)(nil)
	_ crawlkit.Opener   = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          "photoscrawl",
		Surface:     "photos",
		DisplayName: "Photos",
		Description: "Local-first, read-only Apple Photos archive crawler.",
		ShortRefs:   true,
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

func (c *Crawler) Verbs() []crawlkit.Verb {
	return []crawlkit.Verb{
		{
			Name:    "classify",
			Help:    "Write metadata, place and model-card observations.",
			Mutates: true,
			Flags:   c.classifyFlags,
			Run:     c.runClassify,
		},
	}
}

func (c *Crawler) classifyFlags(fs *flag.FlagSet) {
	c.classifyAll = false
	c.classifyLimit = trackedLimit{value: 100}
	c.classifyModel = ""
	fs.BoolVar(&c.classifyAll, "all", false, "classify all pending assets")
	fs.Var(&c.classifyLimit, "limit", "max pending assets to classify")
	fs.StringVar(&c.classifyModel, "model", "", "Ollama-API vision model for content observations; local or cloud")
}

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	paths := archivePaths(req)
	status, err := archive.Status(ctx, paths)
	if err != nil {
		return nil, err
	}
	return controlStatus(status, req.Paths.Config), nil
}

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	result, err := archive.Doctor(ctx, archivePaths(req), archive.DoctorOptions{LibraryPath: c.cfg.LibraryPath})
	if err != nil {
		return nil, err
	}
	checks := make([]crawlkit.Check, 0, len(result.Checks))
	for _, check := range result.Checks {
		checks = append(checks, crawlkit.Check{
			ID:      check.ID,
			State:   check.State,
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return &crawlkit.Doctor{Checks: checks}, nil
}

func (c *Crawler) Sync(ctx context.Context, req *crawlkit.Request) (*crawlkit.SyncReport, error) {
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
			Provider:    photos.NewProvider(),
		})
		return syncErr
	})
	if err != nil {
		return nil, err
	}
	reportProgress(req, "sync", int64(result.AssetsSeen), int64(result.AssetsSeen), "synced Photos library")
	if req.Log != nil {
		_ = req.Log.Info("sync_written", syncLogMessage(result))
	}
	return &crawlkit.SyncReport{
		Added:    int64(result.AssetsNew),
		Updated:  int64(result.AssetsChanged),
		Removed:  int64(result.PreviouslySeenMissing),
		Warnings: syncWarnings(result),
	}, nil
}

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	result, err := archive.Search(ctx, archivePaths(req), archive.SearchOptions{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  queryTime(query.After),
		Before: queryTime(query.Before),
	})
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	hits := make([]crawlkit.Hit, 0, len(result.Results))
	for _, hit := range result.Results {
		converted, err := searchHit(hit)
		if err != nil {
			return crawlkit.SearchResult{}, err
		}
		hits = append(hits, converted)
	}
	if req.Log != nil {
		_ = req.Log.Info("search_written", fmt.Sprintf("returned=%d total=%d truncated=%t", len(result.Results), result.TotalMatches, result.Truncated))
	}
	return crawlkit.SearchResult{
		Results:      hits,
		TotalMatches: result.TotalMatches,
		Truncated:    result.Truncated,
	}, nil
}

func (c *Crawler) runClassify(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return output.UsageError{Err: fmt.Errorf("classify takes flags only")}
	}
	limit, err := flags.Limit(c.classifyLimit.value, c.classifyLimit.set, c.classifyAll)
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
			All:         c.classifyAll,
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

func archivePaths(req *crawlkit.Request) archive.Paths {
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
	out := control.NewStatus("photoscrawl", status.Summary)
	out.State = status.State
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

func searchHit(hit archive.SearchHit) (crawlkit.Hit, error) {
	var capturedAt time.Time
	if timeText := strings.TrimSpace(hit.Time); timeText != "" {
		parsed, err := time.Parse(time.RFC3339, timeText)
		if err != nil {
			return crawlkit.Hit{}, fmt.Errorf("parse search hit time: %w", err)
		}
		capturedAt = parsed
	}
	return crawlkit.Hit{
		Ref:      hit.Ref,
		ShortRef: hit.ShortRef,
		Time:     capturedAt,
		Who:      hit.Who,
		Where:    hit.Where,
		Snippet:  hit.Snippet,
	}, nil
}

func queryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func reportProgress(req *crawlkit.Request, phase string, done, total int64, message string) {
	if req.Progress == nil {
		return
	}
	req.Progress(crawlkit.Progress{Phase: phase, Done: done, Total: total, Message: message})
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
		"provider=%s assets=%d new=%d changed=%d unchanged=%d missing=%d "+
			"queued_for_classify=%d queued_needs_download=%d classification_queue_pending=%d "+
			"marked_stale_model_assets=%d marked_stale_model_rows=%d "+
			"marked_stale_place_assets=%d marked_stale_place_rows=%d",
		result.Provider,
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
