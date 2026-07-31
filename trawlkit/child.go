package trawlkit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	synccontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync"
	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

type childFrame struct {
	kind                   childFrameKind
	progress               Progress
	logText                string
	trawlerCommandResponse *command.TrawlerCommandResponse
	syncReport             *synccontract.TrawlerArchiveSyncReport
	errorDescription       *output.ErrorDescription
}

type childFrameKind int

const (
	childFrameResult childFrameKind = iota
	childFrameProgress
	childFrameLog
)

const (
	childStateRootEnv = "TRAWLKIT_STATE_ROOT"
	childRunIDEnv     = "TRAWLKIT_RUN_ID"
	childParentFDEnv  = "TRAWLKIT_PARENT_FD"
)

type childLogFrameWriter struct {
	mu      sync.Mutex
	w       io.Writer
	pending string
}

func (w *childLogFrameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.pending + string(p)
	w.pending = ""
	for {
		line, rest, ok := strings.Cut(text, "\n")
		if !ok {
			w.pending = text
			return len(p), nil
		}
		text = rest
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := writeChildFrame(w.w, childLogFrame(line)); err != nil {
			return len(p), err
		}
	}
}

type childRunError struct {
	description output.ErrorDescription
	code        int
}

func (e childRunError) Error() string {
	return e.description.Message
}

func (e childRunError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e childRunError) ErrorDescription() output.ErrorDescription {
	return e.description
}

type childWireEnvError struct {
	name    string
	invalid bool
}

func (e childWireEnvError) Error() string {
	if e.invalid {
		return fmt.Sprintf("%s is invalid", e.name)
	}
	return fmt.Sprintf("%s is required", e.name)
}

func (e childWireEnvError) ExitCode() int {
	return 2
}

func (e childWireEnvError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "usage",
		Message: e.Error(),
	}
}

func (r runner) runWireChild(ctx context.Context, argv []string, sources []Trawler) int {
	stopParentWatch, err := watchParentLifetime()
	if err != nil {
		description := TrawlerOperationErrorDescription(err)
		frame := childResultFrame(nil, nil, &description)
		_ = writeChildFrame(r.opts.stdout, frame)
		return exitCodeFor(err)
	}
	defer stopParentWatch()
	globals, err := parseGlobal(argv)
	var result executionResult
	if err == nil {
		globals, err = childWireGlobals(globals)
	}
	if err == nil {
		source, rest, selectErr := selectTrawler(globals.args, sources)
		if selectErr != nil {
			err = selectErr
		} else if len(rest) == 1 && rest[0] == internalPeopleReconcileTrawlerCommand {
			command, requestErr := r.peopleReconcileTrawlerCommandFromInput()
			if requestErr != nil {
				err = requestErr
			} else {
				result = r.runInProcess(ctx, source, command, globals, true)
				err = result.err
			}
		} else {
			result = r.dispatch(ctx, source, rest, globals, true)
			err = result.err
		}
	}
	var description *output.ErrorDescription
	if err != nil {
		errorDescription := TrawlerOperationErrorDescription(err)
		description = &errorDescription
	}
	frame := childResultFrame(result.trawlerCommandResponse, result.syncReport, description)
	if writeErr := writeChildFrame(r.opts.stdout, frame); writeErr != nil && err == nil {
		return 1
	}
	return exitCodeFor(err)
}

