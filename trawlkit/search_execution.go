package trawlkit

import (
	"context"
	"fmt"
	"strings"

	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
)

type executeTrawlerSearchOperation struct {
	query                                                Query
	trawlerSearchResponse                                *search.TrawlerSearchResponse
	localShortReferencesByCanonicalSearchRecordReference []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
}

func (operation *executeTrawlerSearchOperation) execute(ctx context.Context, trawler Trawler, request *TrawlerCommandExecutionRequest) error {
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
	localShortReferencesByCanonicalSearchRecordReference, err :=
		readAssignedLocalShortReferencesByCanonicalRecordReference(
			ctx,
			request,
			canonicalSearchRecordReferences(trawlerSearchResponse),
		)
	if err != nil {
		return err
	}
	operation.trawlerSearchResponse = trawlerSearchResponse
	operation.localShortReferencesByCanonicalSearchRecordReference =
		localShortReferencesByCanonicalSearchRecordReference
	return nil
}

func executeSearch(
	ctx context.Context,
	searcher Searcher,
	request *TrawlerCommandExecutionRequest,
	query Query,
	registeredTrawlerDisplayName string,
) (*search.TrawlerSearchResponse, error) {
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
	trawlerSearchResponse *search.TrawlerSearchResponse,
) []*CanonicalArchiveRecordReference {
	if trawlerSearchResponse == nil {
		return nil
	}
	canonicalRecordReferences := make(
		[]*CanonicalArchiveRecordReference,
		0,
		len(trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder()),
	)
	for _, searchMatch := range trawlerSearchResponse.GetTrawlerSearchMatchesInDisplayOrder() {
		if searchMatch == nil {
			continue
		}
		canonicalRecordReference := searchMatch.GetCanonicalRecordReference()
		if CanonicalArchiveRecordReferenceText(canonicalRecordReference) != "" {
			canonicalRecordReferences = append(canonicalRecordReferences, canonicalRecordReference)
		}
	}
	return canonicalRecordReferences
}
