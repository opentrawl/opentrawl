package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

// PluginCapabilityDeclarator is implemented by trawlers that declare network
// hosts, secret keys, read paths, or auth origins for standalone plugin
// execution.
type PluginCapabilityDeclarator interface {
	DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities
}

// ExternalPluginManifestProvider returns the pre-probed manifest for an
// out-of-process standalone trawler plugin.
type ExternalPluginManifestProvider interface {
	ExternalPluginManifest() *federation.RegisteredTrawlerManifest
}

// ExternalPluginCapabilityProvider returns the probed capability declarations
// of an out-of-process standalone trawler plugin.
type ExternalPluginCapabilityProvider interface {
	ExternalPluginDeclaredCapabilities() *plugincontract.PluginDeclaredCapabilities
}

// TrawlerSupportsPeopleSnapshot reports whether trawler provides People
// identity snapshots either in-process or via an external plugin executable.
func TrawlerSupportsPeopleSnapshot(trawler Trawler) bool {
	if _, ok := trawler.(PeopleSnapshotProvider); ok {
		return true
	}
	if provider, ok := trawler.(ExternalPluginCapabilityProvider); ok {
		return provider.ExternalPluginDeclaredCapabilities().GetSupportsPeopleSnapshotExport()
	}
	return false
}

// TrawlerSupportsPersonMessageCounting reports whether trawler counts person
// messages either in-process or via an external plugin executable.
func TrawlerSupportsPersonMessageCounting(trawler Trawler) bool {
	if _, ok := trawler.(PersonMessageCounter); ok {
		return true
	}
	if provider, ok := trawler.(ExternalPluginCapabilityProvider); ok {
		return provider.ExternalPluginDeclaredCapabilities().GetSupportsPersonMessageCounting()
	}
	return false
}

// ExternalPluginExecutor is implemented by host-side adapters for standalone
// plugin executables so core dispatches all operations over the sandboxed
// protobuf wire protocol without opening the plugin's private archive directly.
type ExternalPluginExecutor interface {
	ExecuteExternalPluginStatus(ctx context.Context, opts TrawlerExecutorOptions) (*status.TrawlerStatusResponse, error)
	ExecuteExternalPluginSearch(ctx context.Context, opts TrawlerExecutorOptions, query Query) (*search.TrawlerSearchResponse, []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error)
	ExecuteExternalPluginOpenRecord(ctx context.Context, opts TrawlerExecutorOptions, localShortReference *LocalTrawlerShortReference, recordAnchor *RecordAnchorIdentifier) (*open.OpenRecord, error)
	ExecuteExternalPluginUpdate(ctx context.Context, opts TrawlerExecutorOptions, args []string) (*update.TrawlerArchiveUpdateReport, error)
	ExecuteExternalPluginDeclaredCommand(ctx context.Context, opts TrawlerExecutorOptions, args []string) (*command.TrawlerCommandResponse, []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, render.TrawlerCommandRenderContext, error)
	ExecuteExternalPluginSyncArchiveIndex(ctx context.Context, stateRoot string, forceFullRebuild bool) (bool, error)
	ExternalPluginDeclaredCommandWritesNoFiles(arguments []string) (bool, error)
	ExecuteExternalPluginWho(ctx context.Context, opts TrawlerExecutorOptions, query string) (*person.TrawlerPersonMatchResponse, error)
	ExecuteExternalPluginCountPersonMessages(ctx context.Context, opts TrawlerExecutorOptions, personFilter *person.ExactPersonMessageCountRequest) (*person.ExactPersonMessageCountResponse, error)
	ExecuteExternalPluginConversations(ctx context.Context, opts TrawlerExecutorOptions, query ConversationQuery) (*conversation.ConversationListResponse, []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error)
	ExecuteExternalPluginMessages(ctx context.Context, opts TrawlerExecutorOptions, query TrawlerMessageListQuery) (*message.MessageListResponse, error)
	ExecuteExternalPluginPeopleSnapshot(ctx context.Context, opts TrawlerExecutorOptions) (*person.TrawlerPeopleSnapshot, error)
}

