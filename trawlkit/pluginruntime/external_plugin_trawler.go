package pluginruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
	"github.com/opentrawl/opentrawl/trawlkit/pluginsandbox"
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
	"github.com/opentrawl/opentrawl/trawlkit/secrets"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ExternalPluginTrawlerConfig defines the immutable configuration for one
// host-side standalone plugin trawler adapter.
type ExternalPluginTrawlerConfig struct {
	PluginIdentifier    string
	ExecutablePath      string
	ExecutableSHA256    string
	IsBundledFirstParty bool
	Manifest            *plugincontract.PluginManifestResponse
	TrustRecord         *plugincontract.PluginTrustRecord
	SecretStore         secrets.PluginSecretStore
}

// ExternalPluginTrawler adapts one standalone plugin executable to the core
// trawlkit.Trawler and trawlkit.ExternalPluginExecutor interfaces.
type ExternalPluginTrawler struct {
	pluginIdentifier    string
	executablePath      string
	executableSHA256    string
	isBundledFirstParty bool
	manifest            *plugincontract.PluginManifestResponse
	trustRecord         *plugincontract.PluginTrustRecord
	secretStore         secrets.PluginSecretStore
}

type externalPluginIndexExportingTrawler struct {
	*ExternalPluginTrawler
}

var (
	_ trawlkit.Trawler                          = (*ExternalPluginTrawler)(nil)
	_ trawlkit.ExternalPluginManifestProvider   = (*ExternalPluginTrawler)(nil)
	_ trawlkit.ExternalPluginCapabilityProvider = (*ExternalPluginTrawler)(nil)
	_ trawlkit.ExternalPluginExecutor           = (*ExternalPluginTrawler)(nil)
	_ trawlkit.ArchiveIndexChangeExporter       = (*externalPluginIndexExportingTrawler)(nil)
)

// NewExternalPluginTrawler creates the host-side trawler adapter for a
// standalone plugin. If the plugin declared ArchiveIndexChangeExport support,
// the returned trawler also satisfies trawlkit.ArchiveIndexChangeExporter so
// unified search and `trawl index rebuild` include it automatically.
func NewExternalPluginTrawler(cfg ExternalPluginTrawlerConfig) trawlkit.Trawler {
	base := &ExternalPluginTrawler{
		pluginIdentifier:    cfg.PluginIdentifier,
		executablePath:      cfg.ExecutablePath,
		executableSHA256:    cfg.ExecutableSHA256,
		isBundledFirstParty: cfg.IsBundledFirstParty,
		manifest:            proto.Clone(cfg.Manifest).(*plugincontract.PluginManifestResponse),
		trustRecord:         cloneTrustRecord(cfg.TrustRecord),
		secretStore:         cfg.SecretStore,
	}
	if base.manifest.GetDeclaredCapabilities().GetSupportsArchiveIndexChangeExport() {
		return &externalPluginIndexExportingTrawler{ExternalPluginTrawler: base}
	}
	return base
}

func cloneTrustRecord(record *plugincontract.PluginTrustRecord) *plugincontract.PluginTrustRecord {
	if record == nil {
		return nil
	}
	return proto.Clone(record).(*plugincontract.PluginTrustRecord)
}

func (p *externalPluginIndexExportingTrawler) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	_ = req
	outcome, err := p.invokeReadOnlyOperation(ctx, trawlkit.TrawlerExecutorOptions{}, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_ExportArchiveIndexChanges{
			ExportArchiveIndexChanges: &plugincontract.PluginExportArchiveIndexChangesOperationRequest{
				SinceOpaqueIndexCursor: sinceOpaqueCursor,
			},
		},
	})
	if err != nil {
		return err
	}
	for _, batch := range outcome.StagedIndexChangeBatches {
		if err := emit(batch); err != nil {
			return err
		}
	}
	return nil
}

// ExternalPluginManifest returns the plugin's pre-probed RegisteredTrawlerManifest.
func (p *ExternalPluginTrawler) ExternalPluginManifest() *federation.RegisteredTrawlerManifest {
	return p.manifest.GetRegisteredTrawlerManifest()
}

// ExternalPluginDeclaredCapabilities returns the plugin's probed capability
// declarations.
func (p *ExternalPluginTrawler) ExternalPluginDeclaredCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return p.manifest.GetDeclaredCapabilities()
}

