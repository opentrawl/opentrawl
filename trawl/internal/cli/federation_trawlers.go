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

func (r *Runtime) federationStatusTrawlers(installedTrawlers []InstalledTrawler) []federation.StatusSource {
	trawlers := make([]federation.StatusSource, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.StatusSource{Manifest: manifest, Run: func(context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, "status", installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !hasCapability(installedTrawler, "status") {
			trawlers = append(trawlers, federation.StatusSource{Manifest: manifest, SkipReason: "Status is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.StatusSource{Manifest: manifest, Run: func(ctx context.Context) (*statusv1.TrawlerStatusResponse, *federationv1.TrawlerOperationFailure) {
			if installedTrawler.Trawler == nil {
				return nil, federation.FailureForError(manifest, "status", errors.New("status command has no trawler"))
			}
			status, err := r.trawlerExecutor().Status(ctx, installedTrawler.Trawler)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, "status", err)
			}
			return status, nil
		}})
	}
	return trawlers
}

func (r *Runtime) federationSearchTrawlers(installedTrawlers []InstalledTrawler) []federation.SearchSource {
	trawlers := make([]federation.SearchSource, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.SearchSource{Manifest: manifest, Run: func(context.Context, trawlkit.Query) (*searchv1.TrawlerSearchResponse, map[string]string, *federationv1.TrawlerOperationFailure) {
				return nil, nil, federation.FailureForError(manifest, "search", installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !hasCapability(installedTrawler, "search") {
			trawlers = append(trawlers, federation.SearchSource{Manifest: manifest, SkipReason: "Search is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.SearchSource{Manifest: manifest, Run: func(ctx context.Context, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, map[string]string, *federationv1.TrawlerOperationFailure) {
			_, ok := installedTrawler.Trawler.(trawlkit.Searcher)
			if !ok {
				return nil, nil, federation.FailureForError(manifest, "search", errors.New("declared search command has no searcher"))
			}
			result, localReferenceAliasesByCanonicalRecordReference, err := r.trawlerExecutor().Search(ctx, installedTrawler.Trawler, query)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, nil, federation.FailureForError(manifest, "search", err)
			}
			return result, localReferenceAliasesByCanonicalRecordReference, nil
		}})
	}
	return trawlers
}

func (r *Runtime) federationOpenTrawlers(installedTrawlers []InstalledTrawler) []federation.OpenSource {
	trawlers := make([]federation.OpenSource, 0, len(installedTrawlers))
	for _, installedTrawler := range installedTrawlers {
		installedTrawler := installedTrawler
		manifest := cloneRegisteredTrawlerManifest(installedTrawler.RegisteredTrawlerManifest)
		if installedTrawler.TrawlerDiscoveryError != nil {
			trawlers = append(trawlers, federation.OpenSource{Manifest: manifest, Run: func(context.Context, string, string) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure) {
				return nil, federation.FailureForError(manifest, "open", installedTrawler.TrawlerDiscoveryError)
			}})
			continue
		}
		if !hasCapability(installedTrawler, "open") {
			trawlers = append(trawlers, federation.OpenSource{Manifest: manifest, SkipReason: "Open is not supported."})
			continue
		}
		trawlers = append(trawlers, federation.OpenSource{Manifest: manifest, Run: func(ctx context.Context, ref, anchorID string) (*openv1.OpenRecord, *federationv1.TrawlerOperationFailure) {
			if _, ok := installedTrawler.Trawler.(trawlkit.RecordOpener); !ok {
				return nil, federation.FailureForError(manifest, "open", errors.New("declared open command has no record opener"))
			}
			record, err := r.trawlerExecutor().OpenRecord(ctx, installedTrawler.Trawler, ref, anchorID)
			if isTimeoutError(err) {
				err = context.DeadlineExceeded
			}
			if err != nil {
				return nil, federation.FailureForError(manifest, "open", err)
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
