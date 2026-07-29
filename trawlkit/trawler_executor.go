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

type typedTrawlerOperation interface {
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

func (e TrawlerExecutor) runTyped(ctx context.Context, trawler Trawler, command targetTrawlerCommand, operation typedTrawlerOperation) error {
	if ctx == nil {
		ctx = context.Background()
	}
	command.typed = operation
	return e.runner().runInProcess(ctx, trawler, command, e.globals(), false).err
}

func typedTrawlerCommand(trawler Trawler, name string, args ...string) (targetTrawlerCommand, error) {
	return resolveTrawlerCommand(trawler, append([]string{name}, args...))
}

type typedStatus struct {
	result *statusv1.TrawlerStatusResponse
}

func (operation *typedStatus) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	status, err := trawler.Status(ctx, req)
	operation.result = status
	return err
}

func (e TrawlerExecutor) Status(ctx context.Context, trawler Trawler) (*statusv1.TrawlerStatusResponse, error) {
	command, err := typedTrawlerCommand(trawler, "status")
	if err != nil {
		return nil, err
	}
	operation := &typedStatus{}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e TrawlerExecutor) Search(
	ctx context.Context,
	trawler Trawler,
	query Query,
) (*searchv1.TrawlerSearchResponse, map[string]string, error) {
	command, err := typedTrawlerCommand(trawler, "search")
	if err != nil {
		return nil, nil, err
	}
	operation := &typedSearch{query: query}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, nil, err
	}
	return operation.trawlerSearchResponse,
		operation.localReferenceAliasesByCanonicalSearchRecordReference,
		nil
}

type typedOpenRecord struct {
	ref      string
	anchorID string
	result   *openv1.OpenRecord
}

func (operation *typedOpenRecord) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
	opener, ok := trawler.(RecordOpener)
	if !ok {
		return errors.New("trawler does not support typed open")
	}
	req.RequestedPresentationAnchorIdentifier = operation.anchorID
	record, err := opener.OpenRecord(ctx, req, operation.ref)
	if err != nil {
		return err
	}
	if err := setGloballyRoutableTrawlLinkForConversationContainingOpenedMessage(ctx, req, record); err != nil {
		return err
	}
	operation.result = record
	return nil
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
	canonicalConversationRecordReference := strings.TrimSpace(
		openedMessageRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
	)
	if canonicalConversationRecordReference == "" {
		return errors.New("canonical conversation record reference containing opened message is missing")
	}
	shortReferencesByCanonicalRecordReference, err := req.ShortReferenceAliases(
		ctx,
		[]string{canonicalConversationRecordReference},
	)
	if err != nil {
		return fmt.Errorf("resolve conversation link for opened message: %w", err)
	}
	localShortReferenceAcceptedByRegisteredTrawler := strings.TrimSpace(
		shortReferencesByCanonicalRecordReference[canonicalConversationRecordReference],
	)
	if localShortReferenceAcceptedByRegisteredTrawler == "" {
		return errors.New("short reference for conversation containing opened message is missing")
	}
	globallyRoutableTrawlLink, err := ComposeGloballyRoutableTrawlLink(GloballyRoutableTrawlLinkRoute{
		RegisteredTrawlerManifestIdentity:              record.GetRegisteredTrawlerManifestIdentity(),
		LocalShortReferenceAcceptedByRegisteredTrawler: localShortReferenceAcceptedByRegisteredTrawler,
	})
	if err != nil {
		return fmt.Errorf("compose conversation link for opened message: %w", err)
	}
	openedMessageRecord.GloballyRoutableTrawlLinkForConversationContainingOpenedMessage = globallyRoutableTrawlLink
	if strings.TrimSpace(openedMessageRecord.GetGloballyRoutableTrawlLinkForConversationContainingOpenedMessage()) == "" {
		return errors.New("globally routable conversation link for opened message is missing")
	}
	return nil
}

func (e TrawlerExecutor) OpenRecord(ctx context.Context, trawler Trawler, ref, anchorID string) (*openv1.OpenRecord, error) {
	if _, ok := trawler.(RecordOpener); !ok {
		return nil, errors.New("trawler does not support typed open")
	}
	sharedCommands, err := supportedTrawlerCommandDeclarations(trawler)
	if err != nil {
		return nil, err
	}
	declaration := sharedTrawlerCommandDeclaration(sharedCommands, "open")
	command := targetTrawlerCommand{name: "open", args: []string{ref}, shared: declaration, storeMode: sharedTrawlerCommandArchiveAccessMode("open", declaration)}
	operation := &typedOpenRecord{ref: ref, anchorID: anchorID}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

type typedWho struct {
	query  string
	result *personv1.TrawlerPersonMatchResponse
}

func (operation *typedWho) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
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
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
		response,
	); err != nil {
		return err
	}
	operation.result = response
	return nil
}