// RegisteredTrawlerDeclaration projects the plugin's probed manifest into the
// core declaration struct.
func (p *ExternalPluginTrawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	m := p.manifest.GetRegisteredTrawlerManifest()
	privacy := m.GetRegisteredTrawlerPrivacyBoundary()
	return trawlkit.RegisteredTrawlerDeclaration{
		SearchMatchesResolvedPersonDisplayNames: m.GetSearchMatchesResolvedPersonDisplayNames(),
		RegisteredTrawler:                       m.GetRegisteredTrawler(),
		RegisteredTrawlerCommandName:            m.GetRegisteredTrawlerCommandName(),
		RegisteredTrawlerAliases:                append([]string(nil), m.GetRegisteredTrawlerAliases()...),
		RegisteredTrawlerDisplayName:            m.GetRegisteredTrawlerDisplayName(),
		RegisteredTrawlerBranding:               m.GetTrawlerBranding(),
		SourceFreshnessDependency:               m.GetSourceFreshnessDependency(),
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           privacy.GetArchiveContentReadByTrawler(),
			LeavesMachine:   privacy.GetArchiveContentThatLeavesMachine(),
			NetworkRequests: privacy.GetNetworkRequestsMadeByTrawler(),
		},
	}
}

func (p *ExternalPluginTrawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (p *ExternalPluginTrawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return nil
}

func (p *ExternalPluginTrawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	_ = req
	return p.ExecuteExternalPluginStatus(ctx, trawlkit.TrawlerExecutorOptions{})
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginStatus(ctx context.Context, opts trawlkit.TrawlerExecutorOptions) (*status.TrawlerStatusResponse, error) {
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_Status{
			Status: &plugincontract.PluginStatusOperationRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	response := outcome.Result.GetStatus()
	if response == nil {
		return nil, errors.New("external plugin returned no status response")
	}
	return response, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginSearch(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	query trawlkit.Query,
) (*search.TrawlerSearchResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	searchReq := &plugincontract.PluginSearchOperationRequest{
		QueryText:        query.Text,
		ResultLimit:      uint32(max(query.Limit, 0)),
		PersonFilterText: query.PersonFilter.UnresolvedPersonFilterText(),
		SearchTotalIsLowerBoundWhenResultLimitIsReached: query.SearchTotalIsLowerBoundWhenResultLimitIsReached,
	}
	if !query.After.IsZero() {
		searchReq.AfterTime = timestamppb.New(query.After)
		searchReq.AfterTimeIsSet = true
	}
	if !query.Before.IsZero() {
		searchReq.BeforeTime = timestamppb.New(query.Before)
		searchReq.BeforeTimeIsSet = true
	}
	if resolved := query.PersonFilter.ResolvedPersonFilter(); resolved != nil {
		searchReq.ExactPersonFilterIdentifiers = resolved.ExactPersonFilterIdentifiers
	}
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_Search{Search: searchReq},
	})
	if err != nil {
		return nil, nil, err
	}
	response := outcome.Result.GetSearch()
	if response == nil {
		return nil, nil, errors.New("external plugin returned no search response")
	}
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, nil, err
	}
	refs, err := trawlkit.ResolveLocalShortReferencesForExternalPluginCanonicalReferences(
		ctx,
		opts.StateRoot,
		p.pluginIdentifier,
		paths.TrawlerArchivePath,
		trawlkit.CollectCanonicalRecordReferencesFromSearchResponse(response),
		p.replayIndexFunc(opts.StateRoot),
	)
	if err != nil {
		return nil, nil, err
	}
	return response, refs, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginOpenRecord(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	recordAnchor *trawlkit.RecordAnchorIdentifier,
) (*open.OpenRecord, error) {
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	canonicalRefs, err := trawlkit.ResolveLocalShortReferenceForExternalPlugin(
		ctx,
		opts.StateRoot,
		p.pluginIdentifier,
		paths.TrawlerArchivePath,
		localShortReference,
		p.replayIndexFunc(opts.StateRoot),
	)
	if err != nil {
		return nil, err
	}
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_OpenRecord{
			OpenRecord: &plugincontract.PluginOpenRecordOperationRequest{
				CanonicalRecordReference: canonicalRefs[0],
				RequestedRecordAnchor:    recordAnchor,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	record := outcome.Result.GetOpenRecord()
	if record == nil {
		return nil, errors.New("external plugin returned no opened record")
	}
	if err := trawlkit.FinalizeExternalPluginOpenedRecord(
		ctx,
		opts.StateRoot,
		p.pluginIdentifier,
		paths.TrawlerArchivePath,
		record,
		p.replayIndexFunc(opts.StateRoot),
	); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginUpdate(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	args []string,
) (*update.TrawlerArchiveUpdateReport, error) {
	paths, baseDir, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	runLock, err := trawlkit.AcquireExternalPluginRunLock(baseDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = runLock.Close() }()

	runLog, err := p.openHostRunLog(opts, "update")
	if err != nil {
		return nil, err
	}

	sinceCursor := trawlkit.ReadExternalPluginIndexCursor(ctx, opts.StateRoot, p.pluginIdentifier)
	preResolvedRefs, err := p.preResolveCommandArgumentTrawlLinks(ctx, opts.StateRoot, paths.TrawlerArchivePath, args)
	if err != nil {
		_ = runLog.Finish(err)
		return nil, err
	}
	extraReadFiles, extraReadDirs := collectExplicitCommandInputPaths(args)
	outcome, invokeErr := p.invokeMutatingOperationWithExtraReadPaths(ctx, opts, paths, runLog, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_Update{
			Update: &plugincontract.PluginUpdateOperationRequest{
				UpdateArguments:        append([]string(nil), args...),
				SinceOpaqueIndexCursor: sinceCursor,
			},
		},
		PreResolvedLocalShortReferences: preResolvedRefs,
	}, extraReadFiles, extraReadDirs)
	if invokeErr != nil {
		_ = runLog.Finish(invokeErr)
		return nil, invokeErr
	}
	if len(outcome.StagedIndexChangeBatches) > 0 {
		if pubErr := trawlkit.PublishExternalPluginIndexChangeBatches(
			ctx,
			opts.StateRoot,
			p.pluginIdentifier,
			p.displayName(),
			paths.TrawlerArchivePath,
			outcome.StagedIndexChangeBatches,
		); pubErr != nil {
			_ = runLog.Finish(pubErr)
			return nil, pubErr
		}
	}
	report := outcome.Result.GetUpdate()
	if report == nil {
		report = &update.TrawlerArchiveUpdateReport{}
	}
	if err := runLog.Finish(nil); err != nil {
		return nil, err
	}
	return report, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginDeclaredCommand(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	args []string,
) (
	*command.TrawlerCommandResponse,
	[]trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
	render.TrawlerCommandRenderContext,
	error,
) {
	paths, baseDir, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, err
	}
	preResolvedRefs, err := p.preResolveCommandArgumentTrawlLinks(ctx, opts.StateRoot, paths.TrawlerArchivePath, args)
	if err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, err
	}
	isMutating := p.bespokeCommandMutates(args)
	req := &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_BespokeCommand{
			BespokeCommand: &plugincontract.PluginBespokeCommandOperationRequest{
				CommandArguments: append([]string(nil), args...),
			},
		},
		PreResolvedLocalShortReferences: preResolvedRefs,
	}
	var outcome pluginInvocationOutcome
	if isMutating {
		outcome, err = p.executeMutatingBespokeCommand(ctx, opts, paths, baseDir, args, req)
	} else {
		outcome, err = p.invokeReadOnlyOperation(ctx, opts, req)
	}
	if err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, err
	}
	response := outcome.Result.GetTrawlerCommandResponse()
	if response == nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, errors.New("external plugin returned no command response")
	}
	refs, err := trawlkit.ResolveLocalShortReferencesForExternalPluginCanonicalReferences(
		ctx,
		opts.StateRoot,
		p.pluginIdentifier,
		paths.TrawlerArchivePath,
		trawlkit.CollectCanonicalRecordReferencesFromCommandResponse(response),
		p.replayIndexFunc(opts.StateRoot),
	)
	if err != nil {
		return nil, nil, render.TrawlerCommandRenderContext{}, err
	}
	renderContext := trawlkit.ExternalPluginRenderContextForCommandResponse(
		p.manifest.GetRegisteredTrawlerManifest(),
		args,
		response,
	)
	return response, refs, renderContext, nil
}

