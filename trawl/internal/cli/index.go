package cli

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

type IndexCmd struct {
	Status  IndexStatusCmd  `cmd:"" default:"withargs" help:"Show unified search index status and record counts"`
	Rebuild IndexRebuildCmd `cmd:"" help:"Rebuild the unified search index through trawler index exporters"`
}

type IndexStatusCmd struct{}

type IndexRebuildCmd struct {
	Trawler string `name:"trawler" placeholder:"TRAWLERS" help:"Only rebuild these trawlers (comma-separated)"`
}

func (c *IndexStatusCmd) Run(r *Runtime) error {
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	status, err := unifiedindex.ReadUnifiedIndexStatus(r.ctx, stateRoot)
	if err != nil {
		return err
	}
	trawlers := make([]render.UnifiedIndexTrawlerCountPresentation, 0, len(status.Trawlers))
	for _, t := range status.Trawlers {
		trawlers = append(trawlers, render.UnifiedIndexTrawlerCountPresentation{
			TrawlerIdentity:    t.TrawlerIdentity,
			TrawlerDisplayName: t.TrawlerDisplayName,
			RecordCount:        t.RecordCount,
			IndexCursor:        t.IndexCursor,
			IndexedAtUTC:       t.IndexedAtUTC,
		})
	}
	return r.output.WriteUnifiedIndexStatus(render.UnifiedIndexStatusPresentation{
		IndexPath:          status.IndexPath,
		Exists:             status.Exists,
		UpdatedAtUTC:       status.UpdatedAtUTC,
		TotalIndexedCount:  status.TotalIndexedCount,
		AnchorCount:        status.AnchorCount,
		MoneyMovementCount: status.MoneyMovementCount,
		TimeWindowCount:    status.TimeWindowCount,
		DatabaseByteCount:  status.DatabaseByteCount,
		Trawlers:           trawlers,
	})
}

func (c *IndexRebuildCmd) Run(r *Runtime) error {
	started := time.Now()
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	installed := discoverInstalledTrawlers(r.ctx)
	targets := installed
	if strings.TrimSpace(c.Trawler) != "" {
		selected, selErr := r.selectInstalledTrawlers(installed, splitTrawlerCSV(c.Trawler))
		if selErr != nil {
			return selErr
		}
		targets = selected
	}
	for _, t := range targets {
		if !trawlerExportsArchiveIndexChanges(t) {
			continue
		}
		if _, syncErr := trawlkit.SyncTrawlerArchiveIndex(r.ctx, stateRoot, t.Trawler, true); syncErr != nil {
			return render.CommandFailureFromTrawler(
				render.CommandFailureKindArchiveOrOperationFailed,
				trawlerHumanName(t)+": "+syncErr.Error(),
			)
		}
	}
	status, err := unifiedindex.ReadUnifiedIndexStatus(r.ctx, stateRoot)
	if err != nil {
		return err
	}
	trawlers := make([]render.UnifiedIndexTrawlerCountPresentation, 0, len(status.Trawlers))
	for _, t := range status.Trawlers {
		trawlers = append(trawlers, render.UnifiedIndexTrawlerCountPresentation{
			TrawlerIdentity:    t.TrawlerIdentity,
			TrawlerDisplayName: t.TrawlerDisplayName,
			RecordCount:        t.RecordCount,
			IndexCursor:        t.IndexCursor,
			IndexedAtUTC:       t.IndexedAtUTC,
		})
	}
	return r.output.WriteUnifiedIndexRebuildReport(render.UnifiedIndexRebuildPresentation{
		IndexPath:          status.IndexPath,
		TotalIndexedCount:  status.TotalIndexedCount,
		AnchorCount:        status.AnchorCount,
		MoneyMovementCount: status.MoneyMovementCount,
		TimeWindowCount:    status.TimeWindowCount,
		DatabaseByteCount:  status.DatabaseByteCount,
		TotalElapsed:       time.Since(started),
		Trawlers:           trawlers,
	})
}
