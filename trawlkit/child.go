package trawlkit

import (
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
)

const childWireEnvRemedy = "invoke the parent crawler command; the runner supplies TRAWLKIT_STATE_ROOT and TRAWLKIT_RUN_ID for hidden wire child runs"

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
	name string
}

func (e childWireEnvError) Error() string {
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
	cmd.Env = env
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
	go decodeChildFrames(stdout, frames, decodeErrs)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := newTimer(watchdog)
	defer timer.stop()
	for {
		select {
		case <-ctx.Done():
			terminateChild(cmd, done, grace)
			return executionResult{err: ctx.Err()}
		case <-timer.tick():
			terminateChild(cmd, done, grace)
			return executionResult{err: fmt.Errorf("mutating verb made no progress for %s", watchdog)}
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
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
				waitErr := waitForChildExit(ctx, cmd, done, watchdog, grace, newTimer)
				if frame.errorBody != nil {
					if frame.output != "" || frame.syncReport != nil {
						return executionResult{err: errors.New("child result combined an error with a success result")}
					}
					var exitErr *exec.ExitError
					if waitErr != nil && !errors.As(waitErr, &exitErr) {
						return executionResult{output: []byte(frame.output), err: childExitError(waitErr, stderr())}
					}
					return executionResult{output: []byte(frame.output), err: childRunError{body: *frame.errorBody, code: childProcessExitCode(waitErr)}}
				}
				if waitErr != nil {
					return executionResult{output: []byte(frame.output), err: childExitError(waitErr, stderr())}
				}
				return executionResult{output: []byte(frame.output), syncReport: frame.syncReport}
			}
		case err := <-decodeErrs:
			waitErr := waitForChildExit(ctx, cmd, done, watchdog, grace, newTimer)
			if errors.Is(err, io.EOF) {
				err = nil
			}
			if waitErr != nil {
				return executionResult{err: childExitError(waitErr, stderr())}
			}
			if err != nil {
				return executionResult{err: fmt.Errorf("read child wire: %w", err)}
			}
			return executionResult{err: errors.New("child exited without a result frame")}
		}
	}
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

func childExitError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("child failed: %w: %s", err, stderr)
	}
	return fmt.Errorf("child failed: %w", err)
}
