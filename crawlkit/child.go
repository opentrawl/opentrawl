package crawlkit

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

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/crawlkit/output"
)

type childFrame struct {
	SchemaVersion int               `json:"schema_version"`
	Type          string            `json:"type"`
	Progress      *Progress         `json:"progress,omitempty"`
	Output        string            `json:"output,omitempty"`
	Error         *output.ErrorBody `json:"error,omitempty"`
	ExitCode      int               `json:"exit_code,omitempty"`
	LogLine       *childLogLine     `json:"log_line,omitempty"`
}

type childLogLine struct {
	Level  string `json:"level"`
	Source string `json:"source"`
	Line   string `json:"line"`
}

type childLogFrameWriter struct {
	mu      sync.Mutex
	w       io.Writer
	source  string
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
		err := writeChildFrame(w.w, childFrame{
			Type: "log",
			LogLine: &childLogLine{
				Level:  childLogLevel(line),
				Source: w.source,
				Line:   line,
			},
		})
		if err != nil {
			return len(p), err
		}
	}
}

func childLogLevel(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return ""
	}
	return strings.ToLower(fields[2])
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

func (r runner) runWireChild(ctx context.Context, argv []string, sources []Crawler) int {
	globals, err := parseGlobal(argv)
	format := output.Text
	if globals.json {
		format = output.JSON
	}
	var result executionResult
	if err == nil {
		source, rest, selectErr := selectSource(globals.args, sources)
		if selectErr != nil {
			err = selectErr
		} else {
			result = r.dispatch(ctx, source, rest, globals, format, true)
			err = result.err
		}
	}
	frame := childFrame{
		Type:     "result",
		Output:   string(result.output),
		ExitCode: exitCodeFor(err),
	}
	if err != nil {
		body := errorBodyFor(err)
		frame.Error = &body
	}
	if writeErr := writeChildFrame(r.opts.stdout, frame); writeErr != nil && err == nil {
		return 1
	}
	return frame.ExitCode
}

func (r runner) runChild(ctx context.Context, source Crawler, verb targetVerb, globals globalOptions, format output.Format) executionResult {
	paths, err := resolveSourcePaths(globals.stateRoot, source.Info().ID)
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
	args = append(args, HiddenWireSubcommand, "--crawlkit-run-id", runLog.RunID())
	switch globals.verbosity {
	case 1:
		args = append(args, "-v")
	case 2:
		args = append(args, "-vv")
	}
	if globals.stateRoot != "" {
		args = append(args, "--state-root", globals.stateRoot)
	}
	if format == output.JSON {
		args = append(args, "--json")
	}
	args = append(args, source.Info().ID)
	args = append(args, verb.childArgs()...)
	args = append(args, verb.args...)
	cmd := exec.Command(executable, args...) // #nosec G204 -- self-reexec path and test helper are controlled by the runner.
	configureChildCommand(cmd)
	if len(r.opts.childEnv) > 0 {
		cmd.Env = append([]string(nil), r.opts.childEnv...)
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
	watchdog := r.opts.watchdog
	if verb.timeout > 0 {
		watchdog = verb.timeout
	}
	result := waitForChild(ctx, cmd, stdout, stderr.String, watchdog, r.opts.killGrace, runLog, globals.verbosity, r.opts.stderr)
	if err := finishRunLog(runLog, result.err); result.err == nil && err != nil {
		result.err = err
	}
	return result
}

func waitForChild(ctx context.Context, cmd *exec.Cmd, stdout io.Reader, stderr func() string, watchdog, grace time.Duration, runLog *cklog.Run, verbosity int, logStream io.Writer) executionResult {
	frames := make(chan childFrame)
	decodeErrs := make(chan error, 1)
	go decodeChildFrames(stdout, frames, decodeErrs)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(watchdog)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			terminateChild(cmd, done, grace)
			return executionResult{err: ctx.Err()}
		case <-timer.C:
			terminateChild(cmd, done, grace)
			return executionResult{err: fmt.Errorf("mutating verb made no progress for %s", watchdog)}
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			switch frame.Type {
			case "progress":
				if frame.Progress != nil {
					logProgress(runLog, *frame.Progress)
				}
				resetTimer(timer, watchdog)
			case "log":
				if frame.LogLine != nil && verbosity > 0 && logStream != nil {
					_, _ = fmt.Fprintln(logStream, frame.LogLine.Line)
				}
			case "result":
				waitErr := waitForChildExit(ctx, cmd, done, watchdog, grace)
				if frame.Error != nil {
					var exitErr *exec.ExitError
					if waitErr != nil && !errors.As(waitErr, &exitErr) {
						return executionResult{output: []byte(frame.Output), err: childExitError(waitErr, stderr())}
					}
					return executionResult{output: []byte(frame.Output), err: childRunError{body: *frame.Error, code: frame.ExitCode}}
				}
				if waitErr != nil {
					return executionResult{output: []byte(frame.Output), err: childExitError(waitErr, stderr())}
				}
				return executionResult{output: []byte(frame.Output)}
			}
		case err := <-decodeErrs:
			waitErr := waitForChildExit(ctx, cmd, done, watchdog, grace)
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

func waitForChildExit(ctx context.Context, cmd *exec.Cmd, done <-chan error, watchdog, grace time.Duration) error {
	timer := time.NewTimer(watchdog)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		terminateChild(cmd, done, grace)
		return ctx.Err()
	case <-timer.C:
		terminateChild(cmd, done, grace)
		return fmt.Errorf("mutating verb made no progress for %s", watchdog)
	case err := <-done:
		return err
	}
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

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