func (e TrawlerExecutor) Who(ctx context.Context, trawler Trawler, query string) (*personv1.TrawlerPersonMatchResponse, error) {
	command, err := typedTrawlerCommand(trawler, "who", query)
	if err != nil {
		return nil, err
	}
	operation := &typedWho{query: query}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func setGloballyRoutableTrawlLinksForPersonMatchCandidates(
	ctx context.Context,
	req *TrawlerCommandExecutionRequest,
	registeredTrawlerManifestIdentity string,
	response *personv1.TrawlerPersonMatchResponse,
) error {
	canonicalPersonRecordReferences := make([]string, 0)
	for _, candidate := range response.GetPersonMatchCandidates() {
		canonicalPersonRecordReference := strings.TrimSpace(
			candidate.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		)
		if canonicalPersonRecordReference != "" {
			canonicalPersonRecordReferences = append(canonicalPersonRecordReferences, canonicalPersonRecordReference)
		}
	}
	if len(canonicalPersonRecordReferences) == 0 {
		return nil
	}
	localShortReferenceAliasesByCanonicalRecordReference, err := readAssignedLocalShortReferenceAliasesByCanonicalRecordReference(
		ctx,
		req,
		canonicalPersonRecordReferences,
	)
	if err != nil {
		return fmt.Errorf("resolve person links: %w", err)
	}
	globallyRoutableTrawlLinksByCanonicalRecordReference, err := ComposeGloballyRoutableTrawlLinksByCanonicalRecordReference(
		registeredTrawlerManifestIdentity,
		localShortReferenceAliasesByCanonicalRecordReference,
	)
	if err != nil {
		return fmt.Errorf("compose person links: %w", err)
	}
	for _, candidate := range response.GetPersonMatchCandidates() {
		canonicalPersonRecordReference := strings.TrimSpace(
			candidate.GetCanonicalPersonRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		)
		if canonicalPersonRecordReference == "" {
			continue
		}
		globallyRoutableTrawlLinkForPerson := strings.TrimSpace(
			globallyRoutableTrawlLinksByCanonicalRecordReference[canonicalPersonRecordReference],
		)
		if globallyRoutableTrawlLinkForPerson == "" {
			return fmt.Errorf("short reference for person %q is missing", canonicalPersonRecordReference)
		}
		candidate.GloballyRoutableTrawlLinkForPerson = globallyRoutableTrawlLinkForPerson
	}
	return nil
}

func (e TrawlerExecutor) Conversations(
	ctx context.Context,
	trawler Trawler,
	query ConversationQuery,
) (*conversationv1.ConversationListResponse, map[string]string, error) {
	command, err := typedTrawlerCommand(trawler, "conversations")
	if err != nil {
		return nil, nil, err
	}
	operation := &typedConversations{query: query}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, nil, err
	}
	return operation.response,
		operation.localReferenceAliasesByCanonicalConversationReference,
		nil
}

func (e TrawlerExecutor) Messages(
	ctx context.Context,
	trawler Trawler,
	query TrawlerMessageListQuery,
) (*messagev1.MessageListResponse, error) {
	command, err := typedTrawlerCommand(trawler, "messages")
	if err != nil {
		return nil, err
	}
	operation := &typedTrawlerMessageList{query: query}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.response, nil
}

type typedPeopleSnapshot struct {
	result *personv1.TrawlerPeopleSnapshot
}

func (operation *typedPeopleSnapshot) execute(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) error {
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
	operation := &typedPeopleSnapshot{}
	command := targetTrawlerCommand{name: "people", storeMode: storeRead}
	if err := e.runTyped(ctx, trawler, command, operation); err != nil {
		return nil, err
	}
	return operation.result, nil
}

func (e TrawlerExecutor) ReconcilePeople(ctx context.Context, destination Trawler, source string, snapshot *personv1.TrawlerPeopleSnapshot) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := destination.(PeopleReconciler); !ok {
		return errors.New("destination does not own a People archive")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("people snapshot trawler identity is required")
	}
	if err := ValidateTrawlerPeopleSnapshot(snapshot); err != nil {
		return fmt.Errorf("invalid people snapshot: %w", err)
	}
	r := e.runner()
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	r.opts.childRequest = &workerv1.Request{Operation: &workerv1.Request_ReconcilePeople{ReconcilePeople: &workerv1.ReconcilePeople{
		PeopleSnapshotRegisteredTrawlerManifestIdentity: source,
		TrawlerPeopleSnapshot:                           snapshot,
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
	argv := append([]string{"sync"}, args...)
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
	map[string]string,
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
		result.localShortReferenceAliasesByCanonicalRecordReference,
		result.trawlerCommandRenderContext,
		nil
}
