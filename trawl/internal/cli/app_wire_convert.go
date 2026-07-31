package cli

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
)

const appStatusTimeout = 2 * time.Second

func (r *Runtime) appStatusResponse(
	ctx context.Context,
	registeredTrawlerManifestSnapshot registeredTrawlerManifestSnapshot,
) *federationv1.FederatedTrawlerStatusOperation {
	ctx = trawlkit.WithInternalAppRequest(ctx)
	ctx, cancel := context.WithTimeout(ctx, appStatusTimeout)
	defer cancel()
	response := federation.Status(
		ctx,
		r.federationStatusTrawlers(registeredTrawlerManifestSnapshot.installedTrawlers),
	)
	if registeredTrawlerManifestSnapshot.registeredTrawlerCatalogConstructionError == nil {
		response.RegisteredTrawlerCatalog =
			registeredTrawlerManifestSnapshot.registeredTrawlerCatalogEntries
		return response
	}
	response.OperationFailures = append(response.OperationFailures, &federationv1.TrawlerOperationFailure{
		FailedTrawler:                trawlkit.NewRegisteredTrawlerIdentity("catalog"),
		RegisteredTrawlerDisplayName: "OpenTrawl",
		FailureCode:                  federationv1.FailureCode_FAILURE_CODE_INTERNAL,
		FailureMessage:               registeredTrawlerManifestSnapshot.registeredTrawlerCatalogConstructionError.Error(),
	})
	response.Outcome = federatedOperationOutcome(
		len(response.TrawlerStatusResults),
		len(response.OperationFailures),
		len(response.TrawlersSkippedFromOperation),
	)
	return response
}

func (r *Runtime) appSearchResponse(
	ctx context.Context,
	trawlers []InstalledTrawler,
	canonicalSearchQuery trawlkit.Query,
	maximumReturnedSearchMatchCount int,
) *federationv1.FederatedTrawlerSearchOperation {
	return federation.Search(
		ctx,
		r.federationSearchTrawlers(trawlers),
		canonicalSearchQuery,
		uint32(maximumReturnedSearchMatchCount),
	)
}

func (r *Runtime) appOpenResponse(
	ctx context.Context,
	selectedTrawler *trawlkit.RegisteredTrawlerIdentity,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	recordAnchor *trawlkit.RecordAnchorIdentifier,
) *openv1.OpenResponse {
	return federation.Open(
		ctx,
		r.federationOpenTrawlers(discoverInstalledTrawlers(ctx)),
		selectedTrawler,
		localShortReference,
		recordAnchor,
	)
}

func appSyncAlreadyRunningResponse() *federationv1.FederatedTrawlerArchiveSyncOperation {
	return &federationv1.FederatedTrawlerArchiveSyncOperation{
		Outcome: federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED,
		OperationFailures: []*federationv1.TrawlerOperationFailure{{
			FailureCode:    federationv1.FailureCode_FAILURE_CODE_ALREADY_SYNCING,
			FailureMessage: "OpenTrawl is already syncing.",
		}},
	}
}