// ArchiveStoreAccessRequirement describes how a standalone plugin SDK process
// should open its private SQLite archive for a given command.
type ArchiveStoreAccessRequirement int

const (
	ArchiveStoreAccessNotNeeded ArchiveStoreAccessRequirement = iota
	ArchiveStoreAccessReadOnlyOptional
	ArchiveStoreAccessReadOnlyRequired
	ArchiveStoreAccessReadOnlyWithoutWritingFiles
	ArchiveStoreAccessReadWrite
)

// BuildTrawlerPluginManifest constructs the complete manifest and capability
// declaration returned by a standalone Go plugin during manifest probing.
func BuildTrawlerPluginManifest(trawler Trawler) (*plugincontract.PluginManifestResponse, error) {
	manifest, err := Manifest(trawler)
	if err != nil {
		return nil, err
	}
	capabilities := &plugincontract.PluginDeclaredCapabilities{}
	if declarator, ok := trawler.(PluginCapabilityDeclarator); ok {
		if declared := declarator.DeclaredPluginCapabilities(); declared != nil {
			capabilities = proto.Clone(declared).(*plugincontract.PluginDeclaredCapabilities)
		}
	}
	if _, ok := trawler.(ArchiveIndexChangeExporter); ok {
		capabilities.SupportsArchiveIndexChangeExport = true
	}
	if _, ok := trawler.(WhoMatcher); ok {
		capabilities.SupportsWhoPersonMatching = true
	}
	if _, ok := trawler.(PersonMessageCounter); ok {
		capabilities.SupportsPersonMessageCounting = true
	}
	if _, ok := trawler.(ConversationLister); ok {
		capabilities.SupportsConversationListing = true
	}
	if _, ok := trawler.(TrawlerMessageLister); ok {
		capabilities.SupportsMessageListing = true
	}
	if _, ok := trawler.(PeopleSnapshotProvider); ok {
		capabilities.SupportsPeopleSnapshotExport = true
	}
	mutatingSeen := make(map[string]struct{})
	for _, existing := range capabilities.MutatingBespokeCommandNames {
		if normalized := strings.Join(strings.Fields(existing), " "); normalized != "" {
			mutatingSeen[normalized] = struct{}{}
		}
	}
	for _, cmd := range trawler.TrawlerCommands() {
		if cmd.SharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED || !cmd.TrawlerCommandChangesArchive {
			continue
		}
		normalized := strings.Join(strings.Fields(cmd.TrawlerCommandName), " ")
		if normalized == "" {
			continue
		}
		if _, dup := mutatingSeen[normalized]; !dup {
			mutatingSeen[normalized] = struct{}{}
			capabilities.MutatingBespokeCommandNames = append(capabilities.MutatingBespokeCommandNames, normalized)
		}
	}
	return &plugincontract.PluginManifestResponse{
		RegisteredTrawlerManifest: manifest,
		DeclaredCapabilities:      capabilities,
	}, nil
}

// ResolveExternalPluginArchivePathLayout resolves the host-owned directory and
// default archive paths for a standalone plugin identifier.
func ResolveExternalPluginArchivePathLayout(stateRoot string, trawlerID string, archiveDirOverride string) (TrawlerArchivePaths, string, error) {
	decl := RegisteredTrawlerDeclaration{
		RegisteredTrawler: NewRegisteredTrawlerIdentity(trawlerID),
	}
	if strings.TrimSpace(archiveDirOverride) != "" {
		decl.DefaultTrawlerArchivePaths = TrawlerArchivePaths{
			TrawlerArchiveDirectoryPath: archiveDirOverride,
		}
	}
	paths, err := resolveTrawlerArchivePaths(stateRoot, decl)
	if err != nil {
		return TrawlerArchivePaths{}, "", err
	}
	return paths.TrawlerArchivePaths, paths.Base, nil
}