func (p *ExternalPluginTrawler) executeMutatingBespokeCommand(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	paths trawlkit.TrawlerArchivePaths,
	baseDir string,
	args []string,
	req *plugincontract.PluginInvocationRequest,
) (pluginInvocationOutcome, error) {
	runLock, err := trawlkit.AcquireExternalPluginRunLock(baseDir)
	if err != nil {
		return pluginInvocationOutcome{}, err
	}
	defer func() { _ = runLock.Close() }()

	commandName := "command"
	if len(args) > 0 {
		commandName = args[0]
	}
	runLog, err := p.openHostRunLog(opts, commandName)
	if err != nil {
		return pluginInvocationOutcome{}, err
	}
	req.GetBespokeCommand().SinceOpaqueIndexCursor = trawlkit.ReadExternalPluginIndexCursor(ctx, opts.StateRoot, p.pluginIdentifier)
	extraReadFiles, extraReadDirs := collectExplicitCommandInputPaths(args)
	outcome, invokeErr := p.invokeMutatingOperationWithExtraReadPaths(ctx, opts, paths, runLog, req, extraReadFiles, extraReadDirs)
	if invokeErr != nil {
		_ = runLog.Finish(invokeErr)
		return pluginInvocationOutcome{}, invokeErr
	}
	if len(outcome.StagedIndexChangeBatches) > 0 {
		if pubErr := trawlkit.PublishExternalPluginIndexChangeBatches(
			ctx,
			opts.StateRoot,
			p.pluginIdentifier,
			p.displayName(),
			paths.TrawlerArchivePath,
			outcome.StagedIndexChangeBatches,
		); pubErr != nil {
			_ = runLog.Finish(pubErr)
			return pluginInvocationOutcome{}, pubErr
		}
	}
	if finishErr := runLog.Finish(nil); finishErr != nil {
		return pluginInvocationOutcome{}, finishErr
	}
	return outcome, nil
}