func (r runner) runChild(ctx context.Context, source Trawler, command targetTrawlerCommand, globals globalOptions) executionResult {
	paths, err := resolveTrawlerArchivePaths(globals.stateRoot, source.RegisteredTrawlerDeclaration())
	if err != nil {
		return executionResult{err: err}
	}
	runLog, err := r.openRunLog(paths, command, globals, false)
	if err != nil {
		return executionResult{err: err}
	}
	if command.sharedOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_METADATA {
		if err := source.LoadTrawlerConfiguration(paths.TrawlerConfigurationPath); err != nil {
			_ = finishRunLog(runLog, err)
			return executionResult{err: err}
		}
	}

	executable := r.opts.executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			err = fmt.Errorf("resolve executable: %w", err)
			_ = finishRunLog(runLog, err)
			return executionResult{err: err}
		}
	}
	args := append([]string{}, r.opts.childPrefixArgs...)
	args = append(args, HiddenWireSubcommand)
	switch globals.verbosity {
	case 1:
		args = append(args, "-v")
	case 2:
		args = append(args, "-vv")
	}
	args = append(args, RegisteredTrawlerIdentityText(source.RegisteredTrawlerDeclaration().RegisteredTrawler))
	args = append(args, command.childArgs()...)
	args = append(args, command.args...)
	cmd := exec.Command(executable, args...) // #nosec G204 -- self-reexec path and test helper are controlled by the runner.
	configureChildCommand(cmd)
	env := r.opts.childEnv
	if len(env) == 0 {
		env = os.Environ()
	} else {
		env = append([]string(nil), env...)
	}
	env = setEnvValue(env, childStateRootEnv, paths.StateRoot)
	env = setEnvValue(env, childRunIDEnv, runLog.RunID())
	lifetime, err := newParentLifetimePipe(cmd, env)
	if err != nil {
		_ = finishRunLog(runLog, err)
		return executionResult{err: err}
	}
	defer func() { _ = lifetime.Close() }()
	cmd.Env = lifetime.env
	if r.opts.childRequest != nil {
		var input bytes.Buffer
		if err := prototransport.WriteDelimited(&input, r.opts.childRequest); err != nil {
			_ = finishRunLog(runLog, err)
			return executionResult{err: fmt.Errorf("encode child request: %w", err)}
		}
		cmd.Stdin = &input
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return executionResult{err: fmt.Errorf("open child stdout: %w", err)}
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		err = fmt.Errorf("start child: %w", err)
		_ = finishRunLog(runLog, err)
		return executionResult{err: err}
	}
	lifetime.childStarted()
	watchdog := r.opts.watchdog
	if command.timeout > 0 {
		watchdog = command.timeout
	}
	result := waitForChild(ctx, cmd, stdout, stderr.String, watchdog, r.opts.killGrace, runLog, globals.verbosity, r.opts.stderr, r.opts.newWatchdogTimer)
	if result.err == nil {
		switch {
		case command.sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC ||
			command.name == internalPeopleReconcileTrawlerCommand:
			if result.syncReport == nil || result.trawlerCommandResponse != nil {
				result = executionResult{err: errors.New("sync child returned the wrong terminal result")}
			}
		case result.trawlerCommandResponse == nil || result.syncReport != nil:
			result = executionResult{err: errors.New("child returned a sync result for a non-sync command")}
		}
	}
	if result.err == nil && result.trawlerCommandResponse != nil {
		var localShortReferencesByCanonicalRecordReference []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
		if len(trawlerCommandResponseCanonicalRecordReferences(result.trawlerCommandResponse)) > 0 {
			archiveStore, openErr := openStore(ctx, paths.TrawlerArchivePaths, storeRead)
			if openErr != nil {
				result = executionResult{err: openErr}
			} else {
				request := &TrawlerCommandExecutionRequest{
					OpenedTrawlerArchiveStore: archiveStore,
					TrawlerArchivePaths:       paths.TrawlerArchivePaths,
				}
				localShortReferencesByCanonicalRecordReference, result.err =
					trawlerCommandResponseLocalShortReferencesByCanonicalRecordReference(
						ctx,
						request,
						result.trawlerCommandResponse,
					)
				if closeErr := archiveStore.Close(); result.err == nil && closeErr != nil {
					result.err = closeErr
				}
			}
		}
		result.localShortReferencesByCanonicalRecordReference = localShortReferencesByCanonicalRecordReference
		result.trawlerCommandRenderContext = trawlerCommandRenderContext(
			source.RegisteredTrawlerDeclaration(),
			command,
			result.trawlerCommandResponse,
		)
	}
	if err := finishRunLog(runLog, result.err); result.err == nil && err != nil {
		result.err = err
	}
	return result
}