// OpenExternalPluginArchiveStore opens a plugin's private SQLite archive inside
// the plugin process using the exact store access semantics required for the
// current operation.
func OpenExternalPluginArchiveStore(ctx context.Context, archivePath string, access ArchiveStoreAccessRequirement) (*store.Store, error) {
	var mode storeMode
	switch access {
	case ArchiveStoreAccessNotNeeded:
		mode = storeNone
	case ArchiveStoreAccessReadOnlyOptional:
		mode = storeOptional
	case ArchiveStoreAccessReadOnlyRequired:
		mode = storeRead
	case ArchiveStoreAccessReadOnlyWithoutWritingFiles:
		mode = storeReadWithoutWritingFiles
	case ArchiveStoreAccessReadWrite:
		mode = storeWrite
	default:
		return nil, fmt.Errorf("unknown archive store access requirement %d", access)
	}
	if mode == storeOptional || mode == storeRead {
		// Inside a read-only sandbox the host already holds the shared archive
		// file-set lock; open read-only without creating lock/WAL sidecars.
		opened, err := store.OpenReadOnlyWithoutWritingFiles(ctx, archivePath)
		if errors.Is(err, os.ErrNotExist) {
			if mode == storeOptional {
				return nil, nil
			}
			return nil, NewMissingArchiveError(archivePath)
		}
		return opened, err
	}
	return openStore(ctx, TrawlerArchivePaths{TrawlerArchivePath: archivePath}, mode)
}

// ExecuteBespokeCommandForExternalPluginSDK resolves flags, opens the archive
// with the declared store mode, and runs one bespoke or namespace-routed shared
// trawler command inside a standalone plugin process.
func ExecuteBespokeCommandForExternalPluginSDK(
	ctx context.Context,
	trawler Trawler,
	commandArguments []string,
	req *TrawlerCommandExecutionRequest,
) (*command.TrawlerCommandResponse, bool, error) {
	targetCommand, err := resolveTrawlerCommand(trawler, commandArguments)
	if err != nil {
		return nil, false, err
	}
	targetCommand, err = parseTargetTrawlerCommandFlags(targetCommand)
	if err != nil {
		return nil, false, err
	}
	if targetCommand.sharedOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		parsedArgs, flagErr := parseSharedTrawlerCommandFlags(
			targetCommand.sharedOperation,
			targetCommand.shared,
			targetCommand.args,
			targetCommand.sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		)
		if flagErr != nil {
			return nil, false, flagErr
		}
		targetCommand.args = parsedArgs
	}
	access := archiveStoreAccessFromStoreMode(targetCommand.storeMode)
	openedStore, err := OpenExternalPluginArchiveStore(ctx, req.TrawlerArchivePaths.TrawlerArchivePath, access)
	if err != nil {
		return nil, targetCommand.mutates, err
	}
	if openedStore != nil {
		defer func() { _ = openedStore.Close() }()
		req.OpenedTrawlerArchiveStore = openedStore
	}
	response, err := executeTrawlerCommand(ctx, trawler, targetCommand, req)
	return response, targetCommand.mutates, err
}

func archiveStoreAccessFromStoreMode(mode storeMode) ArchiveStoreAccessRequirement {
	switch mode {
	case storeNone:
		return ArchiveStoreAccessNotNeeded
	case storeOptional:
		return ArchiveStoreAccessReadOnlyOptional
	case storeRead:
		return ArchiveStoreAccessReadOnlyRequired
	case storeReadWithoutWritingFiles:
		return ArchiveStoreAccessReadOnlyWithoutWritingFiles
	case storeWrite:
		return ArchiveStoreAccessReadWrite
	default:
		return ArchiveStoreAccessReadOnlyRequired
	}
}

