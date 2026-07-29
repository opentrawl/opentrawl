package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func (r runner) runInProcess(ctx context.Context, source Trawler, command targetTrawlerCommand, globals globalOptions, wireChild bool) (result executionResult) {
	paths, err := resolveTrawlerArchivePaths(globals.stateRoot, source.RegisteredTrawlerDeclaration())
	if err != nil {
		return executionResult{err: err}
	}
	runLog, err := r.openRunLog(paths, command, globals, wireChild)
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
	if err := loadConfig(source.RegisteredTrawlerDeclaration(), globals.stateRoot); err != nil {
		return executionResult{err: err}
	}
	if command.bespoke != nil {
		args, err := parseBespokeFlags(*command.bespoke, command.args)
		if err != nil {
			return executionResult{err: err}
		}
		command.args = args
		if err := validateBespokeArgs(*command.bespoke, command.args); err != nil {
			return executionResult{err: err}
		}
	}
	if command.shared != nil && command.typed == nil {
		command.invocationArguments = append([]string(nil), command.args...)
		args, err := parseSharedTrawlerCommandFlags(*command.shared, command.args, command.name == "search")
		if err != nil {
			return executionResult{err: err}
		}
		command.args = args
	}
	var lock *runLock
	if command.mutates {
		lock, err = acquireRunLock(paths.Base)
		if err != nil {
			return executionResult{err: err}
		}
		defer func() { _ = lock.Close() }()
	}
	var timeout time.Duration
	if !command.mutates {
		timeout = command.timeout
		if timeout == 0 {
			timeout = r.opts.readTimeout
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var exclusiveTrawlerArchiveFileSetLockDuringPreparation *store.TrawlerArchiveFileSetLock
	switch command.storeMode {
	case storeWrite:
		// Peek-and-park, if the trawler wants it, happens here -- before
		// openStore ever creates the write connection req.OpenedTrawlerArchiveStore will hand to
		// the command. Parking after req.OpenedTrawlerArchiveStore is open would mean either
		// closing a connection the harness still owns past this call (see
		// assignSourceShortRefs below, which runs against req.OpenedTrawlerArchiveStore again
		// right after the command returns) or applying schema DDL to a file
		// that's about to be parked, mutating what's meant to survive
		// untouched. See ArchivePreparer.
		if preparer, ok := source.(ArchivePreparer); ok {
			exclusiveTrawlerArchiveFileSetLockDuringPreparation, err = store.AcquireExclusiveTrawlerArchiveFileSetLock(paths.TrawlerArchivePath)
			if err != nil {
				return executionResult{err: err}
			}
			if err := preparer.PrepareArchive(ctx, paths.TrawlerArchivePath); err != nil {
				return executionResult{err: errors.Join(err, exclusiveTrawlerArchiveFileSetLockDuringPreparation.Close())}
			}
		}
	case storeOptional, storeRead:
		if preparer, ok := source.(ReadArchivePreparer); ok {
			started := time.Now()
			if err := preparer.PrepareReadArchive(ctx, paths.TrawlerArchivePath); err != nil {
				_ = runLog.Info("archive_prepare_read", fmt.Sprintf("duration_ms=%d", time.Since(started).Milliseconds()))
				return executionResult{err: err}
			}
			_ = runLog.Info("archive_prepare_read", fmt.Sprintf("duration_ms=%d", time.Since(started).Milliseconds()))
		}
	}
	st, err := openStore(ctx, paths.TrawlerArchivePaths, command.storeMode)
	if exclusiveTrawlerArchiveFileSetLockDuringPreparation != nil {
		releaseExclusiveTrawlerArchiveFileSetLockError := exclusiveTrawlerArchiveFileSetLockDuringPreparation.Close()
		if err != nil || releaseExclusiveTrawlerArchiveFileSetLockError != nil {
			var closeOpenedStoreError error
			if st != nil {
				closeOpenedStoreError = st.Close()
			}
			return executionResult{err: errors.Join(err, releaseExclusiveTrawlerArchiveFileSetLockError, closeOpenedStoreError)}
		}
	}
	if err != nil {
		return executionResult{err: err}
	}
	if st != nil {
		defer func() { _ = st.Close() }()
	}
	req := &TrawlerCommandExecutionRequest{
		OpenedTrawlerArchiveStore: st,
		TrawlerArchivePaths:       paths.TrawlerArchivePaths,
		TrawlerCommandLog:         runLog,
		ReportTrawlerCommandProgress: func(progress Progress) {
			logProgress(runLog, progress)
		},
	}
	if wireChild {
		req.ReportTrawlerCommandProgress = func(progress Progress) {
			_ = writeChildFrame(r.opts.stdout, childProgressFrame(progress))
		}
	}
	if command.name == "sync" {
		report, err := executeSync(ctx, source, req)
		return executionResult{syncReport: report, err: err}
	}
	if peopleReconcile, ok := command.typed.(*typedPeopleReconcile); ok {
		err := peopleReconcile.execute(ctx, source, req)
		return executionResult{syncReport: peopleReconcile.report, err: err}
	}
	trawlerCommandResponse, err := executeTrawlerCommand(ctx, source, command, req)
	if err != nil {
		return executionResult{err: err}
	}
	if trawlerCommandResponse != nil {
		localShortReferenceAliasesByCanonicalRecordReference, err := trawlerCommandResponseLocalShortReferenceAliasesByCanonicalRecordReference(
			ctx,
			req,
			trawlerCommandResponse,
		)
		if err != nil {
			return executionResult{err: err}
		}
		renderContext := trawlerCommandRenderContext(
			source.RegisteredTrawlerDeclaration(),
			command,
			trawlerCommandResponse,
		)
		commandExecutionResult := executionResult{
			trawlerCommandResponse:                               trawlerCommandResponse,
			localShortReferenceAliasesByCanonicalRecordReference: localShortReferenceAliasesByCanonicalRecordReference,
			trawlerCommandRenderContext:                          renderContext,
		}
		if err := ctx.Err(); err != nil {
			commandExecutionResult.err = err
		}
		return commandExecutionResult
	}
	if err := ctx.Err(); err != nil {
		return executionResult{err: err}
	}
	return executionResult{}
}

func validateBespokeArgs(command TrawlerCommand, args []string) error {
	commandName := strings.Join(strings.Fields(command.TrawlerCommandName), " ")
	commandDisplayName := render.DisplayLabel(commandName)
	positionalArgumentNames := command.TrawlerCommandPositionalArgumentNames
	if len(args) > len(positionalArgumentNames) {
		return usageError{err: errors.New("The command has too many arguments.")}
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
	missingArgumentNames := make([]string, 0, required-len(args))
	for _, positionalArgumentName := range positionalArgumentNames[len(args):required] {
		missingArgumentName := strings.ToLower(strings.ReplaceAll(strings.Trim(positionalArgumentName, "[]"), "_", " "))
		missingArgumentNames = append(missingArgumentNames, "a "+missingArgumentName)
	}
	return usageError{err: fmt.Errorf("%s needs %s.", commandDisplayName, humanList(missingArgumentNames))}
}

func executeSync(ctx context.Context, source Trawler, req *TrawlerCommandExecutionRequest) (*syncv1.TrawlerArchiveSyncReport, error) {
	report, syncErr := source.(Syncer).Sync(ctx, req)
	assignErr := assignSourceShortRefs(ctx, source, req)
	if syncErr != nil {
		if assignErr != nil {
			return nil, errors.Join(syncErr, assignErr)
		}
		return nil, syncErr
	}
	if assignErr != nil {
		return nil, assignErr
	}
	if successfullyCompletedArchiveSyncRecorder, ok := source.(SuccessfullyCompletedArchiveSyncRecorder); ok {
		if err := successfullyCompletedArchiveSyncRecorder.RecordSuccessfullyCompletedArchiveSync(ctx, req); err != nil {
			return nil, err
		}
	}
	if report == nil {
		report = &syncv1.TrawlerArchiveSyncReport{}
	}
	return report, nil
}

func (r runner) openRunLog(paths resolvedTrawlerArchivePaths, command targetTrawlerCommand, globals globalOptions, attach bool) (*cklog.Run, error) {
	opts := cklog.Options{
		StateRoot:                         paths.StateRoot,
		RegisteredTrawlerManifestIdentity: paths.RegisteredTrawlerManifestIdentity,
		RunID:                             globals.runID,
		Command:                           command.name,
		Version:                           buildVersion,
		Stderr:                            r.opts.stderr,
		Verbosity:                         globals.verbosity,
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
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return openedStore, err
	case storeRead:
		openedStore, err := store.OpenReadOnlyWithSharedTrawlerArchiveFileSetLock(ctx, paths.TrawlerArchivePath)
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
	source Trawler,
	command targetTrawlerCommand,
	req *TrawlerCommandExecutionRequest,
) (*commandv1.TrawlerCommandResponse, error) {
	if command.typed != nil {
		return nil, command.typed.execute(ctx, source, req)
	}
	if command.shared != nil && command.name == "messages" {
		query, err := parseTrawlerMessageListQuery(command.args)
		if err != nil {
			return nil, err
		}
		response, err := executeTrawlerMessageList(
			ctx,
			source.(TrawlerMessageLister),
			req,
			query,
		)
		if err != nil {
			return nil, err
		}
		return &commandv1.TrawlerCommandResponse{
			TypedTrawlerCommandResponse: &commandv1.TrawlerCommandResponse_MessageListResponse{
				MessageListResponse: response,
			},
		}, nil
	}
	if command.bespoke == nil || command.bespoke.ExecuteTrawlerCommand == nil {
		return nil, usageError{err: fmt.Errorf("unknown command %q", command.name)}
	}
	req.TrawlerCommandPositionalArguments = command.args
	if command.mutates {
		response, err := command.bespoke.ExecuteTrawlerCommand(ctx, req)
		if err != nil {
			return nil, err
		}
		return response, assignSourceShortRefs(ctx, source, req)
	}
	return command.bespoke.ExecuteTrawlerCommand(ctx, req)
}

func assignSourceShortRefs(ctx context.Context, source Trawler, req *TrawlerCommandExecutionRequest) error {
	provider, ok := source.(ShortReferenceAssignmentProvider)
	if !ok || req.OpenedTrawlerArchiveStore == nil {
		return nil
	}
	records, err := provider.RecordReferencesForShortReferenceAssignment(ctx, req)
	if err != nil {
		return err
	}
	if _, err := req.AssignShortReferences(ctx, records); err != nil {
		return err
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("short_refs_assigned", fmt.Sprintf("refs=%d", len(records)))
	}
	return nil
}
