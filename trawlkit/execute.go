package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	commandcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

func (r runner) runInProcess(ctx context.Context, trawler Trawler, command targetTrawlerCommand, globals globalOptions, wireChild bool) (result executionResult) {
	command, paths, err := prepareTargetTrawlerCommand(trawler, command, globals)
	if err != nil {
		return executionResult{err: err}
	}
	runLog, err := r.openRunLogIfWritable(paths, command, globals, wireChild)
	if err != nil {
		return executionResult{err: err}
	}
	if runLog != nil && !wireChild {
		defer func() {
			if err := finishRunLog(runLog, result.err); result.err == nil && err != nil {
				result.err = err
			}
		}()
	}
	if err := trawler.LoadTrawlerConfiguration(paths.TrawlerConfigurationPath); err != nil {
		return executionResult{err: err}
	}
	if command.mutates {
		lock, err := acquireRunLock(paths.Base)
		if err != nil {
			return executionResult{err: err}
		}
		defer func() { _ = lock.Close() }()
	}
	ctx, cancel := r.withReadCommandTimeout(ctx, command)
	defer cancel()

	st, err := openStore(ctx, paths.TrawlerArchivePaths, command.storeMode)
	if err != nil {
		return executionResult{err: err}
	}
	if st != nil {
		defer func() { _ = st.Close() }()
	}
	coreShortRefOpenMode := selectCoreShortReferenceOpenMode(command, st, wireChild)
	coreShortRefStore, err := openCoreShortReferenceStore(ctx, paths, coreShortRefOpenMode)
	if err != nil {
		return executionResult{err: err}
	}
	if coreShortRefStore != nil {
		defer func() { _ = coreShortRefStore.Close() }()
	}
	req := r.buildExecutionRequest(trawler, command, paths, runLog, st, coreShortRefStore, coreShortRefOpenMode, wireChild)
	return r.executePreparedCommand(ctx, trawler, command, paths, req, wireChild)
}

func prepareTargetTrawlerCommand(
	trawler Trawler,
	command targetTrawlerCommand,
	globals globalOptions,
) (targetTrawlerCommand, resolvedTrawlerArchivePaths, error) {
	parsedCommand, err := parseTargetTrawlerCommandFlags(command)
	if err != nil {
		return targetTrawlerCommand{}, resolvedTrawlerArchivePaths{}, err
	}
	paths, err := resolveTrawlerArchivePaths(globals.stateRoot, trawler.RegisteredTrawlerDeclaration())
	if err != nil {
		return targetTrawlerCommand{}, resolvedTrawlerArchivePaths{}, err
	}
	if parsedCommand.sharedOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED &&
		parsedCommand.sharedOperationExecution == nil {
		parsedCommand.invocationArguments = append([]string(nil), parsedCommand.args...)
		args, err := parseSharedTrawlerCommandFlags(
			parsedCommand.sharedOperation,
			parsedCommand.shared,
			parsedCommand.args,
			parsedCommand.sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		)
		if err != nil {
			return targetTrawlerCommand{}, resolvedTrawlerArchivePaths{}, err
		}
		parsedCommand.args = args
	}
	return parsedCommand, paths, nil
}

func (r runner) openRunLogIfWritable(
	paths resolvedTrawlerArchivePaths,
	command targetTrawlerCommand,
	globals globalOptions,
	wireChild bool,
) (*cklog.Run, error) {
	if command.writesNoFiles {
		return nil, nil
	}
	return r.openRunLog(paths, command, globals, wireChild)
}

