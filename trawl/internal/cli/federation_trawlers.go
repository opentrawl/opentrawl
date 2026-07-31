package cli

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) federationStatusTrawlers(installedTrawlers []InstalledTrawler) []federation.StatusTrawler {
	trawlers := make([]federation.StatusTrawler, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, Run: func(context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS) {
			trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, SkipReason: "Status is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, Run: func(ctx context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure) {
			if installedTrawler.Trawler == nil {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, errors.New("status command has no trawler"))
			}
			status, err := r.trawlerExecutor().Status(ctx, installedTrawler.Trawler)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, err)
			}
			return status, nil
		}})
	}
	return trawlers
}

func (r *Runtime) federationSearchTrawlers(installedTrawlers []InstalledTrawler) []federation.SearchTrawler {
	trawlers := make([]federation.SearchTrawler, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, Run: func(context.Context, trawlkit.Query) (*searchv1.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationv1.TrawlerOperationFailure) {
				return nil, nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH) {
			trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, SkipReason: "Search is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, Run: func(ctx context.Context, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationv1.TrawlerOperationFailure) {
			_, ok := installedTrawler.Trawler.(trawlkit.Searcher)
			if !ok {
				return nil, nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, errors.New("declared search command has no searcher"))
			}
			result, localShortReferencesByCanonicalRecordReference, err := r.trawlerExecutor().Search(ctx, installedTrawler.Trawler, query)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, err)
			}
			return result, localShortReferencesByCanonicalRecordReference, nil
		}})
	}
	return trawlers
}

func (r *Runtime) federationOpenTrawlers(installedTrawlers []InstalledTrawler) []federation.OpenTrawler {
	trawlers := make([]federation.OpenTrawler, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.OpenTrawler{Manifest: manifest, Run: func(
				context.Context,
				*trawlkit.LocalTrawlerShortReference,
				*trawlkit.RecordAnchorIdentifier,
			) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN) {
			trawlers = append(trawlers, federation.OpenTrawler{Manifest: manifest, SkipReason: "Open is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.OpenTrawler{Manifest: manifest, Run: func(
			ctx context.Context,
			localShortReference *trawlkit.LocalTrawlerShortReference,
			recordAnchor *trawlkit.RecordAnchorIdentifier,
		) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure) {
			if _, ok := installedTrawler.Trawler.(trawlkit.RecordOpener); !ok {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, errors.New("declared open command has no record opener"))
			}
			record, err := r.trawlerExecutor().OpenRecord(
				ctx,
				installedTrawler.Trawler,
				localShortReference,
				recordAnchor,
			)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, err)
			}
			return record, nil
		}})
	}
	return trawlers
}

func cloneRegisteredTrawlerManifest(manifest *federationv1.RegisteredTrawlerManifest) *federationv1.RegisteredTrawlerManifest {
	if manifest == nil {
		return &federationv1.RegisteredTrawlerManifest{}
	}
	return proto.Clone(manifest).(*federationv1.RegisteredTrawlerManifest)
}