// ExecuteUpdateForExternalPluginSDK parses update-specific flags declared by
// the trawler, then runs Updater.Update and optional
// SuccessfullyCompletedArchiveUpdateRecorder inside a standalone plugin.
func ExecuteUpdateForExternalPluginSDK(
	ctx context.Context,
	trawler Trawler,
	req *TrawlerCommandExecutionRequest,
	updateArgs []string,
) (*update.TrawlerArchiveUpdateReport, error) {
	targetCommand, err := sharedTrawlerOperationTargetCommand(
		trawler,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		updateArgs...,
	)
	if err != nil {
		return nil, err
	}
	remainingArgs, err := parseSharedTrawlerCommandFlags(
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		targetCommand.shared,
		targetCommand.args,
		false,
	)
	if err != nil {
		return nil, err
	}
	req.TrawlerCommandPositionalArguments = remainingArgs
	return executeUpdate(ctx, trawler, req)
}

// ExecuteSearchForExternalPluginSDK runs Searcher.Search and stamps the
// trawler display name on every returned match presentation.
func ExecuteSearchForExternalPluginSDK(
	ctx context.Context,
	trawler Trawler,
	req *TrawlerCommandExecutionRequest,
	query Query,
) (*search.TrawlerSearchResponse, error) {
	searcher, ok := trawler.(Searcher)
	if !ok {
		return nil, errors.New("trawler does not support search")
	}
	return executeSearch(ctx, searcher, req, query, trawler.RegisteredTrawlerDeclaration().RegisteredTrawlerDisplayName)
}

// ExecuteConversationsForExternalPluginSDK runs ConversationLister with the
// shared unread and limit filtering rules.
func ExecuteConversationsForExternalPluginSDK(
	ctx context.Context,
	trawler Trawler,
	req *TrawlerCommandExecutionRequest,
	query ConversationQuery,
) (*conversation.ConversationListResponse, error) {
	lister, ok := trawler.(ConversationLister)
	if !ok {
		return nil, errors.New("trawler does not support conversations")
	}
	return executeConversations(ctx, lister, req, query, trawler.RegisteredTrawlerDeclaration().RegisteredTrawler)
}

// ExecuteMessagesForExternalPluginSDK runs TrawlerMessageLister with the
// shared normalization and filtering rules.
func ExecuteMessagesForExternalPluginSDK(
	ctx context.Context,
	trawler Trawler,
	req *TrawlerCommandExecutionRequest,
	query TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	lister, ok := trawler.(TrawlerMessageLister)
	if !ok {
		return nil, errors.New("trawler does not support messages")
	}
	return executeTrawlerMessageList(ctx, lister, req, query)
}

// ExternalPluginRenderContextForCommandResponse builds the shared render
// context (including pagination --limit hints) for an external plugin command
// response.
func ExternalPluginRenderContextForCommandResponse(
	manifest *federation.RegisteredTrawlerManifest,
	commandArguments []string,
	response *command.TrawlerCommandResponse,
) render.TrawlerCommandRenderContext {
	decl := RegisteredTrawlerDeclaration{
		RegisteredTrawler:            manifest.GetRegisteredTrawler(),
		RegisteredTrawlerCommandName: manifest.GetRegisteredTrawlerCommandName(),
		RegisteredTrawlerDisplayName: manifest.GetRegisteredTrawlerDisplayName(),
	}
	target := matchManifestCommandForRenderContext(manifest, commandArguments)
	return trawlerCommandRenderContext(decl, target, response)
}