func (r runner) withReadCommandTimeout(ctx context.Context, command targetTrawlerCommand) (context.Context, context.CancelFunc) {
	if command.mutates {
		return ctx, func() {}
	}
	timeout := command.timeout
	if timeout == 0 {
		timeout = r.opts.readTimeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func selectCoreShortReferenceOpenMode(command targetTrawlerCommand, st *store.Store, wireChild bool) coreShortReferenceStoreOpenMode {
	switch {
	case command.writesNoFiles:
		return coreShortReferenceStoreReadOnlyWithoutWritingFilesIfPresent
	case wireChild && command.mutates:
		return coreShortReferenceStoreReadOnlyIfPresent
	case command.mutates || st != nil:
		return coreShortReferenceStoreReadWrite
	default:
		return coreShortReferenceStoreReadOnlyIfPresent
	}
}

func (r runner) buildExecutionRequest(
	trawler Trawler,
	command targetTrawlerCommand,
	paths resolvedTrawlerArchivePaths,
	runLog *cklog.Run,
	st *store.Store,
	coreShortRefStore *store.Store,
	coreShortRefOpenMode coreShortReferenceStoreOpenMode,
	wireChild bool,
) *TrawlerCommandExecutionRequest {
	decl := trawler.RegisteredTrawlerDeclaration()
	trawlerID := RegisteredTrawlerIdentityText(decl.RegisteredTrawler)
	displayName := strings.TrimSpace(decl.RegisteredTrawlerDisplayName)
	if displayName == "" {
		displayName = trawlerID
	}
	req := &TrawlerCommandExecutionRequest{
		OpenedTrawlerArchiveStore:     st,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		TrawlerCommandLog:             runLog,
		coreShortReferenceStore:       coreShortRefStore,
		registeredTrawlerIdentityText: trawlerID,
		registeredTrawlerDisplayName:  displayName,
		stateRoot:                     paths.StateRoot,
		ReportTrawlerCommandProgress: func(progress Progress) {
			logProgress(runLog, progress)
		},
	}
	if wireChild {
		req.ReportTrawlerCommandProgress = func(progress Progress) {
			_ = writeChildFrame(r.opts.stdout, childProgressFrame(progress))
		}
	}
	attachShortReferenceReplay(trawler, command, paths, st, coreShortRefStore, coreShortRefOpenMode, req)
	return req
}

func attachShortReferenceReplay(
	trawler Trawler,
	command targetTrawlerCommand,
	paths resolvedTrawlerArchivePaths,
	st *store.Store,
	coreShortRefStore *store.Store,
	coreShortRefOpenMode coreShortReferenceStoreOpenMode,
	req *TrawlerCommandExecutionRequest,
) {
	exporter, ok := trawler.(ArchiveIndexChangeExporter)
	if !ok || coreShortRefStore == nil || coreShortRefOpenMode != coreShortReferenceStoreReadWrite || st == nil || command.writesNoFiles {
		return
	}
	replayed := false
	req.replayShortReferences = func(replayCtx context.Context) error {
		if replayed {
			return nil
		}
		replayed = true
		var batches []*indexcontract.ArchiveIndexChangeBatch
		if replayErr := exporter.ExportArchiveIndexChanges(replayCtx, req, "", func(batch *indexcontract.ArchiveIndexChangeBatch) error {
			if batch != nil {
				batches = append(batches, batch)
			}
			return nil
		}); replayErr != nil {
			return replayErr
		}
		if len(batches) == 0 {
			return nil
		}
		return publishIndexChangeBatchesToCoreStore(
			replayCtx,
			coreShortRefStore,
			paths.StateRoot,
			req.registeredTrawlerIdentityText,
			req.registeredTrawlerDisplayName,
			paths.TrawlerArchivePath,
			batches,
		)
	}
}

func (r runner) executePreparedCommand(
	ctx context.Context,
	trawler Trawler,
	command targetTrawlerCommand,
	paths resolvedTrawlerArchivePaths,
	req *TrawlerCommandExecutionRequest,
	wireChild bool,
) executionResult {
	if command.sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE {
		report, err := executeUpdate(ctx, trawler, req)
		if err == nil {
			err = r.publishOrStreamIndexChangesAfterMutation(ctx, trawler, paths, req, wireChild)
		}
		return executionResult{updateReport: report, err: err}
	}
	if peopleReconcile, ok := command.sharedOperationExecution.(*executePeopleReconciliationOperation); ok {
		err := peopleReconcile.execute(ctx, trawler, req)
		if err == nil {
			err = r.publishOrStreamIndexChangesAfterMutation(ctx, trawler, paths, req, wireChild)
		}
		return executionResult{updateReport: peopleReconcile.report, err: err}
	}
	trawlerCommandResponse, err := executeTrawlerCommand(ctx, trawler, command, req)
	if err != nil {
		return executionResult{err: err}
	}
	if command.mutates {
		if err := r.publishOrStreamIndexChangesAfterMutation(ctx, trawler, paths, req, wireChild); err != nil {
			return executionResult{err: err}
		}
	}
	if trawlerCommandResponse == nil {
		return executionResult{err: ctx.Err()}
	}
	return finalizeCommandExecutionResult(ctx, trawler, command, paths, req, trawlerCommandResponse, wireChild)
}

func finalizeCommandExecutionResult(
	ctx context.Context,
	trawler Trawler,
	command targetTrawlerCommand,
	paths resolvedTrawlerArchivePaths,
	req *TrawlerCommandExecutionRequest,
	trawlerCommandResponse *commandcontract.TrawlerCommandResponse,
	wireChild bool,
) executionResult {
	if wireChild && trawlerCommandResponse.GetPhotosStepRunResponse() != nil {
		boundedResponse, err := render.PreparePhotosStepRunResponseForTransport(trawlerCommandResponse.GetPhotosStepRunResponse(), paths.TrawlerArchivePath)
		if err != nil {
			return executionResult{err: err}
		}
		trawlerCommandResponse.TypedTrawlerCommandResponse = &commandcontract.TrawlerCommandResponse_PhotosStepRunResponse{PhotosStepRunResponse: boundedResponse}
	}
	var localShortReferencesByCanonicalRecordReference []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
	if !wireChild {
		var err error
		localShortReferencesByCanonicalRecordReference, err = trawlerCommandResponseLocalShortReferencesByCanonicalRecordReference(
			ctx,
			req,
			trawlerCommandResponse,
		)
		if err != nil {
			return executionResult{err: err}
		}
	}
	renderContext := trawlerCommandRenderContext(
		trawler.RegisteredTrawlerDeclaration(),
		command,
		trawlerCommandResponse,
	)
	return executionResult{
		trawlerCommandResponse:                         trawlerCommandResponse,
		localShortReferencesByCanonicalRecordReference: localShortReferencesByCanonicalRecordReference,
		trawlerCommandRenderContext:                    renderContext,
		err:                                            ctx.Err(),
	}
}

func (r runner) publishOrStreamIndexChangesAfterMutation(
	ctx context.Context,
	trawler Trawler,
	paths resolvedTrawlerArchivePaths,
	req *TrawlerCommandExecutionRequest,
	wireChild bool,
) error {
	exporter, ok := trawler.(ArchiveIndexChangeExporter)
	if !ok {
		return nil
	}
	sinceCursor := ""
	if wireChild {
		sinceCursor = strings.TrimSpace(os.Getenv(childIndexSinceCursorEnv))
	} else if req.coreShortReferenceStore != nil {
		cursor, hasIndexed, err := unifiedindex.ReadTrawlerIndexCursor(ctx, req.coreShortReferenceStore.DB(), req.registeredTrawlerIdentityText)
		if err != nil {
			return err
		}
		if hasIndexed {
			sinceCursor = cursor
		}
	}
	if wireChild {
		return exporter.ExportArchiveIndexChanges(ctx, req, sinceCursor, func(batch *indexcontract.ArchiveIndexChangeBatch) error {
			if batch == nil {
				return nil
			}
			return writeChildFrame(r.opts.stdout, childArchiveIndexChangeBatchFrame(batch))
		})
	}
	if req.coreShortReferenceStore == nil {
		return nil
	}
	var batches []*indexcontract.ArchiveIndexChangeBatch
	if err := exporter.ExportArchiveIndexChanges(ctx, req, sinceCursor, func(batch *indexcontract.ArchiveIndexChangeBatch) error {
		if batch != nil {
			batches = append(batches, batch)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(batches) == 0 {
		return nil
	}
	return publishIndexChangeBatchesToCoreStore(
		ctx,
		req.coreShortReferenceStore,
		paths.StateRoot,
		req.registeredTrawlerIdentityText,
		req.registeredTrawlerDisplayName,
		paths.TrawlerArchivePath,
		batches,
	)
}

func validateBespokeArgs(command TrawlerCommand, args []string) error {
	positionalArgumentNames := command.TrawlerCommandPositionalArgumentNames
	if len(args) > len(positionalArgumentNames) {
		return usageError{err: render.CommandHasTooManyArguments()}
	}
	required := 0
	for _, name := range positionalArgumentNames {
		if !strings.HasPrefix(strings.TrimSpace(name), "[") {
			required++
		}
	}
	if len(args) >= required {
		return nil
	}
	return usageError{err: render.CommandNeedsArguments(
		strings.Join(strings.Fields(command.TrawlerCommandName), " "),
		positionalArgumentNames[len(args):required],
	)}
}

func executeUpdate(ctx context.Context, trawler Trawler, req *TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	report, err := trawler.(Updater).Update(ctx, req)
	if err != nil {
		return nil, err
	}
	if successfullyCompletedArchiveUpdateRecorder, ok := trawler.(SuccessfullyCompletedArchiveUpdateRecorder); ok {
		if err := successfullyCompletedArchiveUpdateRecorder.RecordSuccessfullyCompletedArchiveUpdate(ctx, req); err != nil {
			return nil, err
		}
	}
	if report == nil {
		report = &update.TrawlerArchiveUpdateReport{}
	}
	return report, nil
}

func (r runner) openRunLog(paths resolvedTrawlerArchivePaths, command targetTrawlerCommand, globals globalOptions, attach bool) (*cklog.Run, error) {
	logOwner, err := cklog.NewRegisteredTrawlerLogOwner(paths.RegisteredTrawler)
	if err != nil {
		return nil, err
	}
	opts := cklog.Options{
		StateRoot: paths.StateRoot,
		LogOwner:  logOwner,
		RunID:     globals.runID,
		Command:   command.commandName(),
		Version:   buildVersion,
		Stderr:    r.opts.stderr,
		Verbosity: globals.verbosity,
	}
	if attach {
		opts.Stderr = &childLogFrameWriter{w: r.opts.stdout}
		if opts.Verbosity < 1 {
			opts.Verbosity = 1
		}
		return cklog.AttachRun(opts)
	}
	return cklog.NewRun(opts)
}

func finishRunLog(runLog *cklog.Run, err error) error {
	if runLog == nil {
		return nil
	}
	if exitCodeFor(err) == 2 {
		return runLog.FinishRejected()
	}
	return runLog.Finish(err)
}

func logProgress(runLog *cklog.Run, progress Progress) {
	if runLog == nil {
		return
	}
	parts := []string{"done=" + strconv.FormatInt(progress.Done, 10)}
	if progress.Total > 0 {
		parts = append(parts, "total="+strconv.FormatInt(progress.Total, 10))
	}
	if message := strings.Join(strings.Fields(progress.Message), " "); message != "" {
		parts = append(parts, "message="+strconv.Quote(message))
	}
	_ = runLog.Info(progressLogEvent(progress.Phase), strings.Join(parts, " "))
}

func progressLogEvent(phase string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(phase)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case b.Len() > 0:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	event := strings.Trim(b.String(), "_")
	if event == "" || event[0] < 'a' || event[0] > 'z' {
		event = "progress"
	}
	if !strings.HasSuffix(event, "_progress") {
		event += "_progress"
	}
	return event
}

func openStore(ctx context.Context, paths TrawlerArchivePaths, mode storeMode) (*store.Store, error) {
	switch mode {
	case storeNone:
		return nil, nil
	case storeOptional:
		openedStore, err := store.OpenReadOnlyWithSharedTrawlerArchiveFileSetLock(ctx, paths.TrawlerArchivePath)
		if errors.Is(err, store.ErrTrawlerArchiveFileSetIsBeingRecreated) {
			return nil, newTrawlerArchiveTemporarilyUnavailableError(paths.TrawlerArchivePath)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return openedStore, err
	case storeRead:
		openedStore, err := store.OpenReadOnlyWithSharedTrawlerArchiveFileSetLock(ctx, paths.TrawlerArchivePath)
		if errors.Is(err, store.ErrTrawlerArchiveFileSetIsBeingRecreated) {
			return nil, newTrawlerArchiveTemporarilyUnavailableError(paths.TrawlerArchivePath)
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, NewMissingArchiveError(paths.TrawlerArchivePath)
		}
		return openedStore, err
	case storeReadWithoutWritingFiles:
		openedStore, err := store.OpenReadOnlyWithoutWritingFiles(ctx, paths.TrawlerArchivePath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, NewMissingArchiveError(paths.TrawlerArchivePath)
		}
		return openedStore, err
	case storeWrite:
		return store.Open(ctx, store.Options{Path: paths.TrawlerArchivePath})
	default:
		return nil, fmt.Errorf("unknown store mode %d", mode)
	}
}

func executeTrawlerCommand(
	ctx context.Context,
	trawler Trawler,
	targetCommand targetTrawlerCommand,
	req *TrawlerCommandExecutionRequest,
) (*commandcontract.TrawlerCommandResponse, error) {
	if targetCommand.sharedOperationExecution != nil {
		return nil, targetCommand.sharedOperationExecution.execute(ctx, trawler, req)
	}
	if targetCommand.sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES {
		query, err := parseTrawlerMessageListQuery(targetCommand.args)
		if err != nil {
			return nil, err
		}
		response, err := executeTrawlerMessageList(
			ctx,
			trawler.(TrawlerMessageLister),
			req,
			query,
		)
		if err != nil {
			return nil, err
		}
		return &commandcontract.TrawlerCommandResponse{
			TypedTrawlerCommandResponse: &commandcontract.TrawlerCommandResponse_MessageListResponse{
				MessageListResponse: response,
			},
		}, nil
	}
	if targetCommand.bespoke == nil || targetCommand.bespoke.ExecuteTrawlerCommand == nil {
		return nil, usageError{err: render.UnknownCommand(targetCommand.commandName(), nil)}
	}
	req.TrawlerCommandPositionalArguments = targetCommand.args
	if targetCommand.mutates {
		return targetCommand.bespoke.ExecuteTrawlerCommand(ctx, req)
	}
	return targetCommand.bespoke.ExecuteTrawlerCommand(ctx, req)
}
