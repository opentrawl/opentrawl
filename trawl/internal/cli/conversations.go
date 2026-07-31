package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/opentrawl/opentrawl/trawl/internal/federation"
	"github.com/opentrawl/opentrawl/trawlkit"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
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
	if operation.GetOutcome() != federationcontract.OperationOutcome_OPERATION_OUTCOME_FAILED {
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
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName(),
		false,
	)
}

func (c *ConversationsCmd) writeConversationListMoreAction(writer io.Writer, registeredTrawlerCommandName string) error {
	if _, err := fmt.Fprintln(writer); err != nil {
		return err
	}
	return render.WriteTrawlCommandHint(
		writer,
		"More: "+c.conversationListMoreCommand(writer, registeredTrawlerCommandName),
	)
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
		return trawlkit.ConversationQuery{}, usageErr{humanFacingUsageErrorMessage("--limit must be at least 1.")}
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
		if trawler.TrawlerDiscoveryError == nil &&
			supportsSharedTrawlerOperation(
				trawler,
				federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			) {
			trawlers = append(trawlers, trawler)
		}
	}
	return trawlers
}

func (r *Runtime) federatedTrawlerConversationListOperation(
	trawlers []InstalledTrawler,
	query trawlkit.ConversationQuery,
) *federationcontract.FederatedTrawlerConversationListOperation {
	results := make([]*federationcontract.TrawlerConversationListResult, len(trawlers))
	localShortReferencesByCanonicalConversationRecordReference := make(
		[][]trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
		len(trawlers),
	)
	failures := make([]*federationcontract.TrawlerOperationFailure, len(trawlers))
	var waitForTrawlers sync.WaitGroup
	waitForTrawlers.Add(len(trawlers))
	for index, trawler := range trawlers {
		index, trawler := index, trawler
		go func() {
			defer waitForTrawlers.Done()
			results[index], localShortReferencesByCanonicalConversationRecordReference[index], failures[index] =
				r.listTrawlerConversations(trawler, query)
		}()
	}
	waitForTrawlers.Wait()

	operation := &federationcontract.FederatedTrawlerConversationListOperation{}
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
					federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
					fmt.Errorf("conversation record %d is missing", conversationRecordIndex),
				))
				continue
			}
			localShortReference := trawlkit.LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(
				localShortReferencesByCanonicalConversationRecordReference[index],
				conversationRecord.GetCanonicalRecordReference(),
			)
			globallyRoutableTrawlLink, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
				RegisteredTrawler:   trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
				LocalShortReference: localShortReference,
			})
			if err != nil {
				operation.OperationFailures = append(operation.OperationFailures, federation.FailureForError(
					trawler.RegisteredTrawlerManifest,
					federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
					err,
				))
				continue
			}
			operation.ConversationRecordsNewestFirst = append(operation.ConversationRecordsNewestFirst, &federationcontract.FederatedConversationRecord{
				ConversationRecord: conversationRecord,
				TrawlLink:          globallyRoutableTrawlLink,
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
		operation.Outcome = federationcontract.OperationOutcome_OPERATION_OUTCOME_COMPLETE
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
) (*federationcontract.TrawlerConversationListResult, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, *federationcontract.TrawlerOperationFailure) {
	started := r.logTrawlerStart(trawler, "conversations")
	response, localShortReferencesByCanonicalConversationRecordReference, err :=
		r.trawlerExecutor().Conversations(r.ctx, trawler.Trawler, query)
	err = trawlerExecutionError("conversations", err)
	if err != nil {
		r.logTrawlerDone(trawler, "conversations", started, err)
		failureError := err
		if isTimeoutError(err) {
			failureError = context.DeadlineExceeded
		}
		return nil, nil, federation.FailureForError(
			trawler.RegisteredTrawlerManifest,
			federationcontract.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			failureError,
		)
	}
	r.logTrawlerDone(trawler, "conversations", started, nil, "conversations="+fmt.Sprint(len(response.GetConversationRecordsNewestFirst())))
	return &federationcontract.TrawlerConversationListResult{
		RegisteredTrawler:            trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		RegisteredTrawlerDisplayName: trawlerHumanName(trawler),
		ConversationListResponse:     response,
	}, localShortReferencesByCanonicalConversationRecordReference, nil
}

func (r *Runtime) resolveConversationPersonMatchFacts(
	installedTrawlers []InstalledTrawler,
	query string,
) ([]*person.PersonMatchFactsFromTrawler, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	resolvedPersonMatchFactsFromTrawlers := make(
		[]*person.PersonMatchFactsFromTrawler,
		0,
	)
	resolution := resolveWhoThroughContacts(r, installedTrawlers, query)
	if len(resolution.OperationFailures) > 0 {
		r.reportWhoFailures(resolution)
		if len(resolution.TrawlersConsulted) == 0 {
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
