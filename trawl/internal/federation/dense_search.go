package federation

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

func AddDenseSearchMatches(
	response *federation.FederatedTrawlerSearchOperation,
	denseSearchMatches []*federation.FederatedSearchMatch,
) int {
	if response == nil || len(denseSearchMatches) == 0 || response.GetResultLimit() == 0 {
		return 0
	}
	lexicalSearchMatches := append([]*federation.FederatedSearchMatch(nil), response.GetSearchMatchesInDisplayOrder()...)
	mergedSearchMatches := make([]*federation.FederatedSearchMatch, 0, response.GetResultLimit())
	seenSearchResultGroups := make(map[string]struct{}, response.GetResultLimit())
	denseSearchMatchesAdded := 0
	for searchMatchIndex := 0; searchMatchIndex < max(len(lexicalSearchMatches), len(denseSearchMatches)); searchMatchIndex++ {
		for searchLaneIndex, searchLane := range [][]*federation.FederatedSearchMatch{lexicalSearchMatches, denseSearchMatches} {
			if searchMatchIndex >= len(searchLane) {
				continue
			}
			searchMatch := searchLane[searchMatchIndex]
			if searchMatch == nil {
				continue
			}
			groupReference := strings.TrimSpace(trawlkit.CanonicalArchiveRecordReferenceText(
				searchMatch.GetCanonicalSearchResultGroupReference(),
			))
			if groupReference == "" {
				continue
			}
			if _, alreadyIncluded := seenSearchResultGroups[groupReference]; alreadyIncluded {
				continue
			}
			seenSearchResultGroups[groupReference] = struct{}{}
			mergedSearchMatches = append(mergedSearchMatches, searchMatch)
			if searchLaneIndex == 1 {
				denseSearchMatchesAdded++
			}
			if len(mergedSearchMatches) == int(response.GetResultLimit()) {
				response.SearchMatchesInDisplayOrder = mergedSearchMatches
				response.MoreSearchMatchesExist = response.GetMoreSearchMatchesExist() ||
					searchMatchIndex+1 < len(lexicalSearchMatches) ||
					searchMatchIndex+1 < len(denseSearchMatches)
				return denseSearchMatchesAdded
			}
		}
	}
	response.SearchMatchesInDisplayOrder = mergedSearchMatches
	return denseSearchMatchesAdded
}
