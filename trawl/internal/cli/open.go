package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type OpenCmd struct {
	Link         string `arg:"" name:"link" help:"Link from search or a list, or Apple photo UUID"`
	Participants bool   `name:"participants" help:"Show all observed conversation participants"`
}

var applePhotoUUIDAcceptedByOpenCommandPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (c *OpenCmd) Run(r *Runtime) error {
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(c.Link)
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink)
	if err != nil {
		applePhotoUUIDArgument := strings.TrimPrefix(strings.TrimSpace(c.Link), "photos:")
		if !applePhotoUUIDAcceptedByOpenCommandPattern.MatchString(applePhotoUUIDArgument) {
			return render.LinkIsNotValid()
		}
		requestedTrawlLink = nil
		route = trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity("photos"),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(strings.ToUpper(applePhotoUUIDArgument)),
		}
	}
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	if selectedTrawler, found := findInstalledTrawler(installedTrawlers, trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)); found {
		if _, err := trawlersForSharedOperation([]InstalledTrawler{selectedTrawler}, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, true); err != nil {
			return err
		}
	}
	openTrawlers := r.federationOpenTrawlers(installedTrawlers)
	response := r.canonicalOpen(
		openTrawlers,
		route.RegisteredTrawler,
		route.LocalShortReference,
		requestedTrawlLink,
	)
	if !c.Participants || response.GetFailure() != nil {
		return r.renderOpenResponse(response)
	}
	conversationRecord := response.GetRecord().GetConversationRecord()
	if conversationRecord == nil {
		return render.ParticipantsNeedsConversationLink()
	}
	participantListResponse := r.conversationParticipantListResponse(
		conversationRecord,
		installedTrawlers,
	)
	return r.output.WriteConversationParticipants(participantListResponse)
}

func (r *Runtime) conversationParticipantListResponse(
	conversationRecord *conversation.ConversationRecord,
	installedTrawlers []InstalledTrawler,
) *conversation.ConversationParticipantListResponse {
	numberOfDistinctParticipantRecordsObservedByTrawlerArchive := uint64(0)
	participantRows := make(
		[]*conversation.ConversationParticipantForCompleteConversationParticipantList,
		0,
		len(conversationRecord.GetConversationParticipantIdentitiesObservedByTrawlerArchive()),
	)
	for _, participantIdentity := range conversationRecord.GetConversationParticipantIdentitiesObservedByTrawlerArchive() {
		if participantIdentity == nil {
			continue
		}
		numberOfDistinctParticipantRecordsObservedByTrawlerArchive++
		personDisplayName := strings.TrimSpace(participantIdentity.GetPersonDisplayName())
		if personDisplayName == "" {
			continue
		}
		participantRows = append(
			participantRows,
			&conversation.ConversationParticipantForCompleteConversationParticipantList{
				PersonDisplayName: personDisplayName,
				PersonTrawlLinkResolvedAcrossTrawlerArchives: r.unambiguousPersonTrawlLinkForConversationParticipant(
					installedTrawlers,
					participantIdentity.GetExactPersonFilterIdentifiersObservedByTrawlerArchive(),
				),
			},
		)
	}
	sort.SliceStable(participantRows, func(left, right int) bool {
		leftDisplayName := strings.ToLower(participantRows[left].GetPersonDisplayName())
		rightDisplayName := strings.ToLower(participantRows[right].GetPersonDisplayName())
		if leftDisplayName != rightDisplayName {
			return leftDisplayName < rightDisplayName
		}
		return participantRows[left].GetPersonDisplayName() < participantRows[right].GetPersonDisplayName()
	})
	if conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive != nil &&
		conversationRecord.GetNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive() >
			numberOfDistinctParticipantRecordsObservedByTrawlerArchive {
		numberOfDistinctParticipantRecordsObservedByTrawlerArchive =
			conversationRecord.GetNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive()
	}
	return &conversation.ConversationParticipantListResponse{
		ConversationParticipantsInAlphabeticalOrder:                            participantRows,
		NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive: numberOfDistinctParticipantRecordsObservedByTrawlerArchive,
	}
}

