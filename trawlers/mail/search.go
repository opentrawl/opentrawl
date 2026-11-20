package mail

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (*Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveUnavailableError(err)
	}
	options := archive.MailSearchOptions{
		SearchText:                     strings.TrimSpace(query.Text),
		ResultLimit:                    query.Limit,
		SearchTotalIsLowerBoundAtLimit: query.SearchTotalIsLowerBoundWhenResultLimitIsReached,
		ReceivedAtOrAfter:              query.After,
		ReceivedAtOrBefore:             query.Before,
	}
	if resolvedPersonFilter := query.PersonFilter.ResolvedPersonFilter(); resolvedPersonFilter != nil {
		options.ExactPersonFilterIdentifiers = resolvedPersonFilter.ExactPersonFilterIdentifiers
	} else {
		options.UnresolvedPersonFilterText = query.PersonFilter.UnresolvedPersonFilterText()
	}
	result, err := archiveStore.SearchMessages(ctx, options)
	var ambiguousPersonFilter *archive.AmbiguousPersonFilterError
	var unknownPersonFilter *archive.UnknownPersonFilterError
	switch {
	case errors.As(err, &ambiguousPersonFilter):
		return nil, newMailCommandError("ambiguous_who", err.Error(), err)
	case errors.As(err, &unknownPersonFilter):
		return nil, newMailCommandError("unknown_who", err.Error(), err)
	case err != nil:
		return nil, err
	}
	matches := make([]*search.TrawlerSearchMatch, 0, len(result.MailSearchHitsInDisplayOrder))
	for _, hit := range result.MailSearchHitsInDisplayOrder {
		matches = append(matches, mailTrawlerSearchMatch(hit))
	}
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: matches,
		TotalSearchMatches:                 uint64(max(result.TotalSearchMatches, 0)),
		TotalSearchMatchesIsLowerBound:     result.TotalSearchMatchesIsLowerBound,
		MoreSearchMatchesExist:             result.MoreSearchMatchesExist,
	}, nil
}

func mailTrawlerSearchMatch(hit archive.MailSearchHit) *search.TrawlerSearchMatch {
	displayName := strings.TrimSpace(hit.EmailSubject)
	if displayName == "" {
		displayName = "(no subject)"
	}
	matchPresentation := &search.SearchMatchPresentation{MatchingRecordDisplayName: displayName}
	if !hit.MessageReceivedTime.IsZero() {
		matchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(hit.MessageReceivedTime)},
		}
	}
	matchPresentation.PeopleRelatedToMatchingRecord = []*person.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         hit.SenderDisplayText,
		PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
	}}
	matchPresentation.DigitalContainerNamesNearestToBroadest = compactTexts(hit.AppleMailMailboxName, hit.AppleMailAccountDisplayName)
	if textField := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Message", hit.MatchingTextSnippet); textField != nil {
		matchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{textField}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(hit.MailMessageRecordReference),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(hit.FirstMatchingFieldName),
		SearchMatchPresentation:  matchPresentation,
	}
}

func compactTexts(values ...string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}
