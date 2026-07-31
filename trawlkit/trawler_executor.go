package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
	workerv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// TrawlerExecutor is the typed host boundary for running trawler operations.
// Every method uses the same paths, configuration, logging, deadline, archive
// preparation and store lifecycle as the trawler CLI.
type TrawlerExecutor struct {
	opts TrawlerExecutorOptions
}

type TrawlerExecutorOptions struct {
	StateRoot string
	Timeout   time.Duration
	Verbosity int
	Stderr    io.Writer
}

func NewTrawlerExecutor(opts TrawlerExecutorOptions) TrawlerExecutor {
	return TrawlerExecutor{opts: opts}
}

type sharedTrawlerOperationExecution interface {
	execute(context.Context, Trawler, *TrawlerCommandExecutionRequest) error
}

func (e TrawlerExecutor) runner() runner {
	r := runner{opts: defaultRunOptions()}
	r.opts.stderr = e.opts.Stderr
	r.opts.readTimeout = e.opts.Timeout
	r.opts = r.opts.withDefaults()
	return r
}

func (e TrawlerExecutor) globals() globalOptions {
	return globalOptions{stateRoot: e.opts.StateRoot, verbosity: e.opts.Verbosity}
}

func (e TrawlerExecutor) runSharedTrawlerOperation(ctx context.Context, trawler Trawler, command targetTrawlerCommand, operation sharedTrawlerOperationExecution) error {
	if ctx == nil {
		ctx = context.Background()
	}
	command.sharedOperationExecution = operation
	return e.runner().runInProcess(ctx, trawler, command, e.globals(), false).err
}

func sharedTrawlerOperationTargetCommand(
	trawler Trawler,
	operation federationv1.SharedTrawlerOperation,
	args ...string,
) (targetTrawlerCommand, error) {
	if !sharedTrawlerOperationIsSupported(trawler, operation) {
		return targetTrawlerCommand{}, unsupportedSharedTrawlerCommandInterfaceError(
			sharedTrawlerOperationCommandName(operation),
			unsupportedSharedTrawlerCommandInterface(trawler, operation),
		)
	}
	sharedCommands, err := supportedTrawlerCommandDeclarations(trawler)
	if err != nil {
		return targetTrawlerCommand{}, err
	}
	declaration := sharedTrawlerCommandDeclaration(sharedCommands, operation)
	return targetTrawlerCommand{
		args:            append([]string(nil), args...),
		mutates:         operation == federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
		sharedOperation: operation,
		shared:          declaration,
		storeMode:       sharedTrawlerCommandArchiveAccessMode(operation, declaration),
	}, nil
}

type executeTrawlerStatusOperation struct {
	result *statusv1.TrawlerStatusResponse
}

func (operation *executeTrawlerStatusOperation) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	status, err := trawler.Status(ctx, req)
	operation.result = status
	return err
}

func (e TrawlerExecutor) Status(ctx context.Context, trawler Trawler) (*statusv1.TrawlerStatusResponse, error) {
	command, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
	)
	if err != nil {
		return nil, err
	}
	operation := &executeTrawlerStatusOperation{}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e TrawlerExecutor) Search(
	ctx context.Context,
	trawler Trawler,
	query Query,
) (*searchv1.TrawlerSearchResponse, []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	command, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
	)
	if err != nil {
		return nil, nil, err
	}
	operation := &executeTrawlerSearchOperation{query: query}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, nil, err
	}
	return operation.trawlerSearchResponse,
		operation.localShortReferencesByCanonicalSearchRecordReference,
		nil
}

type executeTrawlerOpenRecordOperation struct {
	localShortReference *LocalTrawlerShortReference
	recordAnchor        *RecordAnchorIdentifier
	result              *openv1.OpenRecord
}