func matchManifestCommandForRenderContext(
	manifest *federation.RegisteredTrawlerManifest,
	commandArguments []string,
) targetTrawlerCommand {
	for _, declaration := range manifest.GetRegisteredTrawlerCommandDeclarations() {
		if declaration == nil {
			continue
		}
		commandName := strings.TrimSpace(declaration.GetBespokeTrawlerCommandName())
		if commandName == "" {
			continue
		}
		tokens := strings.Fields(commandName)
		if len(tokens) > 0 && len(commandArguments) >= len(tokens) {
			matched := true
			for i, token := range tokens {
				if commandArguments[i] != token {
					matched = false
					break
				}
			}
			if matched {
				return targetTrawlerCommand{
					name:                commandKey(commandName),
					tokens:              tokens,
					invocationArguments: append([]string(nil), commandArguments[len(tokens):]...),
					bespoke: &TrawlerCommand{
						TrawlerCommandName:                commandName,
						TrawlerCommandRunsAtNamespaceRoot: declaration.GetTrawlerCommandRunsAtNamespaceRoot(),
					},
				}
			}
		}
	}
	if len(commandArguments) > 0 && commandArguments[0] == sharedTrawlerOperationCommandName(federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES) {
		return targetTrawlerCommand{
			name:                "messages",
			tokens:              []string{"messages"},
			invocationArguments: append([]string(nil), commandArguments[1:]...),
			sharedOperation:     federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
		}
	}
	return targetTrawlerCommand{
		invocationArguments: append([]string(nil), commandArguments...),
	}
}

// CollectCanonicalRecordReferencesFromCommandResponse extracts all canonical
// record references present in a bespoke command response.
func CollectCanonicalRecordReferencesFromCommandResponse(response *command.TrawlerCommandResponse) []*CanonicalArchiveRecordReference {
	return trawlerCommandResponseCanonicalRecordReferences(response)
}

// CollectCanonicalRecordReferencesFromSearchResponse extracts all canonical
// record references present in a trawler search response.
func CollectCanonicalRecordReferencesFromSearchResponse(response *search.TrawlerSearchResponse) []*CanonicalArchiveRecordReference {
	return canonicalSearchRecordReferences(response)
}

// CollectCanonicalRecordReferencesFromConversationListResponse extracts all
// canonical record references present in a conversation list response.
func CollectCanonicalRecordReferencesFromConversationListResponse(response *conversation.ConversationListResponse) []*CanonicalArchiveRecordReference {
	return canonicalConversationRecordReferences(response)
}

// ResolveLocalShortReferenceForExternalPlugin resolves one local short
// reference in core index.db before dispatching to an external plugin.
func ResolveLocalShortReferenceForExternalPlugin(
	ctx context.Context,
	stateRoot string,
	trawlerID string,
	archivePath string,
	localShortReference *LocalTrawlerShortReference,
	replay func(context.Context) error,
) ([]*CanonicalArchiveRecordReference, error) {
	paths, err := externalPluginResolvedPaths(stateRoot, trawlerID, archivePath)
	if err != nil {
		return nil, err
	}
	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return nil, err
	}
	if coreStore != nil {
		defer func() { _ = coreStore.Close() }()
	}
	req := &TrawlerCommandExecutionRequest{
		coreShortReferenceStore:       coreStore,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		registeredTrawlerIdentityText: trawlerID,
		stateRoot:                     paths.StateRoot,
		replayShortReferences:         replay,
	}
	return req.ResolveShortReference(ctx, localShortReference)
}

// ResolveLocalShortReferencesForExternalPluginCanonicalReferences resolves
// canonical record references returned by a plugin against core index.db.
func ResolveLocalShortReferencesForExternalPluginCanonicalReferences(
	ctx context.Context,
	stateRoot string,
	trawlerID string,
	archivePath string,
	canonicalRefs []*CanonicalArchiveRecordReference,
	replay func(context.Context) error,
) ([]CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	if len(canonicalRefs) == 0 {
		return nil, nil
	}
	paths, err := externalPluginResolvedPaths(stateRoot, trawlerID, archivePath)
	if err != nil {
		return nil, err
	}
	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return nil, err
	}
	if coreStore != nil {
		defer func() { _ = coreStore.Close() }()
	}
	req := &TrawlerCommandExecutionRequest{
		coreShortReferenceStore:       coreStore,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		registeredTrawlerIdentityText: trawlerID,
		stateRoot:                     paths.StateRoot,
		replayShortReferences:         replay,
	}
	return req.LocalShortReferencesForCanonicalArchiveRecordReferences(ctx, canonicalRefs)
}