func (p *ExternalPluginTrawler) bespokeCommandMutates(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, declaredName := range p.manifest.GetDeclaredCapabilities().GetMutatingBespokeCommandNames() {
		tokens := strings.Fields(declaredName)
		if len(tokens) == 0 || len(args) < len(tokens) {
			continue
		}
		matched := true
		for i, token := range tokens {
			if args[i] != token {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func collectExplicitCommandInputPaths(args []string) (files []string, dirs []string) {
	seen := make(map[string]struct{})
	addExistingPath := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "-") {
			return
		}
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return
		}
		stat, err := os.Stat(abs)
		if err != nil {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		if stat.IsDir() {
			dirs = append(dirs, abs)
		} else {
			files = append(files, abs)
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if _, afterEq, hasEq := strings.Cut(arg, "="); hasEq && strings.HasPrefix(arg, "-") {
			addExistingPath(afterEq)
			continue
		}
		if arg == "--file" || arg == "-file" || arg == "--path" || arg == "-path" {
			if i+1 < len(args) {
				addExistingPath(args[i+1])
				i++
			}
			continue
		}
		if i > 0 && !strings.HasPrefix(arg, "-") {
			addExistingPath(arg)
		}
	}
	return files, dirs
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginSyncArchiveIndex(
	ctx context.Context,
	stateRoot string,
	forceFullRebuild bool,
) (bool, error) {
	if !p.manifest.GetDeclaredCapabilities().GetSupportsArchiveIndexChangeExport() {
		return false, nil
	}
	paths, _, err := p.archivePaths(stateRoot)
	if err != nil {
		return false, err
	}
	if _, statErr := os.Stat(paths.TrawlerArchivePath); errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	} else if statErr != nil {
		return false, fmt.Errorf("stat %s archive: %w", p.pluginIdentifier, statErr)
	}
	sinceCursor := ""
	if !forceFullRebuild {
		sinceCursor = trawlkit.ReadExternalPluginIndexCursor(ctx, stateRoot, p.pluginIdentifier)
	}
	outcome, err := p.invokeReadOnlyOperation(ctx, trawlkit.TrawlerExecutorOptions{
		StateRoot: stateRoot,
		Timeout:   pluginDefaultUpdateWatchdog,
	}, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_ExportArchiveIndexChanges{
			ExportArchiveIndexChanges: &plugincontract.PluginExportArchiveIndexChangesOperationRequest{
				SinceOpaqueIndexCursor: sinceCursor,
			},
		},
	})
	if err != nil {
		return false, err
	}
	if len(outcome.StagedIndexChangeBatches) == 0 {
		return true, nil
	}
	if err := trawlkit.PublishExternalPluginIndexChangeBatches(
		ctx,
		stateRoot,
		p.pluginIdentifier,
		p.displayName(),
		paths.TrawlerArchivePath,
		outcome.StagedIndexChangeBatches,
	); err != nil {
		return false, err
	}
	return true, nil
}

func (p *ExternalPluginTrawler) ExternalPluginDeclaredCommandWritesNoFiles([]string) (bool, error) {
	return false, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginWho(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	query string,
) (*person.TrawlerPersonMatchResponse, error) {
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_Who{
			Who: &plugincontract.PluginWhoOperationRequest{QueryText: query},
		},
	})
	if err != nil {
		return nil, err
	}
	response := outcome.Result.GetWho()
	if response == nil {
		return nil, errors.New("external plugin returned no person match response")
	}
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	if err := trawlkit.FinalizeExternalPluginPersonMatchResponse(
		ctx,
		opts.StateRoot,
		p.manifest.GetRegisteredTrawlerManifest().GetRegisteredTrawler(),
		paths.TrawlerArchivePath,
		response,
		p.replayIndexFunc(opts.StateRoot),
	); err != nil {
		return nil, err
	}
	return response, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginCountPersonMessages(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	personFilter *person.ExactPersonMessageCountRequest,
) (*person.ExactPersonMessageCountResponse, error) {
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_CountPersonMessages{
			CountPersonMessages: personFilter,
		},
	})
	if err != nil {
		return nil, err
	}
	response := outcome.Result.GetCountPersonMessages()
	if response == nil {
		return nil, errors.New("external plugin returned no person message count response")
	}
	return response, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginConversations(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	query trawlkit.ConversationQuery,
) (*conversation.ConversationListResponse, []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference, error) {
	convReq := &plugincontract.PluginConversationsOperationRequest{
		ResultLimit:                      uint32(max(query.Limit, 0)),
		IncludeAllConversations:          query.All,
		UnreadOnly:                       query.Unread,
		ResolvedPersonConversationFilter: query.PersonConversationFilterResolvedAcrossTrawlerArchives,
	}
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_Conversations{Conversations: convReq},
	})
	if err != nil {
		return nil, nil, err
	}
	response := outcome.Result.GetConversations()
	if response == nil {
		return nil, nil, errors.New("external plugin returned no conversation list response")
	}
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, nil, err
	}
	refs, err := trawlkit.ResolveLocalShortReferencesForExternalPluginCanonicalReferences(
		ctx,
		opts.StateRoot,
		p.pluginIdentifier,
		paths.TrawlerArchivePath,
		trawlkit.CollectCanonicalRecordReferencesFromConversationListResponse(response),
		p.replayIndexFunc(opts.StateRoot),
	)
	if err != nil {
		return nil, nil, err
	}
	return response, refs, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginMessages(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	query trawlkit.TrawlerMessageListQuery,
) (*message.MessageListResponse, error) {
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	localConvRef := strings.TrimSpace(query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation)
	var preResolved []*plugincontract.ResolvedLocalShortReferenceMapping
	if localConvRef != "" && trawlkit.ValidShortRef(localConvRef) {
		canonicalRefs, resolveErr := trawlkit.ResolveLocalShortReferenceForExternalPlugin(
			ctx,
			opts.StateRoot,
			p.pluginIdentifier,
			paths.TrawlerArchivePath,
			trawlkit.NewLocalTrawlerShortReference(localConvRef),
			p.replayIndexFunc(opts.StateRoot),
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if len(canonicalRefs) == 1 {
			preResolved = append(preResolved, &plugincontract.ResolvedLocalShortReferenceMapping{
				LocalShortReference:      trawlkit.NewLocalTrawlerShortReference(localConvRef),
				CanonicalRecordReference: canonicalRefs[0],
			})
		}
	}
	msgReq := &plugincontract.PluginMessagesOperationRequest{
		LocalConversationShortReference: trawlkit.NewLocalTrawlerShortReference(localConvRef),
		ResultLimit:                     uint32(max(query.MaximumReturnedMessageCount, 0)),
	}
	if !query.After.IsZero() {
		msgReq.AfterTime = timestamppb.New(query.After)
		msgReq.AfterTimeIsSet = true
	}
	if !query.Before.IsZero() {
		msgReq.BeforeTime = timestamppb.New(query.Before)
		msgReq.BeforeTimeIsSet = true
	}
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation:                       &plugincontract.PluginInvocationRequest_Messages{Messages: msgReq},
		PreResolvedLocalShortReferences: preResolved,
	})
	if err != nil {
		return nil, err
	}
	response := outcome.Result.GetMessages()
	if response == nil {
		return nil, errors.New("external plugin returned no message list response")
	}
	return response, nil
}

