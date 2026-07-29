package gmail

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func gmailTrawlerSearchMatch(archiveSearchHit archive.SearchHit) (*searchv1.TrawlerSearchMatch, error) {
	associatedExactTime, err := parseContractTime(archiveSearchHit.Time)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(archiveSearchHit.Where)
	if name == "" {
		name = "(no subject)"
	}
	anchorID := "subject"
	if len(archiveSearchHit.Matches) > 0 {
		anchorID = archiveSearchHit.Matches[0].Field
	}
	searchMatchPresentation := &searchv1.SearchMatchPresentation{
		MatchingRecordDisplayName: name,
	}
	if !associatedExactTime.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(associatedExactTime)},
		}
	}
	if matchingMessageText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Message", archiveSearchHit.Snippet); matchingMessageText != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*searchv1.SearchMatchTextField{matchingMessageText}
	}
	return &searchv1.TrawlerSearchMatch{
		CanonicalMatchingRecordReferenceForGloballyRoutableTrawlLinkAssignment: archiveSearchHit.Ref,
		MatchingRecordAnchorIdentifier:                                         anchorID,
		SearchMatchPresentation:                                                searchMatchPresentation,
	}, nil
}

func whoCandidate(candidate archive.WhoCandidate) *personv1.TrawlerPersonMatchCandidate {
	lastSeen, _ := parseContractTime(candidate.LastSeen)
	result := &personv1.TrawlerPersonMatchCandidate{
		PersonDisplayName: candidate.Who,
		PersonMatchFactsFromTrawlers: []*personv1.PersonMatchFactsFromTrawler{
			trawlkit.NewPersonMatchFactsFromTrawler(
				appID,
				candidate.Identifiers,
				candidate.Who,
			),
		},
		MessageCountInvolvingPerson: uint64(max(candidate.Messages, 0)),
	}
	if !lastSeen.IsZero() {
		result.LatestMatchingArchiveRecordTime = timestamppb.New(lastSeen)
	}
	return result
}

func parseContractTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid archive time %q", value)
	}
	return t, nil
}
