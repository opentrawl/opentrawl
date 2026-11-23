package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/semantic"
)

type SemanticCmd struct {
	Status  SemanticStatusCmd  `cmd:"" default:"withargs" help:"Show local semantic search index status, cached model weights, and helper state"`
	Enable  SemanticEnableCmd  `cmd:"" help:"Download verified local embedding weights, enable hybrid semantic search, and build an initial index"`
	Build   SemanticBuildCmd   `cmd:"" help:"Build or incrementally update disposable semantic.db from index.db"`
	Disable SemanticDisableCmd `cmd:"" help:"Disable hybrid semantic search and optionally remove disposable semantic.db"`
}

type SemanticStatusCmd struct{}

type SemanticEnableCmd struct {
	Window    string `name:"window" default:"60d" placeholder:"DURATION" help:"Initial history window to embed alongside all Notes (for example 60d, 180d, or all)"`
	SkipBuild bool   `name:"skip-build" help:"Verify weights and enable semantic search without running the initial build"`
}

type SemanticBuildCmd struct {
	Window  string `name:"window" default:"60d" placeholder:"DURATION" help:"History window to include alongside all Notes (for example 60d, 180d, or all)"`
	Trawler string `name:"trawler" placeholder:"TRAWLERS" help:"Limit semantic build to these trawlers (comma-separated)"`
	Rebuild bool   `name:"rebuild" help:"Re-embed all selected chunks from scratch"`
}

type SemanticDisableCmd struct {
	DeleteIndex bool `name:"delete-index" help:"Remove disposable semantic.db from disk"`
}

func (c *SemanticStatusCmd) Run(r *Runtime) error {
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	status, err := semantic.ReadSemanticIndexStatus(r.ctx, stateRoot)
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	return r.output.WriteSemanticIndexStatus(toSemanticStatusPresentation(status))
}

func (c *SemanticEnableCmd) Run(r *Runtime) error {
	windowDuration, err := parseSemanticWindowFlag(c.Window)
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, err.Error())
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	modelDir, err := semantic.ResolveDefaultModelCacheDirectory()
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	if _, err := semantic.EnsureDefaultModelDownloaded(r.ctx, modelDir); err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	if err := semantic.SetSemanticSearchEnabled(r.ctx, stateRoot, true); err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	if c.SkipBuild {
		status, err := semantic.ReadSemanticIndexStatus(r.ctx, stateRoot)
		if err != nil {
			return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
		}
		return r.output.WriteSemanticIndexStatus(toSemanticStatusPresentation(status))
	}
	installed := discoverInstalledTrawlers(r.ctx)
	if err := r.ensureUnifiedIndexBootstrappedForTrawlers(r.ctx, installed); err != nil {
		return err
	}
	report, err := semantic.BuildOrUpdateSemanticIndex(r.ctx, stateRoot, semantic.SemanticBuildOptions{
		WindowDuration: windowDuration,
	})
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	return r.output.WriteSemanticIndexBuildReport(toSemanticBuildPresentation(report))
}

func (c *SemanticBuildCmd) Run(r *Runtime) error {
	windowDuration, err := parseSemanticWindowFlag(c.Window)
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, err.Error())
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	installed := discoverInstalledTrawlers(r.ctx)
	var trawlerFilter []string
	if strings.TrimSpace(c.Trawler) != "" {
		selected, selErr := r.selectInstalledTrawlers(installed, splitTrawlerCSV(c.Trawler))
		if selErr != nil {
			return selErr
		}
		for _, t := range selected {
			trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
		}
	}
	if err := r.ensureUnifiedIndexBootstrappedForTrawlers(r.ctx, installed); err != nil {
		return err
	}
	report, err := semantic.BuildOrUpdateSemanticIndex(r.ctx, stateRoot, semantic.SemanticBuildOptions{
		WindowDuration:  windowDuration,
		Trawlers:        trawlerFilter,
		ForceFullRehash: c.Rebuild,
	})
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	return r.output.WriteSemanticIndexBuildReport(toSemanticBuildPresentation(report))
}

func (c *SemanticDisableCmd) Run(r *Runtime) error {
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	_ = semantic.NewSemanticHelperClient(stateRoot).ShutdownIfRunning(r.ctx)
	if c.DeleteIndex {
		if err := semantic.DeleteSemanticDatabase(stateRoot); err != nil {
			return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
		}
		return r.output.WriteSemanticSearchDisabled(true)
	}
	if err := semantic.SetSemanticSearchEnabled(r.ctx, stateRoot, false); err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindArchiveOrOperationFailed, err.Error())
	}
	return r.output.WriteSemanticSearchDisabled(false)
}

func parseSemanticWindowFlag(raw string) (time.Duration, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" || s == "all" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid --window %q (use e.g. 60d, 180d, or all)", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		weeks, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil || weeks <= 0 {
			return 0, fmt.Errorf("invalid --window %q (use e.g. 60d, 180d, or all)", raw)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --window %q (use e.g. 60d, 180d, or all)", raw)
	}
	return d, nil
}

func toSemanticStatusPresentation(status semantic.SemanticIndexStatus) render.SemanticIndexStatusPresentation {
	trawlers := make([]render.SemanticIndexTrawlerCountPresentation, 0, len(status.Trawlers))
	for _, t := range status.Trawlers {
		trawlers = append(trawlers, render.SemanticIndexTrawlerCountPresentation{
			TrawlerIdentity:    t.TrawlerIdentity,
			TrawlerDisplayName: t.TrawlerDisplayName,
			ChunkCount:         t.ChunkCount,
		})
	}
	return render.SemanticIndexStatusPresentation{
		DatabaseExists:        status.DatabaseExists,
		SemanticSearchEnabled: status.SemanticSearchEnabled,
		ModelIdentifier:       status.ModelIdentifier,
		ModelWeightsCached:    status.ModelWeightsCached,
		ModelWeightsByteCount: status.ModelWeightsByteCount,
		HelperBinaryFound:     status.HelperBinaryFound,
		HelperRunning:         status.HelperRunning,
		HelperModelLoaded:     status.HelperModelLoaded,
		HelperActiveMetalByte: status.HelperActiveMetalByte,
		ChunkerSchema:         status.ChunkerSchema,
		EmbeddingDimensions:   status.EmbeddingDimensions,
		TotalChunkCount:       status.TotalChunkCount,
		DatabaseByteCount:     status.DatabaseByteCount,
		UpdatedAtUTC:          status.UpdatedAtUTC,
		Trawlers:              trawlers,
	}
}

func toSemanticBuildPresentation(report semantic.SemanticBuildReport) render.SemanticIndexBuildPresentation {
	trawlers := make([]render.SemanticIndexTrawlerCountPresentation, 0, len(report.Trawlers))
	for _, t := range report.Trawlers {
		trawlers = append(trawlers, render.SemanticIndexTrawlerCountPresentation{
			TrawlerIdentity:    t.TrawlerIdentity,
			TrawlerDisplayName: t.TrawlerDisplayName,
			ChunkCount:         t.ChunkCount,
		})
	}
	return render.SemanticIndexBuildPresentation{
		ModelIdentifier:     report.ModelIdentifier,
		TotalChunksInIndex:  report.TotalChunksInIndex,
		ChunksNewlyEmbedded: report.ChunksNewlyEmbedded,
		ChunksUnchanged:     report.ChunksUnchanged,
		ChunksRemoved:       report.ChunksRemoved,
		Elapsed:             report.Elapsed,
		DatabaseByteCount:   report.DatabaseByteCount,
		Trawlers:            trawlers,
	}
}