func (r runner) peopleReconcileTrawlerCommandFromInput() (targetTrawlerCommand, error) {
	var request worker.Request
	if err := prototransport.ReadDelimited(bufio.NewReader(r.opts.stdin), &request); err != nil {
		return targetTrawlerCommand{}, fmt.Errorf("read People reconciliation request: %w", err)
	}
	reconcile := request.GetReconcilePeople()
	if reconcile == nil {
		return targetTrawlerCommand{}, errors.New("child request is not a People reconciliation")
	}
	peopleSnapshotTrawlerIdentity := RegisteredTrawlerIdentityText(reconcile.GetPeopleSnapshotTrawler())
	if peopleSnapshotTrawlerIdentity == "" {
		return targetTrawlerCommand{}, errors.New("people snapshot trawler identity is required")
	}
	snapshot := reconcile.GetTrawlerPeopleSnapshot()
	if validationError := ValidateTrawlerPeopleSnapshot(snapshot); validationError != nil {
		return targetTrawlerCommand{}, fmt.Errorf("invalid people snapshot: %w", validationError)
	}
	return targetTrawlerCommand{
		name:      internalPeopleReconcileTrawlerCommand,
		mutates:   true,
		storeMode: storeWrite,
		sharedOperationExecution: &executePeopleReconciliationOperation{
			peopleSnapshotTrawler: reconcile.GetPeopleSnapshotTrawler(),
			snapshot:              snapshot,
		},
	}, nil
}

func childWireGlobals(globals globalOptions) (globalOptions, error) {
	stateRoot := strings.TrimSpace(os.Getenv(childStateRootEnv))
	if stateRoot == "" {
		return globals, childWireEnvError{name: childStateRootEnv}
	}
	runID := strings.TrimSpace(os.Getenv(childRunIDEnv))
	if runID == "" {
		return globals, childWireEnvError{name: childRunIDEnv}
	}
	globals.stateRoot = stateRoot
	globals.runID = runID
	return globals, nil
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return append(out, prefix+value)
}

func waitForChild(ctx context.Context, cmd *exec.Cmd, stdout io.Reader, stderr func() string, watchdog, grace time.Duration, runLog *cklog.Run, verbosity int, logStream io.Writer, newTimer func(time.Duration) watchdogTimer) executionResult {
	if newTimer == nil {
		newTimer = newRealWatchdogTimer
	}
	frames := make(chan childFrame)
	decodeErrs := make(chan error, 1)
	done := make(chan error, 1)
	// StdoutPipe requires every read to finish before Wait closes the pipe. Keep
	// both operations in one goroutine so a fast child cannot lose its terminal
	// frame to Wait racing the decoder.
	go func() {
		decodeChildFrames(stdout, frames, decodeErrs)
		done <- cmd.Wait()
	}()

	timer := newTimer(watchdog)
	defer timer.stop()
	var terminal *childFrame
	for {
		select {
		case <-ctx.Done():
			terminateChildAndDrain(cmd, done, frames, decodeErrs, grace)
			return terminalFailure(terminal, ctx.Err())
		case <-timer.tick():
			terminateChildAndDrain(cmd, done, frames, decodeErrs, grace)
			return terminalFailure(terminal, fmt.Errorf("mutating command made no progress for %s", watchdog))
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			if terminal != nil {
				terminateChildAndDrain(cmd, done, frames, decodeErrs, grace)
				return terminalFailure(terminal, errors.New("child sent a frame after its terminal result"))
			}
			timer.reset(watchdog)
			switch frame.kind {
			case childFrameProgress:
				logProgress(runLog, frame.progress)
			case childFrameLog:
				if verbosity > 0 && logStream != nil {
					_, _ = fmt.Fprintln(logStream, frame.logText)
				}
			case childFrameResult:
				terminal = &frame
			}
		case err := <-decodeErrs:
			waitErr := waitForChildExit(ctx, cmd, done, watchdog, grace, newTimer)
			if !errors.Is(err, io.EOF) {
				if waitErr != nil {
					return executionResult{err: childExitError(waitErr, stderr())}
				}
				return executionResult{err: fmt.Errorf("read child wire: %w", err)}
			}
			if terminal == nil {
				if waitErr != nil {
					return executionResult{err: childExitError(waitErr, stderr())}
				}
				return executionResult{err: errors.New("child exited without a result frame")}
			}
			return terminalResult(*terminal, waitErr, stderr())
		}
	}
}