func (r *Runtime) unambiguousPersonTrawlLinkForConversationParticipant(
	installedTrawlers []InstalledTrawler,
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
) *trawlkit.GloballyRoutableTrawlLink {
	var unambiguousPersonTrawlLink *trawlkit.GloballyRoutableTrawlLink
	for _, exactPersonFilterIdentifier := range exactPersonFilterIdentifiers {
		exactPersonFilterIdentifierText := strings.TrimSpace(
			exactPersonFilterIdentifier.GetExactPersonFilterIdentifier(),
		)
		if exactPersonFilterIdentifierText == "" {
			continue
		}
		resolution := resolveWhoThroughContacts(r, installedTrawlers, exactPersonFilterIdentifierText)
		if len(resolution.OperationFailures) > 0 {
			return nil
		}
		exactCandidatesWithPersonLinks := make([]personMatchCandidate, 0, len(resolution.Candidates))
		for _, candidate := range resolution.Candidates {
			if candidate.PersonIdentityMatchRank == whomatch.RankExact &&
				strings.TrimSpace(candidate.PersonTrawlLink.GetGloballyRoutableTrawlLink()) != "" {
				exactCandidatesWithPersonLinks = append(exactCandidatesWithPersonLinks, candidate)
			}
		}
		if len(exactCandidatesWithPersonLinks) > 1 {
			return nil
		}
		if len(exactCandidatesWithPersonLinks) == 0 {
			continue
		}
		candidatePersonTrawlLink := exactCandidatesWithPersonLinks[0].PersonTrawlLink
		if unambiguousPersonTrawlLink != nil &&
			unambiguousPersonTrawlLink.GetGloballyRoutableTrawlLink() !=
				candidatePersonTrawlLink.GetGloballyRoutableTrawlLink() {
			return nil
		}
		unambiguousPersonTrawlLink = candidatePersonTrawlLink
	}
	return unambiguousPersonTrawlLink
}

func (r *Runtime) renderOpenResponse(response *open.OpenResponse) error {
	if failure := response.GetFailure(); failure != nil {
		r.logInfo("open_failed", "error="+logQuote(failure.GetFailureMessage()))
		trawlerDisplayName := firstNonEmpty(
			strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			trawlkit.RegisteredTrawlerIdentityText(failure.GetFailedTrawler()),
		)
		switch {
		case failure.GetFailureCode() == federation.FailureCode_FAILURE_CODE_NOT_FOUND:
			return render.NoRecordHasThatLink()
		case failure.GetFailureCode() == federation.FailureCode_FAILURE_CODE_INVALID_INPUT:
			return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, failure.GetFailureMessage())
		case failureMeansArchiveUnavailable(failure.GetFailureCode()) && trawlerDisplayName != "":
			return render.TrawlerArchiveIsNotAvailable(trawlerDisplayName)
		case trawlerDisplayName != "":
			return render.CommandDidNotCompleteForTrawler(trawlerDisplayName)
		default:
			return render.CommandDidNotComplete()
		}
	}
	if response.GetRecord() == nil {
		return fmt.Errorf("open response has no record")
	}
	renderContext, err := r.openResponseRenderContext(response)
	if err != nil {
		return err
	}
	if err := r.output.WriteOpenedRecord(response, renderContext); err != nil {
		return err
	}
	return outcomeExit(response.GetOutcome())
}

func failureMeansArchiveUnavailable(failureCode federation.FailureCode) bool {
	return failureCode == federation.FailureCode_FAILURE_CODE_UNAVAILABLE ||
		failureCode == federation.FailureCode_FAILURE_CODE_PERMISSION
}

func (r *Runtime) openResponseRenderContext(
	response *open.OpenResponse,
) (render.OpenResponseRenderContext, error) {
	openedRecord := response.GetRecord()
	if openedRecord == nil || openedRecord.GetTrawlerSpecificOpenedRecordPresentation() == nil {
		return render.OpenResponseRenderContext{}, nil
	}
	registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(openedRecord.GetRecordTrawler())
	installedTrawler, found := findInstalledTrawler(discoverInstalledTrawlers(r.ctx), registeredTrawlerIdentity)
	if !found || installedTrawler.Trawler == nil {
		return render.OpenResponseRenderContext{}, nil
	}
	actionBuilder, providesActions := installedTrawler.Trawler.(trawlkit.TrawlerSpecificOpenedRecordPresentationActionBuilder)
	if !providesActions {
		return render.OpenResponseRenderContext{}, nil
	}
	actions, err := actionBuilder.BuildTrawlerSpecificOpenedRecordPresentationActions(openedRecord)
	if err != nil {
		return render.OpenResponseRenderContext{}, err
	}
	return render.OpenResponseRenderContext{
		TrawlerSpecificOpenedRecordPresentationActions: actions,
	}, nil
}
