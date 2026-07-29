package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type ConversationsCmd struct {
	With   string `name:"with" help:"Show conversations with a person by name or Contacts link"`
	Unread bool   `name:"unread" help:"Only conversations with unread messages"`
	Limit  int    `name:"limit" default:"50" help:"Maximum number of conversations"`
	All    bool   `name:"all" help:"Show every conversation"`
}

func (c *ConversationsCmd) Run(r *Runtime) error {
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	return c.runForTrawlers(r, conversationTrawlers(installedTrawlers), installedTrawlers, "", true)
}

func (c *ConversationsCmd) runForTrawlers(
	r *Runtime,
	trawlers []InstalledTrawler,
	installedTrawlers []InstalledTrawler,
	registeredTrawlerCommandNameForMoreAction string,
	showRegisteredTrawlerDisplayNameInConversationTable bool,
) error {
	query, err := c.conversationQuery(r, installedTrawlers)
	if err != nil {
		return err
	}
	operation := r.federatedTrawlerConversationListOperation(trawlers, query)
	if operation.GetOutcome() != federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED {
		if err := render.WriteFederatedTrawlerConversationListOperation(
			r.stdout,
			operation,
			showRegisteredTrawlerDisplayNameInConversationTable,
		); err != nil {
			return err
		}
	}
	if operation.GetMoreConversationRecordsExist() {
		if err := c.writeConversationListMoreAction(r.stdout, registeredTrawlerCommandNameForMoreAction); err != nil {
			return err
		}
	}
	r.reportFederationOutcomes(operation.GetOperationFailures(), operation.GetTrawlersSkippedFromOperation())
	return outcomeExit(operation.GetOutcome())
}

func (c *ConversationsCmd) runForTrawler(
	r *Runtime,
	trawler InstalledTrawler,
	installedTrawlers []InstalledTrawler,
) error {
	return c.runForTrawlers(
		r,
		[]InstalledTrawler{trawler},
		installedTrawlers,
		trawler.RegisteredTrawlerCommandName,
		false,
	)
}

func (c *ConversationsCmd) writeConversationListMoreAction(writer io.Writer, registeredTrawlerCommandName string) error {
	_, err := fmt.Fprintf(writer, "\nMore: %s\n", c.conversationListMoreCommand(writer, registeredTrawlerCommandName))
	return err
}

func (c *ConversationsCmd) conversationListMoreCommand(writer io.Writer, registeredTrawlerCommandName string) string {
	commandParts := []string{render.TrawlInvocationDisplay(writer)}
	if registeredTrawlerCommandName = strings.TrimSpace(registeredTrawlerCommandName); registeredTrawlerCommandName != "" {
		commandParts = append(commandParts, registeredTrawlerCommandName)
	}
	commandParts = append(commandParts, "conversations")
	if with := strings.TrimSpace(c.With); with != "" {
		commandParts = append(commandParts, "--with", quoteConversationListFilterForShell(with))
	}
	if c.Unread {
		commandParts = append(commandParts, "--unread")
	}
	commandParts = append(commandParts, "--limit", strconv.Itoa(c.Limit*2))
	return strings.Join(commandParts, " ")
}

func quoteConversationListFilterForShell(filter string) string {
	return "'" + strings.ReplaceAll(filter, "'", "'\"'\"'") + "'"
}

func (c *ConversationsCmd) conversationQuery(r *Runtime, installedTrawlers []InstalledTrawler) (trawlkit.ConversationQuery, error) {
	if !c.All && c.Limit < 1 {
		return trawlkit.ConversationQuery{}, usageErr{errors.New("--limit must be at least 1.")}
	}
	resolvedPersonMatchFactsFromTrawlers, err :=
		r.resolveConversationPersonMatchFacts(installedTrawlers, c.With)
	if err != nil {
		return trawlkit.ConversationQuery{}, err
	}
	return trawlkit.ConversationQuery{
		ResolvedPersonMatchFactsFromTrawlers: resolvedPersonMatchFactsFromTrawlers,
		Unread:                               c.Unread,
		Limit:                                c.Limit,
		All:                                  c.All,
	}, nil
}

func conversationTrawlers(installedTrawlers []InstalledTrawler) []InstalledTrawler {
	trawlers := make([]InstalledTrawler, 0, len(installedTrawlers))
	for _, trawler := range installedTrawlers {
		if trawler.TrawlerDiscoveryError == nil && hasCapability(trawler, "conversations") {
			trawlers = append(trawlers, trawler)
		}
	}
	return trawlers
}

