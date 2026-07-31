package trawlkit

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

var buildVersion = "dev"

type runOptions struct {
	stdout          io.Writer
	stderr          io.Writer
	executable      string
	childPrefixArgs []string
	childEnv        []string
	stdin           io.Reader
	childRequest    *worker.Request
	readTimeout     time.Duration
	watchdog        time.Duration
	killGrace       time.Duration
	signalContext   func(context.Context) (context.Context, context.CancelFunc)
	// newWatchdogTimer builds the child watchdog timer. Tests inject a fake so
	// the watchdog does not depend on wall-clock scheduling.
	newWatchdogTimer func(time.Duration) watchdogTimer
}

type runner struct {
	opts runOptions
}

type executionResult struct {
	updateReport                                   *update.TrawlerArchiveUpdateReport
	trawlerCommandResponse                         *command.TrawlerCommandResponse
	localShortReferencesByCanonicalRecordReference []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
	trawlerCommandRenderContext                    render.TrawlerCommandRenderContext
	err                                            error
}

// ExecuteTrawlerWireChild runs the private child side of the supervised
// trawler worker protocol. args start after HiddenWireSubcommand.
func ExecuteTrawlerWireChild(args []string, trawlers []Trawler) int {
	r := runner{opts: defaultRunOptions()}
	ctx, stop := r.opts.signalContext(context.Background())
	defer stop()
	return r.runWireChild(ctx, args, trawlers)
}

func defaultRunOptions() runOptions {
	stdout, stderr := output.StandardWriters()
	return runOptions{
		stdout:           stdout,
		stderr:           stderr,
		stdin:            os.Stdin,
		readTimeout:      DefaultReadTimeout,
		watchdog:         DefaultWatchdog,
		killGrace:        DefaultKillGrace,
		signalContext:    defaultSignalContext,
		newWatchdogTimer: newRealWatchdogTimer,
	}
}

func (opts runOptions) withDefaults() runOptions {
	defaults := defaultRunOptions()
	if opts.stdout == nil {
		opts.stdout = defaults.stdout
	}
	if opts.stderr == nil {
		opts.stderr = defaults.stderr
	}
	if opts.stdin == nil {
		opts.stdin = defaults.stdin
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