// FinalizeExternalPluginOpenedRecord normalizes an opened record returned by an
// external plugin and populates resolved trawl links from core index.db.
func FinalizeExternalPluginOpenedRecord(
	ctx context.Context,
	stateRoot string,
	trawlerID string,
	archivePath string,
	record *open.OpenRecord,
	replay func(context.Context) error,
) error {
	if record == nil {
		return errors.New("external plugin returned no opened record")
	}
	paths, err := externalPluginResolvedPaths(stateRoot, trawlerID, archivePath)
	if err != nil {
		return err
	}
	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return err
	}
	if coreStore != nil {
		defer func() { _ = coreStore.Close() }()
	}
	req := &TrawlerCommandExecutionRequest{
		coreShortReferenceStore:       coreStore,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		registeredTrawlerIdentityText: trawlerID,
		stateRoot:                     paths.StateRoot,
		replayShortReferences:         replay,
	}
	normalizeOpenedMessageConversationContext(record)
	if err := setGloballyRoutableTrawlLinkForConversationContainingOpenedMessage(ctx, req, record); err != nil {
		return err
	}
	return resolveTrawlerSpecificOpenedRecordLinks(ctx, req, record)
}

// FinalizeExternalPluginPersonMatchResponse populates person trawl links from
// core index.db for a Who response returned by an external plugin.
func FinalizeExternalPluginPersonMatchResponse(
	ctx context.Context,
	stateRoot string,
	registeredTrawler *RegisteredTrawlerIdentity,
	archivePath string,
	response *person.TrawlerPersonMatchResponse,
	replay func(context.Context) error,
) error {
	if response == nil {
		return nil
	}
	trawlerID := RegisteredTrawlerIdentityText(registeredTrawler)
	paths, err := externalPluginResolvedPaths(stateRoot, trawlerID, archivePath)
	if err != nil {
		return err
	}
	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return err
	}
	if coreStore != nil {
		defer func() { _ = coreStore.Close() }()
	}
	req := &TrawlerCommandExecutionRequest{
		coreShortReferenceStore:       coreStore,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		registeredTrawlerIdentityText: trawlerID,
		stateRoot:                     paths.StateRoot,
		replayShortReferences:         replay,
	}
	return setGloballyRoutableTrawlLinksForPersonMatchCandidates(ctx, req, registeredTrawler, response)
}

// PublishExternalPluginIndexChangeBatches commits staged ArchiveIndexChangeBatch
// frames emitted by an external plugin atomically into core index.db.
func PublishExternalPluginIndexChangeBatches(
	ctx context.Context,
	stateRoot string,
	trawlerID string,
	trawlerDisplayName string,
	archivePath string,
	batches []*indexcontract.ArchiveIndexChangeBatch,
) error {
	if len(batches) == 0 {
		return nil
	}
	paths, err := externalPluginResolvedPaths(stateRoot, trawlerID, archivePath)
	if err != nil {
		return err
	}
	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return err
	}
	if coreStore == nil {
		return errors.New("core unified index store is not open")
	}
	pubErr := publishIndexChangeBatchesToCoreStore(
		ctx,
		coreStore,
		paths.StateRoot,
		trawlerID,
		trawlerDisplayName,
		archivePath,
		batches,
	)
	closeErr := coreStore.Close()
	if pubErr != nil {
		return pubErr
	}
	return closeErr
}

// ReadExternalPluginIndexCursor reads the current committed index cursor for an
// external plugin from core index.db without writing files.
func ReadExternalPluginIndexCursor(ctx context.Context, stateRoot string, trawlerID string) string {
	return readTrawlerIndexCursorFromStateRoot(ctx, stateRoot, trawlerID)
}

