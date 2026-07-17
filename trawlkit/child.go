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
	workerv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker/v1"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
)

type childFrame struct {
	kind       childFrameKind
	progress   Progress
	logText    string
	output     string
	syncReport *SyncReport
	errorBody  *output.ErrorBody
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

const childWireEnvRemedy = "invoke the parent crawler command; the runner supplies state, run identity, and parent supervision for hidden wire child runs"

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
	body output.ErrorBody
	code int
}

func (e childRunError) Error() string {
	return e.body.Message
}

func (e childRunError) ExitCode() int {
	if e.code == 0 {
		return 1
	}
	return e.code
}

func (e childRunError) ErrorBody() output.ErrorBody {
	return e.body
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

func (e childWireEnvError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "usage",
		Message: e.Error(),
		Remedy:  childWireEnvRemedy,
	}
}

func (r runner) runWireChild(ctx context.Context, argv []string, sources []Crawler) int {
	stopParentWatch, err := watchParentLifetime()
	if err != nil {
		body := errorBodyFor(err)
		frame := childResultFrame("", nil, &body)
		_ = writeChildFrame(r.opts.stdout, frame)
		return exitCodeFor(err)
	}
	defer stopParentWatch()
	globals, err := parseGlobal(argv)
	format := output.Text
	if globals.json {
		format = output.JSON
	}
	var result executionResult
	if err == nil {
		globals, err = childWireGlobals(globals)
	}
	if err == nil {
		source, rest, selectErr := selectSource(globals.args, sources)
		if selectErr != nil {
			err = selectErr
		} else if len(rest) == 1 && rest[0] == internalPeopleReconcileVerb {
			verb, requestErr := r.peopleReconcileVerbFromInput()
			if requestErr != nil {
				err = requestErr
			} else {
				result = r.runInProcess(ctx, source, verb, globals, format, true)
				err = result.err
			}
		} else {
			result = r.dispatch(ctx, source, rest, globals, format, true)
			err = result.err
		}
	}
	var body *output.ErrorBody
	if err != nil {
		errBody := errorBodyFor(err)
		body = &errBody
	}
	frame := childResultFrame(string(result.output), result.syncReport, body)
	if writeErr := writeChildFrame(r.opts.stdout, frame); writeErr != nil && err == nil {
		return 1
	}
	return exitCodeFor(err)
}

func (r runner) runChild(ctx context.Context, source Crawler, verb targetVerb, globals globalOptions, format output.Format) executionResult {
	paths, err := resolveSourcePaths(globals.stateRoot, source.Info())
	if err != nil {
		return executionResult{err: err}
	}
	runLog, err := r.openRunLog(paths, verb, globals, format, false)
	if err != nil {
		return executionResult{err: err}
	}
	if verb.name != "metadata" {
		if err := loadConfig(source.Info(), globals.stateRoot); err != nil {
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
	if format == output.JSON {
		args = append(args, "--json")
	}
	args = append(args, source.Info().ID)
	args = append(args, verb.childArgs()...)
	args = append(args, verb.args...)
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
	if verb.timeout > 0 {
		watchdog = verb.timeout
	}
	result := waitForChild(ctx, cmd, stdout, stderr.String, watchdog, r.opts.killGrace, runLog, globals.verbosity, r.opts.stderr, r.opts.newWatchdogTimer)
	if result.err == nil {
		if verb.name == "sync" {
			if result.syncReport == nil || len(result.output) != 0 {
				result = executionResult{err: errors.New("sync child returned the wrong terminal result")}
			}
		} else if result.syncReport != nil {
			result = executionResult{err: errors.New("child returned a sync result for a non-sync verb")}
		}
	}
	if err := finishRunLog(runLog, result.err); result.err == nil && err != nil {
		result.err = err
	}
	return result
}

func (r runner) peopleReconcileVerbFromInput() (targetVerb, error) {
	var request workerv1.Request
	if err := prototransport.ReadDelimited(bufio.NewReader(r.opts.stdin), &request); err != nil {
		return targetVerb{}, fmt.Errorf("read People reconciliation request: %w", err)
	}
	reconcile := request.GetReconcilePeople()
	if reconcile == nil {
		return targetVerb{}, errors.New("child request is not a People reconciliation")
	}
	source := strings.TrimSpace(reconcile.GetSource())
	if source == "" {
		return targetVerb{}, errors.New("people source is required")
	}
	snapshot, err := peopleSnapshotFromProto(reconcile.GetSnapshot())
	if err != nil {
		return targetVerb{}, err
	}
	return targetVerb{
		name:      internalPeopleReconcileVerb,
		mutates:   true,
		storeMode: storeWrite,
		typed:     &typedPeopleReconcile{source: source, snapshot: snapshot},
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
			return terminalFailure(terminal, fmt.Errorf("mutating verb made no progress for %s", watchdog))
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
	return executionResult{output: []byte(frame.output), err: err}
}

func terminalResult(frame childFrame, waitErr error, stderr string) executionResult {
	if frame.errorBody != nil {
		if frame.output != "" || frame.syncReport != nil {
			return executionResult{err: errors.New("child result combined an error with a success result")}
		}
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			return executionResult{output: []byte(frame.output), err: childExitError(waitErr, stderr)}
		}
		return executionResult{output: []byte(frame.output), err: childRunError{body: *frame.errorBody, code: childProcessExitCode(waitErr)}}
	}
	if waitErr != nil {
		return executionResult{output: []byte(frame.output), err: childExitError(waitErr, stderr)}
	}
	return executionResult{output: []byte(frame.output), syncReport: frame.syncReport}
}

func childProgressFrame(progress Progress) childFrame {
	return childFrame{kind: childFrameProgress, progress: progress}
}

func childLogFrame(text string) childFrame {
	return childFrame{kind: childFrameLog, logText: text}
}

func childResultFrame(output string, report *SyncReport, body *output.ErrorBody) childFrame {
	return childFrame{kind: childFrameResult, output: output, syncReport: report, errorBody: body}
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
		return fmt.Errorf("mutating verb made no progress for %s", watchdog)
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