func (r *Runtime) federatedTrawlerConversationListOperation(
	trawlers []InstalledTrawler,
	query trawlkit.ConversationQuery,
) *federationv1.FederatedTrawlerConversationListOperation {
	results := make([]*federationv1.TrawlerConversationListResult, len(trawlers))
	localReferenceAliasesByCanonicalConversationReference := make([]map[string]string, len(trawlers))
	failures := make([]*federationv1.TrawlerOperationFailure, len(trawlers))
	var waitForTrawlers sync.WaitGroup
	waitForTrawlers.Add(len(trawlers))
	for index, trawler := range trawlers {
		index, trawler := index, trawler
		go func() {
			defer waitForTrawlers.Done()
			results[index], localReferenceAliasesByCanonicalConversationReference[index], failures[index] =
				r.listTrawlerConversations(trawler, query)
		}()
	}
	waitForTrawlers.Wait()

	operation := &federationv1.FederatedTrawlerConversationListOperation{}
	if !query.All && query.Limit > 0 {
		operation.ResultLimit = uint32(query.Limit)
	}
	for index := range trawlers {
		trawler := trawlers[index]
		if failures[index] != nil {
			operation.OperationFailures = append(operation.OperationFailures, failures[index])
			continue
		}
		if results[index] == nil {
			continue
		}
		operation.TrawlerConversationListResults = append(operation.TrawlerConversationListResults, results[index])
		response := results[index].GetConversationListResponse()
		if response.GetMoreConversationRecordsExist() {
			operation.MoreConversationRecordsExist = true
		}
		for conversationRecordIndex, conversationRecord := range response.GetConversationRecordsNewestFirst() {
			if conversationRecord == nil {
				operation.OperationFailures = append(operation.OperationFailures, federation.FailureForError(
					trawler.RegisteredTrawlerManifest,
					"conversations",
					fmt.Errorf("conversation record %d is missing", conversationRecordIndex),
				))
				continue
			}
			canonicalConversationRecordReference := strings.TrimSpace(
				conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
			)
			globallyRoutableTrawlLink, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
				RegisteredTrawlerManifestIdentity:              trawler.RegisteredTrawlerManifestIdentity,
				LocalShortReferenceAcceptedByRegisteredTrawler: localReferenceAliasesByCanonicalConversationReference[index][canonicalConversationRecordReference],
			})
			if err != nil {
				operation.OperationFailures = append(operation.OperationFailures, federation.FailureForError(trawler.RegisteredTrawlerManifest, "conversations", err))
				continue
			}
			operation.ConversationRecordsNewestFirst = append(operation.ConversationRecordsNewestFirst, &federationv1.FederatedConversationRecord{
				ConversationRecord:        conversationRecord,
				GloballyRoutableTrawlLink: globallyRoutableTrawlLink,
			})
		}
	}
	sort.SliceStable(operation.ConversationRecordsNewestFirst, func(left, right int) bool {
		leftTime := operation.ConversationRecordsNewestFirst[left].GetConversationRecord().GetMostRecentConversationActivityTime()
		rightTime := operation.ConversationRecordsNewestFirst[right].GetConversationRecord().GetMostRecentConversationActivityTime()
		if leftTime == nil || !leftTime.IsValid() {
			return false
		}
		if rightTime == nil || !rightTime.IsValid() {
			return true
		}
		return leftTime.AsTime().After(rightTime.AsTime())
	})
	if !query.All && query.Limit > 0 && len(operation.ConversationRecordsNewestFirst) > query.Limit {
		operation.ConversationRecordsNewestFirst = operation.ConversationRecordsNewestFirst[:query.Limit]
		operation.MoreConversationRecordsExist = true
	}
	if len(trawlers) == 0 {
		operation.Outcome = federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	} else {
		operation.Outcome = federatedOperationOutcome(
			len(operation.TrawlerConversationListResults),
			len(operation.OperationFailures),
			len(operation.TrawlersSkippedFromOperation),
		)
	}
	return operation
}

func (r *Runtime) listTrawlerConversations(
	trawler InstalledTrawler,
	query trawlkit.ConversationQuery,
) (*federationv1.TrawlerConversationListResult, map[string]string, *federationv1.TrawlerOperationFailure) {
	started := r.logTrawlerStart(trawler, "conversations")
	response, localReferenceAliasesByCanonicalConversationReference, err :=
		r.trawlerExecutor().Conversations(r.ctx, trawler.Trawler, query)
	err = trawlerExecutionError("conversations", err)
	if err != nil {
		r.logTrawlerDone(trawler, "conversations", started, err)
		failureError := err
		if isTimeoutError(err) {
			failureError = context.DeadlineExceeded
		}
		return nil, nil, federation.FailureForError(trawler.RegisteredTrawlerManifest, "conversations", failureError)
	}
	r.logTrawlerDone(trawler, "conversations", started, nil, "conversations="+fmt.Sprint(len(response.GetConversationRecordsNewestFirst())))
	return &federationv1.TrawlerConversationListResult{
		RegisteredTrawlerManifestIdentity: trawler.RegisteredTrawlerManifestIdentity,
		RegisteredTrawlerDisplayName:      trawlerHumanName(trawler),
		ConversationListResponse:          response,
	}, localReferenceAliasesByCanonicalConversationReference, nil
}

func (r *Runtime) resolveConversationPersonMatchFacts(
	installedTrawlers []InstalledTrawler,
	query string,
) ([]*personv1.PersonMatchFactsFromTrawler, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	resolvedPersonMatchFactsFromTrawlers := make(
		[]*personv1.PersonMatchFactsFromTrawler,
		0,
	)
	resolution := resolveWhoThroughContacts(r, installedTrawlers, query)
	if len(resolution.OperationFailures) > 0 {
		r.reportWhoFailures(resolution)
		if len(resolution.SourcesConsulted) == 0 {
			return nil, exitErr{code: 1}
		}
	}
	trawlerDisplayNamesByArchiveSourceIdentity := trawlerDisplayNamesByIdentity(
		installedTrawlers,
	)
	switch len(resolution.Candidates) {
	case 0:
		return nil, r.writeUnknownWho(
			query,
			resolution,
			trawlerDisplayNamesByArchiveSourceIdentity,
		)
	case 1:
		if closeResolution, closeSpellingOnlyMatch := closeSpellingOnlyResolution(resolution); closeSpellingOnlyMatch {
			return nil, r.writeUnknownWho(
				query,
				closeResolution,
				trawlerDisplayNamesByArchiveSourceIdentity,
			)
		}
	default:
		return nil, r.writeAmbiguousWho(
			query,
			resolution,
			trawlerDisplayNamesByArchiveSourceIdentity,
		)
	}
	selectedPerson := resolution.Candidates[0]
	return append(
		resolvedPersonMatchFactsFromTrawlers,
		selectedPerson.PersonMatchFactsFromTrawlers...,
	), nil
}