func (p *ExternalPluginTrawler) ExecuteExternalPluginPeopleSnapshot(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
) (*person.TrawlerPeopleSnapshot, error) {
	outcome, err := p.invokeReadOnlyOperation(ctx, opts, &plugincontract.PluginInvocationRequest{
		Operation: &plugincontract.PluginInvocationRequest_PeopleSnapshot{
			PeopleSnapshot: &plugincontract.PluginPeopleSnapshotOperationRequest{},
		},
	})
	if err != nil {
		return nil, err
	}
	snapshot := outcome.Result.GetPeopleSnapshot()
	if snapshot == nil {
		return nil, errors.New("external plugin returned no people snapshot")
	}
	if err := trawlkit.ValidateTrawlerPeopleSnapshot(snapshot); err != nil {
		return nil, fmt.Errorf("invalid external plugin people snapshot: %w", err)
	}
	return snapshot, nil
}

func (p *ExternalPluginTrawler) invokeReadOnlyOperation(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	req *plugincontract.PluginInvocationRequest,
) (pluginInvocationOutcome, error) {
	paths, _, err := p.archivePaths(opts.StateRoot)
	if err != nil {
		return pluginInvocationOutcome{}, err
	}
	if _, statErr := os.Stat(paths.TrawlerArchivePath); statErr == nil {
		sharedLock, lockErr := store.AcquireSharedTrawlerArchiveFileSetLock(paths.TrawlerArchivePath)
		if lockErr != nil {
			return pluginInvocationOutcome{}, lockErr
		}
		defer func() { _ = sharedLock.Close() }()
	}

	req.ArchiveDirectoryPath = paths.TrawlerArchiveDirectoryPath
	req.ArchiveDatabasePath = paths.TrawlerArchivePath
	req.ConfigurationFilePath = string(paths.TrawlerConfigurationPath)
	req.VerbosityLevel = int32(opts.Verbosity)
	req.IsInternalAppRequest = trawlkit.IsInternalAppRequest(ctx)

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = pluginDefaultReadTimeout
	}
	readOnlyDirs := []string{}
	if _, statErr := os.Stat(paths.TrawlerArchiveDirectoryPath); statErr == nil {
		readOnlyDirs = append(readOnlyDirs, paths.TrawlerArchiveDirectoryPath)
	}
	return invokeSandboxedPlugin(ctx, pluginInvocationExecutionOptions{
		ExecutablePath: p.executablePath,
		SandboxPolicy: pluginsandbox.PluginSandboxPolicy{
			ReadOnlyDirectoryPaths: readOnlyDirs,
		},
		Request:      req,
		HardDeadline: timeout,
		IdleWatchdog: timeout,
	})
}

