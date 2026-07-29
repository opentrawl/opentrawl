package trawlkit

import (
	"context"
	"fmt"
	"strings"

	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
)

type typedSearch struct {
	query                                                 Query
	trawlerSearchResponse                                 *searchv1.TrawlerSearchResponse
	localReferenceAliasesByCanonicalSearchRecordReference map[string]string
}

func (operation *typedSearch) execute(ctx context.Context, trawler Trawler, request *TrawlerCommandExecutionRequest) error {
	trawlerSearchResponse, err := executeSearch(
		ctx,
		trawler.(Searcher),
		request,
		operation.query,
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawlerDisplayName,
	)
	if err != nil {
		return err
	}
	localReferenceAliasesByCanonicalSearchRecordReference, err :=
		readAssignedLocalShortReferenceAliasesByCanonicalRecordReference(
			ctx,
			request,
			canonicalSearchRecordReferences(trawlerSearchResponse),
		)
	if err != nil {
		return err
	}
	operation.trawlerSearchResponse = trawlerSearchResponse
	operation.localReferenceAliasesByCanonicalSearchRecordReference =
		localReferenceAliasesByCanonicalSearchRecordReference
	return nil
}

func executeSearch(
	ctx context.Context,
	searcher Searcher,
	request *TrawlerCommandExecutionRequest,
	query Query,
	registeredTrawlerDisplayName string,
) (*searchv1.TrawlerSearchResponse, error) {
	trawlerSearchResponse, err := searcher.Search(ctx, request, query)
	if err != nil {
		return nil, err
	}
	if trawlerSearchResponse == nil {
		return nil, fmt.Errorf("search returned no response")
	}
	registeredTrawlerDisplayName = strings.TrimSpace(registeredTrawlerDisplayName)
	if registeredTrawlerDisplayName == "" {
		return nil, fmt.Errorf("registered trawler display name is empty")
	}
	for matchIndex, searchMatch := range trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder() {
		if searchMatch == nil {
			return nil, fmt.Errorf("search match %d is missing", matchIndex)
		}
		if searchMatch.GetSearchMatchPresentation() == nil {
			return nil, fmt.Errorf("search match %d presentation is missing", matchIndex)
		}
		searchMatch.SearchMatchPresentation.RegisteredTrawlerDisplayName = registeredTrawlerDisplayName
	}
	return trawlerSearchResponse, nil
}

func canonicalSearchRecordReferences(
	trawlerSearchResponse *searchv1.TrawlerSearchResponse,
) []string {
	if trawlerSearchResponse == nil {
		return nil
	}
	canonicalRecordReferences := make(
		[]string,
		0,
		len(trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder()),
	)
	for _, searchMatch := range trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder() {
		if searchMatch == nil {
			continue
		}
		canonicalRecordReference := strings.TrimSpace(
			searchMatch.GetCanonicalMatchingRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		)
		if canonicalRecordReference != "" {
			canonicalRecordReferences = append(canonicalRecordReferences, canonicalRecordReference)
		}
	}
	return uniqueStrings(canonicalRecordReferences)
}
