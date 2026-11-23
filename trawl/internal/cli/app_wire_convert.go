package cli

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const appStatusTimeout = 2 * time.Second

func (r *Runtime) appStatusResponse(
	ctx context.Context,
	registeredTrawlerManifestSnapshot registeredTrawlerManifestSnapshot,
) *federationcontract.FederatedTrawlerStatusOperation {
	ctx = trawlkit.WithInternalAppRequest(ctx)
	ctx, cancel := context.WithTimeout(ctx, appStatusTimeout)
	defer cancel()
	response := federation.Status(
		ctx,
		r.federationStatusTrawlers(registeredTrawlerManifestSnapshot.installedTrawlers),
	)
	response.KeepSourceAppsRunningForAutomaticUpdates = r.keepSourceAppsRunningForAutomaticUpdates()
	r.attachIndexedMonthlyHistoriesToStatus(ctx, response)
	if registeredTrawlerManifestSnapshot.registeredTrawlerCatalogConstructionError == nil {
		response.RegisteredTrawlerCatalog =
			registeredTrawlerManifestSnapshot.registeredTrawlerCatalogEntries
		return response
	}
	response.OperationFailures = append(response.OperationFailures, &federationcontract.TrawlerOperationFailure{
		FailedTrawler:                trawlkit.NewRegisteredTrawlerIdentity("catalog"),
		RegisteredTrawlerDisplayName: "OpenTrawl",
		FailureCode:                  federationcontract.FailureCode_FAILURE_CODE_INTERNAL,
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
) *federationcontract.FederatedTrawlerSearchOperation {
	response := r.runUnifiedAndFederatedSearch(
		ctx,
		trawlers,
		canonicalSearchQuery,
		maximumReturnedSearchMatchCount,
		"",
		nil,
	)
	for _, match := range response.GetSearchMatchesInDisplayOrder() {
		render.PrepareSearchMatchMonetaryAmountForApp(match.GetSearchMatchPresentation())
		if presentation := match.GetSearchMatchPresentation(); presentation != nil && presentation.GetAppleLibraryPhotoFacts() != nil {
			presentation.PhysicalPlaceNamesSpecificToBroadest = render.SearchMatchPhysicalPlaceNamesForHumanOutput(presentation)
		}
	}
	return response
}

func (r *Runtime) appOpenResponse(
	ctx context.Context,
	selectedTrawler *trawlkit.RegisteredTrawlerIdentity,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	recordAnchor *trawlkit.RecordAnchorIdentifier,
	requestedTrawlLink *trawlkit.GloballyRoutableTrawlLink,
) *open.OpenResponse {
	response := federation.Open(
		ctx,
		r.federationOpenTrawlers(discoverInstalledTrawlers(ctx)),
		selectedTrawler,
		localShortReference,
		recordAnchor,
	)
	response.RequestedTrawlLink = requestedTrawlLink
	if record := response.GetRecord(); record != nil && record.GetOpenedPhotoRecord() != nil {
		record.TypedOpenedRecord = &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: render.PhotoOpenedRecordPresentation(record.GetOpenedPhotoRecord()),
		}
	}
	if record := response.GetRecord(); record != nil && record.GetOpenedEmailMessageRecord() != nil {
		record.TypedOpenedRecord = &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: render.EmailOpenedRecordPresentation(record.GetOpenedEmailMessageRecord()),
		}
	}
	return response
}

func appUpdateAlreadyRunningResponse() *federationcontract.FederatedTrawlerArchiveUpdateOperation {
	return &federationcontract.FederatedTrawlerArchiveUpdateOperation{
		Outcome: federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED,
		OperationFailures: []*federationcontract.TrawlerOperationFailure{{
			FailureCode:    federationcontract.FailureCode_FAILURE_CODE_ALREADY_UPDATING,
			FailureMessage: "OpenTrawl is already updating.",
		}},
	}
}
