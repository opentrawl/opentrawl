package cli

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/proto"
)

func (r *Runtime) federationStatusTrawlers(installedTrawlers []InstalledTrawler) []federation.StatusTrawler {
	trawlers := make([]federation.StatusTrawler, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, Run: func(context.Context) (*status.TrawlerStatusResponse, *federationcontract.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS) {
			trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, SkipReason: "Status is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.StatusTrawler{Manifest: manifest, Run: func(ctx context.Context) (*status.TrawlerStatusResponse, *federationcontract.TrawlerOperationFailure) {
			if installedTrawler.Trawler == nil {
				return nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, errors.New("status command has no trawler"))
			}
			status, err := r.trawlerExecutor().Status(ctx, installedTrawler.Trawler)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, err)
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
			trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, Run: func(context.Context, trawlkit.Query) (*search.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationcontract.TrawlerOperationFailure) {
				return nil, nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH) {
			trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, SkipReason: "Search is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.SearchTrawler{Manifest: manifest, Run: func(ctx context.Context, query trawlkit.Query) (*search.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationcontract.TrawlerOperationFailure) {
			result, localShortReferencesByCanonicalRecordReference, err := r.trawlerExecutor().Search(ctx, installedTrawler.Trawler, query)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, err)
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
			) (*open.OpenRecord, *federationcontract.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !supportsSharedTrawlerOperation(installedTrawler, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN) {
			trawlers = append(trawlers, federation.OpenTrawler{Manifest: manifest, SkipReason: "Open is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.OpenTrawler{Manifest: manifest, Run: func(
			ctx context.Context,
			localShortReference *trawlkit.LocalTrawlerShortReference,
			recordAnchor *trawlkit.RecordAnchorIdentifier,
		) (*open.OpenRecord, *federationcontract.TrawlerOperationFailure) {
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
				return nil, federation.FailureForError(manifest, federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, err)
			}
			return record, nil
		}})
	}
	return trawlers
}

func cloneRegisteredTrawlerManifest(manifest *federationcontract.RegisteredTrawlerManifest) *federationcontract.RegisteredTrawlerManifest {
	if manifest == nil {
		return &federationcontract.RegisteredTrawlerManifest{}
	}
	return proto.Clone(manifest).(*federationcontract.RegisteredTrawlerManifest)
}
