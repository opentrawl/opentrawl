package twitter

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (r *runtime) search(ctx context.Context, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	if strings.TrimSpace(query.Text) == "" {
		return nil, usageErr(ckoutput.HumanFacingErrorMessage("X search needs words."))
	}
	archiveSearchFilter := store.SearchFilter{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  timePtr(query.After),
		Before: timePtr(query.Before),
	}
	var trawlerSearchResponse *search.TrawlerSearchResponse
	err := r.withReadOnlyStore(func(archiveStore *store.Store) error {
		archiveSearchResults, totalSearchMatches, err := archiveStore.Search(ctx, archiveSearchFilter)
		if err != nil {
			return err
		}
		ownerAuthorID, err := archiveStore.OwnerAuthorID(ctx)
		if err != nil {
			return err
		}
		trawlerSearchMatches := twitterTrawlerSearchMatches(archiveSearchResults, ownerAuthorID)
		trawlerSearchResponse = &search.TrawlerSearchResponse{
			TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
			TotalSearchMatches:                 uint64(totalSearchMatches),
			MoreSearchMatchesExist:             totalSearchMatches > len(trawlerSearchMatches),
		}
		return nil
	})
	return trawlerSearchResponse, err
}

func twitterTrawlerSearchMatches(archiveSearchResults []store.SearchResult, ownerAuthorID string) []*search.TrawlerSearchMatch {
	trawlerSearchMatches := make([]*search.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		name := postAuthorDisplayName(archiveSearchResult.Who, archiveSearchResult.AuthorID, ownerAuthorID)
		if strings.TrimSpace(name) == "" {
			name = "Post"
		}
		searchMatchPresentation := &search.SearchMatchPresentation{MatchingRecordDisplayName: name}
		if !archiveSearchResult.CreatedAt.IsZero() {
			searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(archiveSearchResult.CreatedAt)},
			}
		}
		if matchingPostText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Post", archiveSearchResult.Snippet); matchingPostText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingPostText}
		}
		trawlerSearchMatches = append(trawlerSearchMatches, &search.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.TweetRef(archiveSearchResult.ID)),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
			SearchMatchPresentation:  searchMatchPresentation,
		})
	}
	return trawlerSearchMatches
}