func (p *ExternalPluginTrawler) invokeMutatingOperationWithExtraReadPaths(
	ctx context.Context,
	opts trawlkit.TrawlerExecutorOptions,
	paths trawlkit.TrawlerArchivePaths,
	runLog *cklog.Run,
	req *plugincontract.PluginInvocationRequest,
	extraReadFiles []string,
	extraReadDirs []string,
) (pluginInvocationOutcome, error) {
	if err := os.MkdirAll(paths.TrawlerArchiveDirectoryPath, 0o700); err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("create plugin archive directory: %w", err)
	}
	allowedPublicHosts, selfHostedHosts, err := p.effectiveNetworkPolicy()
	if err != nil {
		return pluginInvocationOutcome{}, err
	}
	readOnlyFiles, readOnlyDirs := p.effectiveMutatingReadPaths(string(paths.TrawlerConfigurationPath))
	readOnlyFiles = append(readOnlyFiles, extraReadFiles...)
	readOnlyDirs = append(readOnlyDirs, extraReadDirs...)
	secretBroker, err := p.effectiveSecretBroker()
	if err != nil {
		return pluginInvocationOutcome{}, err
	}

	req.ArchiveDirectoryPath = paths.TrawlerArchiveDirectoryPath
	req.ArchiveDatabasePath = paths.TrawlerArchivePath
	req.ConfigurationFilePath = string(paths.TrawlerConfigurationPath)
	req.ApprovedReadPaths = append(append([]string(nil), readOnlyDirs...), readOnlyFiles...)
	req.VerbosityLevel = int32(opts.Verbosity)
	req.IsInternalAppRequest = trawlkit.IsInternalAppRequest(ctx)

	var deniedWriteFiles []string
	if !p.isBundledFirstParty || len(p.manifest.GetDeclaredCapabilities().GetDeclaredConfigReadPathFields()) > 0 {
		deniedWriteFiles = []string{string(paths.TrawlerConfigurationPath)}
	}

	return invokeSandboxedPlugin(ctx, pluginInvocationExecutionOptions{
		ExecutablePath: p.executablePath,
		SandboxPolicy: pluginsandbox.PluginSandboxPolicy{
			WritableDirectoryPaths: []string{paths.TrawlerArchiveDirectoryPath},
			ReadOnlyDirectoryPaths: readOnlyDirs,
			ReadOnlyFilePaths:      readOnlyFiles,
			DeniedWriteFilePaths:   deniedWriteFiles,
		},
		Request:                req,
		IdleWatchdog:           pluginDefaultUpdateWatchdog,
		RunLog:                 runLog,
		SecretBroker:           secretBroker,
		ApprovedAuthOrigins:    p.effectiveAuthOrigins(),
		AllowedPublicHosts:     allowedPublicHosts,
		SelfHostedNetworkHosts: selfHostedHosts,
	})
}

