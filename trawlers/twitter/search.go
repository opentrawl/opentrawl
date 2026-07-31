package twitter

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (r *runtime) search(ctx context.Context, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	archiveSearchFilter := store.SearchFilter{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  timePtr(query.After),
		Before: timePtr(query.Before),
	}
	var trawlerSearchResponse *searchv1.TrawlerSearchResponse
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
		trawlerSearchResponse = &searchv1.TrawlerSearchResponse{
			TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
			TotalSearchMatches:                 uint64(totalSearchMatches),
			MoreSearchMatchesExist:             totalSearchMatches > len(trawlerSearchMatches),
		}
		return nil
	})
	return trawlerSearchResponse, err
}

func twitterTrawlerSearchMatches(archiveSearchResults []store.SearchResult, ownerAuthorID string) []*searchv1.TrawlerSearchMatch {
	trawlerSearchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		name := postAuthorDisplayName(archiveSearchResult.Who, archiveSearchResult.AuthorID, ownerAuthorID)
		if strings.TrimSpace(name) == "" {
			name = "Post"
		}
		searchMatchPresentation := &searchv1.SearchMatchPresentation{MatchingRecordDisplayName: name}
		if !archiveSearchResult.CreatedAt.IsZero() {
			searchMatchPresentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(archiveSearchResult.CreatedAt)},
			}
		}
		if matchingPostText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Post", archiveSearchResult.Snippet); matchingPostText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*searchv1.SearchMatchTextField{matchingPostText}
		}
		trawlerSearchMatches = append(trawlerSearchMatches, &searchv1.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.TweetRef(archiveSearchResult.ID)),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
			SearchMatchPresentation:  searchMatchPresentation,
		})
	}
	return trawlerSearchMatches
}