func (operation *executeTrawlerOpenRecordOperation) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	conversationRecordIdentifiedByLocalTrawlerShortReference, err :=
		findConversationRecordIdentifiedByLocalTrawlerShortReference(
			ctx,
			trawler,
			req,
			operation.localShortReference,
		)
	if err != nil {
		return err
	}
	if conversationRecordIdentifiedByLocalTrawlerShortReference != nil {
		operation.result = &openv1.OpenRecord{
			RecordTrawler:            trawler.RegisteredTrawlerDeclaration().RegisteredTrawler,
			CanonicalRecordReference: conversationRecordIdentifiedByLocalTrawlerShortReference.GetCanonicalRecordReference(),
			TypedOpenedRecord: &openv1.OpenRecord_ConversationRecord{
				ConversationRecord: conversationRecordIdentifiedByLocalTrawlerShortReference,
			},
		}
		return nil
	}
	opener, ok := trawler.(RecordOpener)
	if !ok {
		return errors.New("trawler does not support typed open")
	}
	req.RequestedRecordAnchor = operation.recordAnchor
	record, err := opener.OpenRecord(ctx, req, operation.localShortReference)
	if err != nil {
		return err
	}
	if err := setGloballyRoutableTrawlLinkForConversationContainingOpenedMessage(ctx, req, record); err != nil {
		return err
	}
	operation.result = record
	return nil
}

func findConversationRecordIdentifiedByLocalTrawlerShortReference(
	ctx context.Context,
	trawler Trawler,
	request *TrawlerCommandExecutionRequest,
	localShortReference *LocalTrawlerShortReference,
) (*conversationv1.ConversationRecord, error) {
	conversationLister, supportsConversations := trawler.(ConversationLister)
	if !supportsConversations {
		return nil, nil
	}
	canonicalRecordReferences, err := request.ResolveShortReference(ctx, localShortReference)
	if err != nil {
		return nil, err
	}
	if len(canonicalRecordReferences) != 1 {
		return nil, ErrUnknownShortRef
	}
	requestedCanonicalRecordReference :=
		CanonicalArchiveRecordReferenceText(canonicalRecordReferences[0])
	response, err := executeConversations(
		ctx,
		conversationLister,
		request,
		ConversationQuery{All: true},
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawler,
	)
	if err != nil {
		return nil, err
	}
	for _, conversationRecord := range response.GetConversationRecordsNewestFirst() {
		if CanonicalArchiveRecordReferenceText(conversationRecord.GetCanonicalRecordReference()) ==
			requestedCanonicalRecordReference {
			return conversationRecord, nil
		}
	}
	return nil, nil
}

func setGloballyRoutableTrawlLinkForConversationContainingOpenedMessage(
	ctx context.Context,
	req *TrawlerCommandExecutionRequest,
	record *openv1.OpenRecord,
) error {
	openedMessageRecord := record.GetOpenedMessageRecordWithConversationContext()
	if openedMessageRecord == nil {
		return nil
	}
	canonicalConversationRecordReference := openedMessageRecord.GetConversationRecordReference()
	if CanonicalArchiveRecordReferenceText(canonicalConversationRecordReference) == "" {
		return errors.New("canonical conversation record reference containing opened message is missing")
	}
	shortReferencesByCanonicalRecordReference, err := req.LocalShortReferencesForCanonicalArchiveRecordReferences(
		ctx,
		[]*CanonicalArchiveRecordReference{canonicalConversationRecordReference},
	)
	if err != nil {
		return fmt.Errorf("resolve conversation link for opened message: %w", err)
	}
	localShortReferenceAcceptedByRegisteredTrawler :=
		LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(
			shortReferencesByCanonicalRecordReference,
			canonicalConversationRecordReference,
		)
	if LocalTrawlerShortReferenceText(localShortReferenceAcceptedByRegisteredTrawler) == "" {
		return errors.New("short reference for conversation containing opened message is missing")
	}
	globallyRoutableTrawlLink, err := ComposeGloballyRoutableTrawlLink(GloballyRoutableTrawlLinkRoute{
		RegisteredTrawler:   record.GetRecordTrawler(),
		LocalShortReference: localShortReferenceAcceptedByRegisteredTrawler,
	})
	if err != nil {
		return fmt.Errorf("compose conversation link for opened message: %w", err)
	}
	openedMessageRecord.ConversationTrawlLink = globallyRoutableTrawlLink
	if GloballyRoutableTrawlLinkText(openedMessageRecord.GetConversationTrawlLink()) == "" {
		return errors.New("globally routable conversation link for opened message is missing")
	}
	return nil
}