func (p *ExternalPluginTrawler) effectiveNetworkPolicy() ([]string, []pluginproxy.SelfHostedNetworkHostExemption, error) {
	var publicHosts []string
	var selfHosted []pluginproxy.SelfHostedNetworkHostExemption
	if p.isBundledFirstParty {
		publicHosts = append(publicHosts, p.manifest.GetDeclaredCapabilities().GetDeclaredNetworkHosts()...)
	}
	if p.trustRecord != nil {
		publicHosts = append(publicHosts, p.trustRecord.GetApprovedNetworkHosts()...)
		exemptions, err := parseTrustSelfHostedApprovals(p.trustRecord.GetApprovedSelfHostedNetworkHosts())
		if err != nil {
			return nil, nil, err
		}
		selfHosted = append(selfHosted, exemptions...)
	}
	return publicHosts, selfHosted, nil
}

func (p *ExternalPluginTrawler) effectiveMutatingReadPaths(configPath string) (files []string, dirs []string) {
	if p.isBundledFirstParty {
		declared := p.manifest.GetDeclaredCapabilities()
		declaredFiles, declaredDirs := resolveExistingBundledReadPaths(declared.GetDeclaredReadPaths())
		files = append(files, declaredFiles...)
		dirs = append(dirs, declaredDirs...)
		configFiles, configDirs := extractBundledConfigReadPaths(configPath, declared.GetDeclaredConfigReadPathFields())
		files = append(files, configFiles...)
		dirs = append(dirs, configDirs...)
	}
	if p.trustRecord != nil {
		for _, rawPath := range p.trustRecord.GetApprovedReadPaths() {
			trimmed := strings.TrimSpace(rawPath)
			if trimmed == "" {
				continue
			}
			if stat, err := os.Stat(trimmed); err == nil && stat.IsDir() {
				dirs = append(dirs, trimmed)
			} else {
				files = append(files, trimmed)
			}
		}
	}
	return files, dirs
}

func (p *ExternalPluginTrawler) effectiveSecretBroker() (*secrets.PluginSecretBroker, error) {
	var readKeys, writeKeys []string
	if p.isBundledFirstParty {
		readKeys = append(readKeys, p.manifest.GetDeclaredCapabilities().GetDeclaredReadSecretKeys()...)
		writeKeys = append(writeKeys, p.manifest.GetDeclaredCapabilities().GetDeclaredWriteSecretKeys()...)
	}
	if p.trustRecord != nil {
		readKeys = append(readKeys, p.trustRecord.GetApprovedReadSecretKeys()...)
		writeKeys = append(writeKeys, p.trustRecord.GetApprovedWriteSecretKeys()...)
	}
	if len(readKeys) == 0 && len(writeKeys) == 0 {
		return nil, nil
	}
	return secrets.NewPluginSecretBroker(p.secretStore, p.pluginIdentifier, readKeys, writeKeys)
}

func (p *ExternalPluginTrawler) effectiveAuthOrigins() []string {
	var origins []string
	if p.isBundledFirstParty {
		origins = append(origins, p.manifest.GetDeclaredCapabilities().GetDeclaredAuthOrigins()...)
	}
	if p.trustRecord != nil {
		origins = append(origins, p.trustRecord.GetApprovedAuthOrigins()...)
	}
	return origins
}

