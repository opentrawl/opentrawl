package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawl/internal/densesearch"
	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
)

func (r *Runtime) addDenseSearchMatches(
	ctx context.Context,
	response *federationcontract.FederatedTrawlerSearchOperation,
	query trawlkit.Query,
	selectedTrawlers []InstalledTrawler,
	queryHasExactPersonFilter bool,
) {
	if response == nil || strings.TrimSpace(query.Text) == "" {
		return
	}
	if queryHasExactPersonFilter {
		response.SemanticSearchAvailability = federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_PERSON_FILTER_UNSUPPORTED
		r.logInfo(
			"semantic_search_unavailable",
			"reason="+logQuote(response.GetSemanticSearchAvailability().String()),
		)
		return
	}
	eligibleRegisteredTrawlers := make([]*identity.RegisteredTrawlerIdentity, 0, len(selectedTrawlers))
	for _, selectedTrawler := range selectedTrawlers {
		eligibleRegisteredTrawlers = append(
			eligibleRegisteredTrawlers,
			selectedTrawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		)
	}
	denseSearchMatches, availability, err := densesearch.Search(
		ctx,
		densesearch.IndexPath(r.stateRoot),
		query.Text,
		densesearch.SearchFilters{
			EligibleRegisteredTrawlers: eligibleRegisteredTrawlers,
			After:                      query.After,
			Before:                     query.Before,
		},
	)
	response.SemanticSearchAvailability = semanticSearchAvailability(availability)
	if availability == densesearch.AvailabilityAvailable {
		denseSearchMatchesAdded := federation.AddDenseSearchMatches(response, denseSearchMatches)
		r.logInfo(
			"semantic_search_complete",
			"candidate_count="+fmt.Sprint(len(denseSearchMatches))+" merged_match_count="+fmt.Sprint(denseSearchMatchesAdded),
		)
		return
	}
	reason := response.GetSemanticSearchAvailability().String()
	if err != nil {
		reason += " error_type=" + logQuote(errorTypeName(err))
	}
	r.logInfo("semantic_search_unavailable", "reason="+logQuote(reason))
}

func semanticSearchAvailability(availability densesearch.Availability) federationcontract.SemanticSearchAvailability {
	switch availability {
	case densesearch.AvailabilityAvailable:
		return federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_AVAILABLE
	case densesearch.AvailabilityIndexNotBuilt:
		return federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_NOT_BUILT
	case densesearch.AvailabilityIndexBuilding:
		return federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_BUILDING
	case densesearch.AvailabilityModelUnavailable:
		return federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_MODEL_UNAVAILABLE
	default:
		return federationcontract.SemanticSearchAvailability_SEMANTIC_SEARCH_AVAILABILITY_INDEX_INCOMPATIBLE
	}
}

func errorTypeName(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T", err)
}
