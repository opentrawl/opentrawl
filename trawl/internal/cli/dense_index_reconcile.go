package cli

import (
	"context"
	"io"

	"github.com/opentrawl/opentrawl/trawl/internal/densesearch"
	"github.com/opentrawl/opentrawl/trawlkit"
	appcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
)

func (r *Runtime) reconcileSemanticSearchIndex(ctx context.Context) (*appcontract.SemanticSearchIndexReconcileResponse, error) {
	sources := r.searchableRecordSources(discoverInstalledTrawlers(ctx))
	err := densesearch.ReconcileIndex(ctx, densesearch.BuildIndexOptions{
		StateRoot:                r.stateRoot,
		IndexPath:                densesearch.IndexPath(r.stateRoot),
		Sources:                  sources,
		WithSourceArchivesLocked: r.withSourceArchivesLocked,
		ProgressOutput:           io.Discard,
	})
	if err != nil {
		return nil, err
	}
	return &appcontract.SemanticSearchIndexReconcileResponse{SemanticSearchIndexIsCurrent: true}, nil
}

func (r *Runtime) withSourceArchivesLocked(action func() error) error {
	archiveBoundaryLock, err := acquireSemanticSearchArchiveBoundaryLock(r.stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = archiveBoundaryLock.Close() }()
	lock, err := acquireUpdateBatchLockWaiting(r.stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return action()
}

func (r *Runtime) searchableRecordSources(trawlers []InstalledTrawler) []densesearch.SearchableRecordSource {
	executor := r.trawlerExecutor()
	sources := make([]densesearch.SearchableRecordSource, 0, len(trawlers))
	for _, trawler := range trawlers {
		trawler := trawler
		if trawler.TrawlerDiscoveryError != nil || trawler.Trawler == nil {
			continue
		}
		if _, searchable := trawler.Trawler.(trawlkit.SearchableRecordExporter); !searchable {
			continue
		}
		sources = append(sources, densesearch.SearchableRecordSource{
			RegisteredTrawlerIdentity:    trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
			RegisteredTrawlerDisplayName: trawlerHumanName(trawler),
			ExportPage: func(
				ctx context.Context,
				request *searchablerecord.SearchableRecordExportRequest,
			) (*searchablerecord.TrawlerSearchableRecordExportPage, error) {
				return executor.ExportSearchableRecordPage(ctx, trawler.Trawler, request)
			},
		})
	}
	return sources
}