func (e TrawlerExecutor) OpenRecord(
	ctx context.Context,
	trawler Trawler,
	localShortReference *LocalTrawlerShortReference,
	recordAnchor *RecordAnchorIdentifier,
) (*openv1.OpenRecord, error) {
	if _, ok := trawler.(RecordOpener); !ok {
		return nil, errors.New("trawler does not support typed open")
	}
	sharedCommands, err := supportedTrawlerCommandDeclarations(trawler)
	if err != nil {
		return nil, err
	}
	sharedOperation := federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN
	declaration := sharedTrawlerCommandDeclaration(sharedCommands, sharedOperation)
	command := targetTrawlerCommand{
		args:            []string{LocalTrawlerShortReferenceText(localShortReference)},
		sharedOperation: sharedOperation,
		shared:          declaration,
		storeMode:       sharedTrawlerCommandArchiveAccessMode(sharedOperation, declaration),
	}
	operation := &executeTrawlerOpenRecordOperation{localShortReference: localShortReference, recordAnchor: recordAnchor}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

type executeTrawlerPersonMatchOperation struct {
	query  string
	result *personv1.TrawlerPersonMatchResponse
}

func (operation *executeTrawlerPersonMatchOperation) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	matcher, ok := trawler.(WhoMatcher)
	if !ok {
		return errors.New("trawler does not support who")
	}
	response, err := matcher.Who(ctx, req, operation.query)
	if err != nil {
		return err
	}
	if err := setGloballyRoutableTrawlLinksForPersonMatchCandidates(
		ctx,
		req,
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawler,
		response,
	); err != nil {
		return err
	}
	operation.result = response
	return nil
}

func (e TrawlerExecutor) Who(ctx context.Context, trawler Trawler, query string) (*personv1.TrawlerPersonMatchResponse, error) {
	command, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
		query,
	)
	if err != nil {
		return nil, err
	}
	operation := &executeTrawlerPersonMatchOperation{query: query}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func setGloballyRoutableTrawlLinksForPersonMatchCandidates(
	ctx context.Context,
	req *TrawlerCommandExecutionRequest,
	registeredTrawler *RegisteredTrawlerIdentity,
	response *personv1.TrawlerPersonMatchResponse,
) error {
	canonicalPersonRecordReferences := make([]*CanonicalArchiveRecordReference, 0)
	for _, candidate := range response.GetPersonMatchCandidates() {
		canonicalPersonRecordReference := candidate.GetCanonicalPersonRecordReference()
		if CanonicalArchiveRecordReferenceText(canonicalPersonRecordReference) != "" {
			canonicalPersonRecordReferences = append(canonicalPersonRecordReferences, canonicalPersonRecordReference)
		}
	}
	if len(canonicalPersonRecordReferences) == 0 {
		return nil
	}
	localShortReferencesByCanonicalRecordReference, err := readAssignedLocalShortReferencesByCanonicalRecordReference(
		ctx,
		req,
		canonicalPersonRecordReferences,
	)
	if err != nil {
		return fmt.Errorf("resolve person links: %w", err)
	}
	for _, candidate := range response.GetPersonMatchCandidates() {
		canonicalPersonRecordReference := candidate.GetCanonicalPersonRecordReference()
		if CanonicalArchiveRecordReferenceText(canonicalPersonRecordReference) == "" {
			continue
		}
		localShortReference := LocalTrawlerShortReferenceForCanonicalArchiveRecordReference(
			localShortReferencesByCanonicalRecordReference,
			canonicalPersonRecordReference,
		)
		if LocalTrawlerShortReferenceText(localShortReference) == "" {
			return fmt.Errorf("short reference for person %q is missing", CanonicalArchiveRecordReferenceText(canonicalPersonRecordReference))
		}
		personTrawlLink, err := ComposeGloballyRoutableTrawlLink(GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   registeredTrawler,
			LocalShortReference: localShortReference,
		})
		if err != nil {
			return fmt.Errorf("compose person link: %w", err)
		}
		candidate.PersonTrawlLink = personTrawlLink
	}
	return nil
}

func (e TrawlerExecutor) Conversations(
	ctx context.Context,
	trawler Trawler,
	query ConversationQuery,
) (*conversationv1.ConversationListResponse, []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	command, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
	)
	if err != nil {
		return nil, nil, err
	}
	operation := &executeTrawlerConversationListOperation{query: query}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, nil, err
	}
	return operation.response,
		operation.localShortReferencesByCanonicalConversationRecordReference,
		nil
}

