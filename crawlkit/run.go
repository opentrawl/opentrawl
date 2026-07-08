package crawlkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openclaw/crawlkit/output"
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
}

type runner struct {
	opts runOptions
}

type executionResult struct {
	output []byte
	err    error
}

func Run(argv []string, sources []Crawler) int {
	r := runner{opts: defaultRunOptions()}
	return r.run(argv, sources)
}

func defaultRunOptions() runOptions {
	stdout, stderr := output.StandardWriters()
	return runOptions{
		stdout:        stdout,
		stderr:        stderr,
		readTimeout:   DefaultReadTimeout,
		watchdog:      DefaultWatchdog,
		killGrace:     DefaultKillGrace,
		signalContext: defaultSignalContext,
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
	_, _ = r.opts.stdout.Write(result.output)
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
	return opts
}

func defaultSignalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}
