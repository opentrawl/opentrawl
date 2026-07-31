package gmail

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func gmailTrawlerSearchMatch(archiveSearchHit archive.SearchHit) (*search.TrawlerSearchMatch, error) {
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
	searchMatchPresentation := &search.SearchMatchPresentation{
		MatchingRecordDisplayName: name,
	}
	if !associatedExactTime.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(associatedExactTime)},
		}
	}
	if matchingMessageText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Message", archiveSearchHit.Snippet); matchingMessageText != nil {
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingMessageText}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchHit.Ref),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(anchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}, nil
}

func whoCandidate(candidate archive.WhoCandidate) *person.TrawlerPersonMatchCandidate {
	lastSeen, _ := parseContractTime(candidate.LastSeen)
	result := &person.TrawlerPersonMatchCandidate{
		PersonDisplayName: candidate.Who,
		PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{
			trawlkit.NewPersonMatchFactsFromTrawler(
				trawlkit.NewRegisteredTrawlerIdentity(appID),
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