func (e TrawlerExecutor) Messages(
	ctx context.Context,
	trawler Trawler,
	query TrawlerMessageListQuery,
) (*messagev1.MessageListResponse, error) {
	command, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
	)
	if err != nil {
		return nil, err
	}
	operation := &executeTrawlerMessageListOperation{query: query}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.response, nil
}

type executeTrawlerPeopleSnapshotOperation struct {
	result *personv1.TrawlerPeopleSnapshot
}

func (operation *executeTrawlerPeopleSnapshotOperation) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	provider, ok := trawler.(PeopleSnapshotProvider)
	if !ok {
		return errors.New("trawler does not provide People identities")
	}
	snapshot, err := provider.PeopleSnapshot(ctx, req)
	if err == nil {
		if validationError := ValidateTrawlerPeopleSnapshot(snapshot); validationError != nil {
			return fmt.Errorf("invalid people snapshot: %w", validationError)
		}
	}
	operation.result = snapshot
	return err
}

func (e TrawlerExecutor) PeopleSnapshot(ctx context.Context, trawler Trawler) (*personv1.TrawlerPeopleSnapshot, error) {
	operation := &executeTrawlerPeopleSnapshotOperation{}
	command := targetTrawlerCommand{name: "people", storeMode: storeRead}
	if err := e.runSharedTrawlerOperation(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e TrawlerExecutor) ReconcilePeople(
	ctx context.Context,
	destination Trawler,
	peopleSnapshotTrawler *RegisteredTrawlerIdentity,
	snapshot *personv1.TrawlerPeopleSnapshot,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := destination.(PeopleReconciler); !ok {
		return errors.New("destination does not own a People archive")
	}
	if RegisteredTrawlerIdentityText(peopleSnapshotTrawler) == "" {
		return errors.New("people snapshot trawler identity is required")
	}
	if err := ValidateTrawlerPeopleSnapshot(snapshot); err != nil {
		return fmt.Errorf("invalid people snapshot: %w", err)
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	r.opts.childRequest = &workerv1.Request{Operation: &workerv1.Request_ReconcilePeople{ReconcilePeople: &workerv1.ReconcilePeople{
		PeopleSnapshotTrawler: peopleSnapshotTrawler,
		TrawlerPeopleSnapshot: snapshot,
	}}}
	command := targetTrawlerCommand{name: internalPeopleReconcileTrawlerCommand, mutates: true, storeMode: storeWrite}
	return r.runChild(ctx, destination, command, e.globals()).err
}

func (e TrawlerExecutor) Sync(ctx context.Context, trawler Trawler, args []string) (*syncv1.TrawlerArchiveSyncReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	argv := append([]string{"update"}, args...)
	result := r.dispatch(ctx, trawler, argv, e.globals(), false)
	if result.err != nil {
		return nil, result.err
	}
	if result.syncReport == nil {
		return nil, fmt.Errorf("sync child returned no typed sync result")
	}
	return result.syncReport, nil
}

// ExecuteDeclaredTrawlerCommand runs one declared trawler command and
// returns its typed facts, each local short-reference alias indexed by its
// canonical record reference, and the context required by the shared human renderer.
func (e TrawlerExecutor) ExecuteDeclaredTrawlerCommand(
	ctx context.Context,
	trawler Trawler,
	trawlerCommandArguments []string,
) (
	*commandv1.TrawlerCommandResponse,
	[]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
	render.TrawlerCommandRenderContext,
	error,
) {
	command, err := resolveTrawlerCommand(trawler, trawlerCommandArguments)
	if err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, err
	}
	if command.bespoke == nil && command.shared == nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, usageError{err: fmt.Errorf(
			"%q is not a declared trawler command",
			strings.Join(command.childArgs(), " "),
		)}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	var result executionResult
	if command.mutates {
		result = r.runChild(ctx, trawler, command, e.globals())
	} else {
		result = r.runInProcess(ctx, trawler, command, e.globals(), false)
	}
	if result.err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, result.err
	}
	if result.trawlerCommandResponse == nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, errors.New("declared trawler command returned no typed response")
	}
	return result.trawlerCommandResponse,
		result.localShortReferencesByCanonicalRecordReference,
		result.trawlerCommandRenderContext,
		nil
}
