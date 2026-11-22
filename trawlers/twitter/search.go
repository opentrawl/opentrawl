package twitter

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type datedTwitterSearchMatch struct {
	when  time.Time
	id    string
	match *search.TrawlerSearchMatch
}

func (r *runtime) search(ctx context.Context, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	if strings.TrimSpace(query.Text) == "" {
		return nil, usageErr(render.TrawlerSearchNeedsWords("Twitter"))
	}
	archiveSearchFilter := store.SearchFilter{
		Query:  query.Text,
		Limit:  query.Limit,
		After:  timePtr(query.After),
		Before: timePtr(query.Before),
	}
	var trawlerSearchResponse *search.TrawlerSearchResponse
	err := r.withReadOnlyStore(func(archiveStore *store.Store) error {
		tweetResults, totalTweetMatches, err := archiveStore.Search(ctx, archiveSearchFilter)
		if err != nil {
			return err
		}
		dmResults, totalDMMatches, err := archiveStore.SearchDirectMessages(ctx, archiveSearchFilter)
		if err != nil {
			return err
		}
		ownerAuthorID, err := archiveStore.OwnerAuthorID(ctx)
		if err != nil {
			return err
		}
		combined := make([]datedTwitterSearchMatch, 0, len(tweetResults)+len(dmResults))
		for _, hit := range tweetResults {
			combined = append(combined, datedTwitterSearchMatch{
				when:  hit.CreatedAt,
				id:    hit.ID,
				match: twitterTweetSearchMatch(hit, ownerAuthorID),
			})
		}
		for _, hit := range dmResults {
			combined = append(combined, datedTwitterSearchMatch{
				when:  hit.CreatedAt,
				id:    hit.ID,
				match: twitterDirectMessageSearchMatch(hit),
			})
		}
		sort.Slice(combined, func(i, j int) bool {
			if !combined[i].when.Equal(combined[j].when) {
				return combined[i].when.After(combined[j].when)
			}
			return combined[i].id > combined[j].id
		})
		if query.Limit > 0 && len(combined) > query.Limit {
			combined = combined[:query.Limit]
		}
		matches := make([]*search.TrawlerSearchMatch, 0, len(combined))
		for _, item := range combined {
			matches = append(matches, item.match)
		}
		totalMatches := totalTweetMatches + totalDMMatches
		trawlerSearchResponse = &search.TrawlerSearchResponse{
			TrawlerSearchMatchesInDisplayOrder: matches,
			TotalSearchMatches:                 uint64(max(totalMatches, 0)),
			MoreSearchMatchesExist:             totalMatches > len(matches),
		}
		return nil
	})
	return trawlerSearchResponse, err
}

func twitterTweetSearchMatch(archiveSearchResult store.SearchResult, ownerAuthorID string) *search.TrawlerSearchMatch {
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
	if matchingPostText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(twitterSearchMatchFieldLabel(archiveSearchResult.Roles, archiveSearchResult.Text), archiveSearchResult.Snippet); matchingPostText != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingPostText}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.TweetRef(archiveSearchResult.ID)),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}
}

func twitterSearchMatchFieldLabel(roles []string, tweetText string) string {
	roleSet := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		roleSet[strings.TrimSpace(role)] = struct{}{}
	}
	authoredLabel := "Authored"
	if strings.HasPrefix(strings.TrimSpace(tweetText), "RT @") {
		authoredLabel = "Repost"
	}
	orderedRoles := []struct {
		key   string
		label string
	}{
		{key: "authored", label: authoredLabel},
		{key: "bookmark", label: "Bookmark"},
		{key: "mention", label: "Mention"},
		{key: "like", label: "Like"},
		{key: "reply_context", label: "Reply context"},
	}
	labels := make([]string, 0, len(roles))
	for _, candidate := range orderedRoles {
		if _, present := roleSet[candidate.key]; present {
			labels = append(labels, candidate.label)
		}
	}
	if len(labels) == 0 {
		return "Post"
	}
	return strings.Join(labels, ", ")
}

func twitterDirectMessageSearchMatch(hit store.DirectMessageSearchHit) *search.TrawlerSearchMatch {
	where := directMessageWhere(hit.DirectMessage)
	fieldLabel := "Direct message"
	if hit.InboxKind == "request" {
		fieldLabel = "Direct message request"
	}
	searchMatchPresentation := &search.SearchMatchPresentation{
		MatchingRecordDisplayName:              where,
		PeopleRelatedToMatchingRecord:          directMessagePeople(hit.DirectMessage),
		DigitalContainerNamesNearestToBroadest: []string{where},
	}
	if !hit.CreatedAt.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(hit.CreatedAt)},
		}
	}
	snippet := strings.TrimSpace(hit.Snippet)
	if snippet == "" {
		snippet = strings.TrimSpace(hit.Text)
	}
	if matchingDMText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(fieldLabel, snippet); matchingDMText != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingDMText}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(store.DMMessageRef(hit.ID)),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}
}
