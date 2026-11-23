package beeper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Search runs FTS5 and metadata-filtered search directly against beeper.db.
func (t *BeeperTrawler) Search(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	query trawlkit.Query,
) (*search.TrawlerSearchResponse, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}

	filter := beeperstore.BeeperMessageQueryFilter{
		Query: strings.TrimSpace(query.Text),
		Limit: query.Limit,
	}
	if !query.After.IsZero() {
		after := query.After
		filter.After = &after
	}
	if !query.Before.IsZero() {
		before := query.Before
		filter.Before = &before
	}

	if resolved := query.PersonFilter.ResolvedPersonFilter(); resolved != nil {
		filter.PersonFilter = trawlkit.NewResolvedSearchPersonFilter(
			resolved.PersonFilterText,
			resolved.ExactPersonFilterIdentifiers,
		)
	} else if unresolved := query.PersonFilter.UnresolvedPersonFilterText(); unresolved != "" {
		resolution, whoErr := st.ResolveWho(ctx, unresolved)
		if whoErr != nil {
			return nil, whoErr
		}
		filter.PersonFilter = trawlkit.NewUnresolvedSearchPersonFilter(unresolved)
		if len(resolution.Candidates) == 1 && !resolution.OnlyCloseSpellingMatch() {
			filter.ResolvedParticipantIDs = resolution.Candidates[0].Identifiers
			filter.ResolvedParticipantNames = []string{resolution.Candidates[0].DisplayName}
		}
	}

	total, err := st.CountSearchMessages(ctx, filter)
	if err != nil {
		return nil, err
	}
	messages, err := st.SearchMessages(ctx, filter)
	if err != nil {
		return nil, err
	}

	matches := make([]*search.TrawlerSearchMatch, 0, len(messages))
	for _, msg := range messages {
		matches = append(matches, buildBeeperSearchMatch(msg))
	}
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: matches,
		TotalSearchMatches:                 uint64(max(total, 0)),
		MoreSearchMatchesExist:             query.Limit > 0 && len(messages) < total,
	}, nil
}

func buildBeeperSearchMatch(msg beeperstore.BeeperArchivedMessage) *search.TrawlerSearchMatch {
	people := beeperMessageRelatedPeople(msg)
	pres := &search.SearchMatchPresentation{
		MatchingRecordKindDisplayName: "message",
		MatchingRecordDisplayName:     strings.TrimSpace(msg.MediaTitle),
		PeopleRelatedToMatchingRecord: people,
	}
	if !msg.Timestamp.IsZero() {
		pres.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
				ExactTime: timestamppb.New(msg.Timestamp),
			},
		}
	}

	displayContainer := beeperstore.FormatNetworkChatDisplayTitle(msg.Network, msg.ChatTitle)
	if displayContainer != "" {
		pres.DigitalContainerNamesNearestToBroadest = []string{displayContainer}
	}
	if cleanNet := strings.TrimSpace(msg.Network); cleanNet != "" {
		pres.MatchingRecordStatusLabels = []string{cleanNet}
	}

	for _, match := range msg.SearchMatches {
		field := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(match.Field, match.Runs)
		if field != nil {
			pres.SearchMatchTextFieldsInDisplayOrder = append(pres.SearchMatchTextFieldsInDisplayOrder, field)
		}
	}
	if len(pres.SearchMatchTextFieldsInDisplayOrder) == 0 {
		if textField := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch("Message", formatBeeperMessageBodyText(msg)); textField != nil {
			pres.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{textField}
		}
	}

	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(beeperstore.BeeperMessageRef(msg.MessageID)),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		SearchMatchPresentation:  pres,
	}
}

// Who matches people observed across archived Beeper conversations.
func (t *BeeperTrawler) Who(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	personQuery string,
) (*person.TrawlerPersonMatchResponse, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}
	resolution, err := st.ResolveWho(ctx, personQuery)
	if err != nil {
		return nil, err
	}
	candidates := make([]*person.TrawlerPersonMatchCandidate, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		item := &person.TrawlerPersonMatchCandidate{
			PersonDisplayName: candidate.DisplayName,
			PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{{
				RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(beeperTrawlerIdentifier),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: trawlkit.NewExactPersonFilterIdentifiers(candidate.Identifiers),
				PersonDisplayNamesObservedByTrawlerArchive:           []string{candidate.DisplayName},
			}},
		}
		if !candidate.LastSeen.IsZero() {
			item.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LastSeen)
		}
		if candidate.MessageCount > 0 {
			item.MessageCountInvolvingPerson = uint64(candidate.MessageCount)
		}
		candidates = append(candidates, item)
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: candidates}, nil
}

// CountPersonMessages counts archived Beeper messages involving an exact person filter.
func (t *BeeperTrawler) CountPersonMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	request *person.ExactPersonMessageCountRequest,
) (*person.ExactPersonMessageCountResponse, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}
	count, err := st.CountPersonMessages(ctx, request.GetExactPersonFilterIdentifiers())
	if err != nil {
		return nil, err
	}
	return &person.ExactPersonMessageCountResponse{MessageCountInvolvingPerson: count}, nil
}

// PeopleSnapshot exports observed Beeper contacts for OpenTrawl's cross-source People graph.
func (t *BeeperTrawler) PeopleSnapshot(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*person.TrawlerPeopleSnapshot, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}
	people, err := st.PeopleSnapshotCandidates(ctx)
	if err != nil {
		return nil, err
	}

	identities := make([]*person.TrawlerPersonIdentity, 0, len(people))
	for _, candidate := range people {
		if len(candidate.UserIDs) == 0 || strings.TrimSpace(candidate.DisplayName) == "" {
			continue
		}
		serviceName := beeperTrawlerIdentifier
		if len(candidate.Networks) == 1 {
			if slug := normalizeBeeperNetworkKey(candidate.Networks[0]); slug != "" {
				serviceName = slug
			}
		}
		identity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        trawlkit.NewPersonIdentifierWithinTrawlerArchive(candidate.UserIDs[0]),
			PersonDisplayName:                           candidate.DisplayName,
			PersonPhoneNumbers:                          append([]string(nil), candidate.PhoneNumbers...),
			PersonEmailAddresses:                        append([]string(nil), candidate.Emails...),
			MessageCountInvolvingPersonInTrawlerArchive: uint64(max(candidate.MessageCount, 0)),
		}
		if len(candidate.Identifiers) > 0 {
			identity.PersonAccountIdentifiersForServices = []*person.TrawlerPersonAccountIdentifiersForService{{
				PersonAccountServiceName:              serviceName,
				PersonAccountIdentifiersWithinService: trawlkit.NewPersonAccountIdentifiersWithinService(candidate.Identifiers),
			}}
		}
		if !candidate.LastSeen.IsZero() {
			identity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive = timestamppb.New(candidate.LastSeen)
		}
		identities = append(identities, identity)
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: identities}, nil
}