func (p *ExternalPluginTrawler) archivePaths(stateRoot string) (trawlkit.TrawlerArchivePaths, string, error) {
	archiveDirOverride := ""
	if p.trustRecord != nil {
		archiveDirOverride = strings.TrimSpace(p.trustRecord.GetApprovedArchiveDirectoryOverride())
	}
	paths, baseDir, err := trawlkit.ResolveExternalPluginArchivePathLayout(stateRoot, p.pluginIdentifier, archiveDirOverride)
	if err != nil {
		return trawlkit.TrawlerArchivePaths{}, "", err
	}
	if archiveDirOverride != "" {
		paths.TrawlerArchivePath = filepath.Join(paths.TrawlerArchiveDirectoryPath, p.pluginIdentifier+".db")
	}
	return paths, baseDir, nil
}

func (p *ExternalPluginTrawler) displayName() string {
	if name := strings.TrimSpace(p.manifest.GetRegisteredTrawlerManifest().GetRegisteredTrawlerDisplayName()); name != "" {
		return name
	}
	return p.pluginIdentifier
}

func (p *ExternalPluginTrawler) replayIndexFunc(stateRoot string) func(context.Context) error {
	if !p.manifest.GetDeclaredCapabilities().GetSupportsArchiveIndexChangeExport() {
		return nil
	}
	return func(replayCtx context.Context) error {
		_, err := p.ExecuteExternalPluginSyncArchiveIndex(replayCtx, stateRoot, true)
		return err
	}
}

func (p *ExternalPluginTrawler) openHostRunLog(opts trawlkit.TrawlerExecutorOptions, commandName string) (*cklog.Run, error) {
	resolvedStateRoot, err := trawlkit.ResolveStateRoot(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	logOwner, err := cklog.NewRegisteredTrawlerLogOwner(p.manifest.GetRegisteredTrawlerManifest().GetRegisteredTrawler())
	if err != nil {
		return nil, err
	}
	return cklog.NewRun(cklog.Options{
		StateRoot: resolvedStateRoot,
		LogOwner:  logOwner,
		Command:   commandName,
		Version:   trawlkit.BuildVersion(),
		Stderr:    opts.Stderr,
		Verbosity: opts.Verbosity,
	})
}

func (p *ExternalPluginTrawler) preResolveCommandArgumentTrawlLinks(
	ctx context.Context,
	stateRoot string,
	archivePath string,
	args []string,
) ([]*plugincontract.ResolvedLocalShortReferenceMapping, error) {
	prefix := p.pluginIdentifier + ":"
	var mappings []*plugincontract.ResolvedLocalShortReferenceMapping
	seen := make(map[string]struct{})
	for i := 0; i < len(args); i++ {
		arg := args[i]
		candidate := arg
		isConversationFlagValue := false
		if flagName, afterEq, hasEq := strings.Cut(arg, "="); hasEq && strings.HasPrefix(arg, "-") {
			candidate = afterEq
			isConversationFlagValue = flagName == "--conversation" || flagName == "-conversation"
		} else if i > 0 && (args[i-1] == "--conversation" || args[i-1] == "-conversation") {
			isConversationFlagValue = true
		} else if i == 1 && args[0] == "versions" {
			isConversationFlagValue = true
		}
		localAlias, ok, err := trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(candidate, p.pluginIdentifier)
		if err != nil {
			continue
		}
		if !ok && !strings.HasPrefix(candidate, prefix) && !isConversationFlagValue {
			continue
		}
		if !trawlkit.ValidShortRef(localAlias) {
			continue
		}
		if _, dup := seen[localAlias]; dup {
			continue
		}
		seen[localAlias] = struct{}{}
		canonicalRefs, resolveErr := trawlkit.ResolveLocalShortReferenceForExternalPlugin(
			ctx,
			stateRoot,
			p.pluginIdentifier,
			archivePath,
			trawlkit.NewLocalTrawlerShortReference(localAlias),
			p.replayIndexFunc(stateRoot),
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if len(canonicalRefs) == 1 {
			mappings = append(mappings, &plugincontract.ResolvedLocalShortReferenceMapping{
				LocalShortReference:      trawlkit.NewLocalTrawlerShortReference(localAlias),
				CanonicalRecordReference: canonicalRefs[0],
			})
		}
	}
	return mappings, nil
}