func terminalFailure(frame *childFrame, err error) executionResult {
	if frame == nil {
		return executionResult{err: err}
	}
	return executionResult{err: err}
}

func terminalResult(frame childFrame, waitErr error, stderr string) executionResult {
	if frame.errorDescription != nil {
		if frame.trawlerCommandResponse != nil || frame.syncReport != nil {
			return executionResult{err: errors.New("child result combined an error with a success result")}
		}
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			return executionResult{err: childExitError(waitErr, stderr)}
		}
		return executionResult{err: childRunError{description: *frame.errorDescription, code: childProcessExitCode(waitErr)}}
	}
	if waitErr != nil {
		return executionResult{err: childExitError(waitErr, stderr)}
	}
	return executionResult{
		syncReport:             frame.syncReport,
		trawlerCommandResponse: frame.trawlerCommandResponse,
	}
}

func childProgressFrame(progress Progress) childFrame {
	return childFrame{kind: childFrameProgress, progress: progress}
}

func childLogFrame(text string) childFrame {
	return childFrame{kind: childFrameLog, logText: text}
}

func childResultFrame(response *command.TrawlerCommandResponse, report *synccontract.TrawlerArchiveSyncReport, errorDescription *output.ErrorDescription) childFrame {
	return childFrame{
		kind:                   childFrameResult,
		trawlerCommandResponse: response,
		syncReport:             report,
		errorDescription:       errorDescription,
	}
}

func childProcessExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func waitForChildExit(ctx context.Context, cmd *exec.Cmd, done <-chan error, watchdog, grace time.Duration, newTimer func(time.Duration) watchdogTimer) error {
	if newTimer == nil {
		newTimer = newRealWatchdogTimer
	}
	timer := newTimer(watchdog)
	defer timer.stop()
	select {
	case <-ctx.Done():
		terminateChild(cmd, done, grace)
		return ctx.Err()
	case <-timer.tick():
		terminateChild(cmd, done, grace)
		return fmt.Errorf("mutating command made no progress for %s", watchdog)
	case err := <-done:
		return err
	}
}

// watchdogTimer is the seam the child watchdog uses to measure the no-progress
// window. Production runs on newRealWatchdogTimer, which is a real time.Timer.
// Tests inject a fake so the watchdog never fires on wall-clock scheduling.
type watchdogTimer interface {
	// tick fires when the window elapses with no reset.
	tick() <-chan time.Time
	// reset restarts the window; called on every child frame.
	reset(d time.Duration)
	// stop releases the timer.
	stop()
}

type realWatchdogTimer struct {
	timer *time.Timer
}

func newRealWatchdogTimer(d time.Duration) watchdogTimer {
	return &realWatchdogTimer{timer: time.NewTimer(d)}
}

func (t *realWatchdogTimer) tick() <-chan time.Time { return t.timer.C }

func (t *realWatchdogTimer) reset(d time.Duration) {
	if !t.timer.Stop() {
		select {
		case <-t.timer.C:
		default:
		}
	}
	t.timer.Reset(d)
}

func (t *realWatchdogTimer) stop() { t.timer.Stop() }

func terminateChild(cmd *exec.Cmd, done <-chan error, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = signalChildProcess(cmd, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(grace):
		_ = killChildProcess(cmd)
		<-done
	}
}

func terminateChildAndDrain(cmd *exec.Cmd, done <-chan error, frames <-chan childFrame, decodeErrs <-chan error, grace time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = signalChildProcess(cmd, syscall.SIGTERM)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	timerC := timer.C
	for {
		select {
		case <-done:
			return
		case _, ok := <-frames:
			if !ok {
				frames = nil
			}
		case <-decodeErrs:
			decodeErrs = nil
		case <-timerC:
			_ = killChildProcess(cmd)
			timerC = nil
		}
	}
}

func childExitError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("child failed: %w: %s", err, stderr)
	}
	return fmt.Errorf("child failed: %w", err)
}
