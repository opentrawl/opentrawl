package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

var buildVersion = "dev"

type runOptions struct {
	stdout          io.Writer
	stderr          io.Writer
	executable      string
	childPrefixArgs []string
	childEnv        []string
	// stateRoot is test-only injection for in-process runs.
	stateRoot     string
	baseContext   context.Context
	readTimeout   time.Duration
	watchdog      time.Duration
	killGrace     time.Duration
	signalContext func(context.Context) (context.Context, context.CancelFunc)
	// newWatchdogTimer builds the child watchdog timer. Tests inject a fake so
	// the watchdog does not depend on wall-clock scheduling.
	newWatchdogTimer func(time.Duration) watchdogTimer
}

type runner struct {
	opts runOptions
}

type executionResult struct {
	output     []byte
	syncReport *SyncReport
	err        error
}

func Run(argv []string, sources []Crawler) int {
	r := runner{opts: defaultRunOptions()}
	return r.run(argv, sources)
}

// RunContext executes the same runner lifecycle as Run and also stops the
// isolated child when the caller's context is cancelled.
func RunContext(ctx context.Context, argv []string, sources []Crawler) int {
	if ctx == nil {
		ctx = context.Background()
	}
	r := runner{opts: defaultRunOptions()}
	r.opts.baseContext = ctx
	return r.run(argv, sources)
}

// RunSyncContext executes sync through the same supervised child route as Run
// and returns the typed report before any JSON or human rendering.
func RunSyncContext(ctx context.Context, argv []string, sources []Crawler, stderr io.Writer) (*SyncReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r := runner{opts: defaultRunOptions()}
	r.opts.baseContext = ctx
	r.opts.stdout = io.Discard
	if stderr != nil {
		r.opts.stderr = stderr
	}
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()
	globals, err := parseGlobal(argv)
	if err != nil {
		return nil, err
	}
	source, rest, err := selectSource(globals.args, sources)
	if err != nil {
		return nil, err
	}
	verb, err := resolveVerb(source, rest)
	if err != nil {
		return nil, err
	}
	if verb.name != "sync" {
		return nil, fmt.Errorf("typed sync runner requires sync, got %q", verb.name)
	}
	result := r.dispatch(ctx, source, rest, globals, output.JSON, false)
	if result.err != nil {
		return nil, result.err
	}
	if result.syncReport == nil {
		return nil, errors.New("sync child returned no typed sync result")
	}
	return result.syncReport, nil
}

func defaultRunOptions() runOptions {
	stdout, stderr := output.StandardWriters()
	return runOptions{
		stdout:           stdout,
		stderr:           stderr,
		readTimeout:      DefaultReadTimeout,
		watchdog:         DefaultWatchdog,
		killGrace:        DefaultKillGrace,
		signalContext:    defaultSignalContext,
		newWatchdogTimer: newRealWatchdogTimer,
	}
}

func (r runner) run(argv []string, sources []Crawler) int {
	r.opts = r.opts.withDefaults()
	ctx := r.opts.baseContext
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := r.opts.signalContext(ctx)
	defer stop()

	if len(argv) > 0 && argv[0] == HiddenWireSubcommand {
		return r.runWireChild(ctx, argv[1:], sources)
	}
	globals, err := parseGlobal(argv)
	if err != nil {
		if globals.json {
			renderError(r.opts.stdout, output.JSON, err)
		} else {
			renderError(r.opts.stderr, output.Text, err)
		}
		return exitCodeFor(err)
	}
	if r.opts.stateRoot != "" {
		globals.stateRoot = r.opts.stateRoot
	}
	format := output.Text
	if globals.json {
		format = output.JSON
	}
	if globals.version {
		_, _ = fmt.Fprintln(r.opts.stdout, buildVersion)
		return 0
	}
	if ok, target := helpRequested(globals); ok {
		if len(sources) > 1 && len(target) == 0 {
			if err := writeRootHelp(r.opts.stdout, sources); err != nil {
				renderError(r.opts.stderr, output.Text, err)
				return exitCodeFor(err)
			}
			return 0
		}
		source, rest, err := selectSource(target, sources)
		if err != nil {
			renderError(r.opts.stderr, output.Text, err)
			return exitCodeFor(err)
		}
		if err := writeHelp(r.opts.stdout, source, rest, globals.stateRoot); err != nil {
			renderError(r.opts.stderr, output.Text, err)
			return exitCodeFor(err)
		}
		return 0
	}
	source, rest, err := selectSource(globals.args, sources)
	if err != nil {
		renderError(r.errorWriter(format), format, err)
		return exitCodeFor(err)
	}
	result := r.dispatch(ctx, source, rest, globals, format, false)
	if result.err != nil {
		if format == output.Text {
			_, _ = r.opts.stdout.Write(result.output)
		}
		renderError(r.errorWriter(format), format, result.err)
		return exitCodeFor(result.err)
	}
	if result.syncReport != nil {
		if err := writeResult(r.opts.stdout, format, "sync", result.syncReport); err != nil {
			renderError(r.errorWriter(format), format, err)
			return exitCodeFor(err)
		}
	} else {
		_, _ = r.opts.stdout.Write(result.output)
	}
	return 0
}

func (r runner) errorWriter(format output.Format) io.Writer {
	if format == output.JSON {
		return r.opts.stdout
	}
	return r.opts.stderr
}

func (opts runOptions) withDefaults() runOptions {
	defaults := defaultRunOptions()
	if opts.stdout == nil {
		opts.stdout = defaults.stdout
	}
	if opts.stderr == nil {
		opts.stderr = defaults.stderr
	}
	if opts.readTimeout == 0 {
		opts.readTimeout = defaults.readTimeout
	}
	if opts.watchdog == 0 {
		opts.watchdog = defaults.watchdog
	}
	if opts.killGrace == 0 {
		opts.killGrace = defaults.killGrace
	}
	if opts.signalContext == nil {
		opts.signalContext = defaults.signalContext
	}
	if opts.newWatchdogTimer == nil {
		opts.newWatchdogTimer = defaults.newWatchdogTimer
	}
	return opts
}

func defaultSignalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}