func externalPluginResolvedPaths(stateRoot string, trawlerID string, archivePath string) (resolvedTrawlerArchivePaths, error) {
	decl := RegisteredTrawlerDeclaration{
		RegisteredTrawler: NewRegisteredTrawlerIdentity(trawlerID),
	}
	if strings.TrimSpace(archivePath) != "" {
		decl.DefaultTrawlerArchivePaths = TrawlerArchivePaths{TrawlerArchivePath: archivePath}
	}
	return resolveTrawlerArchivePaths(stateRoot, decl)
}

// ExternalPluginRunErrorFromDescription wraps a structured plugin error frame
// so trawl CLI and Mac app error mapping behave identically to in-process
// trawlers.
func ExternalPluginRunErrorFromDescription(description output.ErrorDescription, exitCode int) error {
	return childRunError{description: description, code: exitCode}
}

// ExternalPluginExitCodeForError maps an error returned inside a standalone
// plugin executable to the canonical OpenTrawl process exit code.
func ExternalPluginExitCodeForError(err error) int {
	return exitCodeFor(err)
}

// AcquireExternalPluginRunLock acquires the host-owned mutation lock for one
// plugin archive directory.
func AcquireExternalPluginRunLock(baseDirectory string) (io.Closer, error) {
	return acquireRunLock(baseDirectory)
}

// ConfigureExternalPluginChildProcess puts a spawned plugin process into its
// own process group so signals clean up its entire process tree.
func ConfigureExternalPluginChildProcess(cmd *exec.Cmd) {
	configureChildCommand(cmd)
}

// SignalExternalPluginChildProcess delivers a Unix signal to a plugin's process
// group.
func SignalExternalPluginChildProcess(cmd *exec.Cmd, sig syscall.Signal) error {
	return signalChildProcess(cmd, sig)
}

// KillExternalPluginChildProcess forcibly terminates a plugin's process group.
func KillExternalPluginChildProcess(cmd *exec.Cmd) error {
	return killChildProcess(cmd)
}

// ExternalPluginParentLifetimePipe wraps the parent-death pipe passed to a
// standalone plugin executable via TRAWLKIT_PARENT_FD.
type ExternalPluginParentLifetimePipe struct {
	inner *parentLifetimePipe
}

// AttachExternalPluginParentLifetimePipe creates and attaches a parent lifetime
// pipe to cmd and returns the updated environment slice.
func AttachExternalPluginParentLifetimePipe(cmd *exec.Cmd, env []string) (*ExternalPluginParentLifetimePipe, []string, error) {
	pipe, err := newParentLifetimePipe(cmd, env)
	if err != nil {
		return nil, nil, err
	}
	return &ExternalPluginParentLifetimePipe{inner: pipe}, pipe.env, nil
}

// ChildStarted closes the parent's copy of the read end after cmd.Start.
func (pipe *ExternalPluginParentLifetimePipe) ChildStarted() {
	if pipe != nil && pipe.inner != nil {
		pipe.inner.childStarted()
	}
}

// Close closes the parent's write end of the lifetime pipe.
func (pipe *ExternalPluginParentLifetimePipe) Close() error {
	if pipe == nil || pipe.inner == nil {
		return nil
	}
	return pipe.inner.Close()
}

// WatchExternalPluginParentLifetime monitors TRAWLKIT_PARENT_FD inside a
// standalone plugin executable when the environment variable is present.
func WatchExternalPluginParentLifetime() (func(), error) {
	if strings.TrimSpace(os.Getenv(childParentFDEnv)) == "" {
		return func() {}, nil
	}
	return watchParentLifetime()
}

// BuildVersion returns the current trawlkit build version string for run logs.
func BuildVersion() string {
	return buildVersion
}
