// Package pluginruntime hosts standalone out-of-process OpenTrawl trawler
// plugins over raw length-delimited binary protobuf and provides the SDK
// entrypoint for standalone Go plugin executables.
package pluginruntime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/pluginproxy"
	"github.com/opentrawl/opentrawl/trawlkit/pluginsandbox"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
	worker "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/worker"
	"github.com/opentrawl/opentrawl/trawlkit/prototransport"
	"github.com/opentrawl/opentrawl/trawlkit/secrets"
)

const (
	pluginManifestProbeTimeout   = 5 * time.Second
	pluginDefaultReadTimeout     = 30 * time.Second
	pluginDefaultUpdateWatchdog  = 5 * time.Minute
	pluginTerminationGracePeriod = 2 * time.Second
	pluginMaxStderrBytes         = 64 * 1024
)

type pluginInvocationExecutionOptions struct {
	ExecutablePath         string
	ExecutableArgs         []string
	SandboxPolicy          pluginsandbox.PluginSandboxPolicy
	Request                *plugincontract.PluginInvocationRequest
	HardDeadline           time.Duration
	IdleWatchdog           time.Duration
	RunLog                 *cklog.Run
	SecretBroker           *secrets.PluginSecretBroker
	ApprovedAuthOrigins    []string
	OpenBrowserURL         func(ctx context.Context, targetURL string) error
	AllowedPublicHosts     []string
	SelfHostedNetworkHosts []pluginproxy.SelfHostedNetworkHostExemption
}

type pluginInvocationOutcome struct {
	Result                   *plugincontract.PluginInvocationResult
	StagedIndexChangeBatches []*indexcontract.ArchiveIndexChangeBatch
	ExitCode                 int
	StderrExcerpt            string
}

type boundedTextBuffer struct {
	mu      sync.Mutex
	maxSize int
	buf     bytes.Buffer
}

func (b *boundedTextBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.maxSize - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedTextBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

func invokeSandboxedPlugin(
	ctx context.Context,
	opts pluginInvocationExecutionOptions,
) (pluginInvocationOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.HardDeadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.HardDeadline)
		defer cancel()
	}

	privateTempDir, err := os.MkdirTemp("", "opentrawl-plugin-tmp-*")
	if err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("create private plugin temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(privateTempDir) }()

	policy := opts.SandboxPolicy
	policy.ExecutablePath = opts.ExecutablePath
	if len(policy.WritableDirectoryPaths) > 0 {
		policy.WritableDirectoryPaths = append(append([]string(nil), policy.WritableDirectoryPaths...), privateTempDir)
	}

	var connectProxy *pluginproxy.AllowlistConnectProxy
	if len(opts.AllowedPublicHosts) > 0 || len(opts.SelfHostedNetworkHosts) > 0 {
		connectProxy, err = pluginproxy.StartAllowlistConnectProxy(pluginproxy.AllowlistConnectProxyOptions{
			AllowedPublicHosts: opts.AllowedPublicHosts,
			SelfHostedHosts:    opts.SelfHostedNetworkHosts,
		})
		if err != nil {
			return pluginInvocationOutcome{}, fmt.Errorf("start plugin network proxy: %w", err)
		}
		defer func() { _ = connectProxy.Close() }()
		policy.LoopbackProxyPort = connectProxy.Port()
		policy.AllowNetworkTrustEvaluation = true
		opts.Request.NetworkProxyGrant = &plugincontract.PluginProxyConnectionGrant{
			LoopbackProxyAddress:     connectProxy.Address(),
			ProxyAuthenticationToken: connectProxy.AuthToken(),
		}
	}

	if opts.SecretBroker != nil {
		loadedSecrets, secretErr := opts.SecretBroker.LoadApprovedSecrets()
		if secretErr != nil && !errors.Is(secretErr, secrets.ErrPluginSecretStoreUnavailable) {
			return pluginInvocationOutcome{}, secretErr
		}
		for key, value := range loadedSecrets {
			opts.Request.BrokeredSecrets = append(opts.Request.BrokeredSecrets, &plugincontract.PluginSecretEntry{
				SecretKey:   key,
				SecretBytes: value,
			})
		}
	}

	var encodedRequest bytes.Buffer
	if err := prototransport.WriteDelimited(&encodedRequest, opts.Request); err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("encode plugin invocation request: %w", err)
	}

	cmd, err := pluginsandbox.CommandContext(ctx, policy, opts.ExecutableArgs...)
	if err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("prepare sandboxed plugin command: %w", err)
	}
	trawlkit.ConfigureExternalPluginChildProcess(cmd)

	minimalEnv := buildMinimalPluginEnvironment(privateTempDir)
	lifetimePipe, envWithLifetime, err := trawlkit.AttachExternalPluginParentLifetimePipe(cmd, minimalEnv)
	if err != nil {
		return pluginInvocationOutcome{}, err
	}
	defer func() { _ = lifetimePipe.Close() }()
	cmd.Env = envWithLifetime
	cmd.Stdin = &encodedRequest

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("open plugin stdout pipe: %w", err)
	}
	stderrCapture := &boundedTextBuffer{maxSize: pluginMaxStderrBytes}
	cmd.Stderr = stderrCapture

	if err := cmd.Start(); err != nil {
		return pluginInvocationOutcome{}, fmt.Errorf("start sandboxed plugin: %w", err)
	}
	lifetimePipe.ChildStarted()

	return supervisePluginStream(ctx, cmd, stdoutPipe, stderrCapture, opts)
}

func buildMinimalPluginEnvironment(privateTempDir string) []string {
	env := []string{
		"PATH=/usr/bin:/bin",
		"TMPDIR=" + privateTempDir,
	}
	for _, key := range []string{"HOME", "TZ", "LANG", "LC_ALL", "LC_CTYPE"} {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			env = append(env, key+"="+val)
		}
	}
	return env
}

func supervisePluginStream(
	ctx context.Context,
	cmd *exec.Cmd,
	stdout io.Reader,
	stderrCapture *boundedTextBuffer,
	opts pluginInvocationExecutionOptions,
) (pluginInvocationOutcome, error) {
	frames := make(chan *plugincontract.PluginStreamFrame)
	decodeErrs := make(chan error, 1)
	waitDone := make(chan error, 1)

	go func() {
		decodePluginStreamFrames(stdout, frames, decodeErrs)
		waitDone <- cmd.Wait()
	}()

	watchdogWindow := opts.IdleWatchdog
	if watchdogWindow <= 0 {
		watchdogWindow = pluginDefaultReadTimeout
	}
	watchdogTimer := time.NewTimer(watchdogWindow)
	defer watchdogTimer.Stop()

	var terminalResult *plugincontract.PluginInvocationResult
	var stagedBatches []*indexcontract.ArchiveIndexChangeBatch
	var sideEffectErrors []error

	for {
		select {
		case <-ctx.Done():
			terminatePluginAndDrain(cmd, waitDone, frames, decodeErrs)
			return pluginInvocationOutcome{StderrExcerpt: stderrCapture.String()}, ctx.Err()
		case <-watchdogTimer.C:
			terminatePluginAndDrain(cmd, waitDone, frames, decodeErrs)
			return pluginInvocationOutcome{StderrExcerpt: stderrCapture.String()}, fmt.Errorf("plugin made no progress for %s", watchdogWindow)
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			resetPluginTimer(watchdogTimer, watchdogWindow)
			if terminalResult != nil {
				terminatePluginAndDrain(cmd, waitDone, frames, decodeErrs)
				return pluginInvocationOutcome{StderrExcerpt: stderrCapture.String()}, errors.New("plugin emitted a frame after its terminal result")
			}
			if result, batch, sideErr := handlePluginStreamFrame(ctx, frame, opts); sideErr != nil {
				sideEffectErrors = append(sideEffectErrors, sideErr)
			} else if result != nil {
				terminalResult = result
			} else if batch != nil {
				stagedBatches = append(stagedBatches, batch)
			}
		case decodeErr := <-decodeErrs:
			waitErr := waitForPluginProcessExit(ctx, cmd, waitDone, watchdogWindow)
			stderrText := stderrCapture.String()
			return finalizePluginInvocationOutcome(terminalResult, stagedBatches, sideEffectErrors, decodeErr, waitErr, stderrText)
		}
	}
}

func decodePluginStreamFrames(
	stdout io.Reader,
	frames chan<- *plugincontract.PluginStreamFrame,
	decodeErrs chan<- error,
) {
	reader := bufio.NewReader(stdout)
	for {
		frame := &plugincontract.PluginStreamFrame{}
		if err := prototransport.ReadDelimited(reader, frame); err != nil {
			close(frames)
			decodeErrs <- err
			return
		}
		frames <- frame
	}
}

func handlePluginStreamFrame(
	ctx context.Context,
	frame *plugincontract.PluginStreamFrame,
	opts pluginInvocationExecutionOptions,
) (*plugincontract.PluginInvocationResult, *indexcontract.ArchiveIndexChangeBatch, error) {
	switch kind := frame.GetKind().(type) {
	case *plugincontract.PluginStreamFrame_Progress:
		if opts.RunLog != nil && kind.Progress != nil {
			_ = opts.RunLog.Info("plugin_progress", formatPluginProgressLog(kind.Progress))
		}
		return nil, nil, nil
	case *plugincontract.PluginStreamFrame_Log:
		if opts.RunLog != nil && kind.Log != nil && strings.TrimSpace(kind.Log.GetText()) != "" {
			_ = opts.RunLog.ShowAlreadyStoredChildLogLine(kind.Log.GetText())
		}
		return nil, nil, nil
	case *plugincontract.PluginStreamFrame_ArchiveIndexChangeBatch:
		return nil, kind.ArchiveIndexChangeBatch, nil
	case *plugincontract.PluginStreamFrame_SecretMutation:
		if kind.SecretMutation == nil {
			return nil, nil, nil
		}
		if opts.SecretBroker == nil {
			return nil, nil, errors.New("plugin attempted secret mutation without an active secret broker")
		}
		return nil, nil, opts.SecretBroker.ApplySecretMutation(
			kind.SecretMutation.GetSecretKey(),
			kind.SecretMutation.GetSecretBytes(),
			kind.SecretMutation.GetDeleteSecret(),
		)
	case *plugincontract.PluginStreamFrame_OpenBrowserUrl:
		if kind.OpenBrowserUrl == nil {
			return nil, nil, nil
		}
		return nil, nil, handleOpenBrowserURLNotice(ctx, kind.OpenBrowserUrl.GetUrl(), opts.ApprovedAuthOrigins, opts.OpenBrowserURL)
	case *plugincontract.PluginStreamFrame_Result:
		return kind.Result, nil, nil
	default:
		return nil, nil, errors.New("plugin emitted an unknown stream frame kind")
	}
}

func handleOpenBrowserURLNotice(
	ctx context.Context,
	rawURL string,
	approvedOrigins []string,
	opener func(context.Context, string) error,
) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || strings.TrimSpace(parsed.Host) == "" {
		return fmt.Errorf("plugin requested invalid browser URL %q", rawURL)
	}
	origin := "https://" + strings.ToLower(parsed.Host)
	originAllowed := false
	for _, approved := range approvedOrigins {
		if strings.EqualFold(strings.TrimSpace(approved), origin) {
			originAllowed = true
			break
		}
	}
	if !originAllowed {
		return fmt.Errorf("plugin browser URL origin %q is not approved", origin)
	}
	if opener == nil {
		return nil
	}
	return opener(ctx, parsed.String())
}

func finalizePluginInvocationOutcome(
	terminalResult *plugincontract.PluginInvocationResult,
	stagedBatches []*indexcontract.ArchiveIndexChangeBatch,
	sideEffectErrors []error,
	decodeErr error,
	waitErr error,
	stderrText string,
) (pluginInvocationOutcome, error) {
	exitCode := pluginProcessExitCode(waitErr)
	if !errors.Is(decodeErr, io.EOF) {
		if waitErr != nil {
			return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, formatPluginExitFailure(waitErr, stderrText)
		}
		return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, fmt.Errorf("read plugin protobuf stream: %w", decodeErr)
	}
	if terminalResult == nil {
		if waitErr != nil {
			return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, formatPluginExitFailure(waitErr, stderrText)
		}
		return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, errors.New("plugin exited without sending a result frame")
	}
	if len(sideEffectErrors) > 0 {
		return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, errors.Join(sideEffectErrors...)
	}
	if workerErr := terminalResult.GetError(); workerErr != nil {
		if terminalResult.GetSuccess() != nil {
			return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, errors.New("plugin result combined both success and error")
		}
		description := output.ErrorDescription{
			Code:                workerErr.GetCode(),
			Message:             workerErr.GetMessage(),
			LockPath:            workerErr.GetLockPath(),
			PhotosStepsNotBuilt: workerErr.GetPhotosStepsNotBuilt(),
		}
		return pluginInvocationOutcome{Result: terminalResult, ExitCode: exitCode, StderrExcerpt: stderrText},
			trawlkit.ExternalPluginRunErrorFromDescription(description, exitCode)
	}
	if waitErr != nil {
		return pluginInvocationOutcome{ExitCode: exitCode, StderrExcerpt: stderrText}, formatPluginExitFailure(waitErr, stderrText)
	}
	return pluginInvocationOutcome{
		Result:                   terminalResult,
		StagedIndexChangeBatches: stagedBatches,
		ExitCode:                 0,
		StderrExcerpt:            stderrText,
	}, nil
}

func formatPluginProgressLog(progress *worker.Progress) string {
	parts := []string{fmt.Sprintf("phase=%s done=%d", strings.TrimSpace(progress.GetPhase()), progress.GetDone())}
	if progress.GetTotal() > 0 {
		parts = append(parts, fmt.Sprintf("total=%d", progress.GetTotal()))
	}
	if msg := strings.TrimSpace(progress.GetMessage()); msg != "" {
		parts = append(parts, fmt.Sprintf("message=%q", msg))
	}
	return strings.Join(parts, " ")
}

func waitForPluginProcessExit(ctx context.Context, cmd *exec.Cmd, waitDone <-chan error, watchdog time.Duration) error {
	timer := time.NewTimer(watchdog)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		terminatePluginProcess(cmd, waitDone)
		return ctx.Err()
	case <-timer.C:
		terminatePluginProcess(cmd, waitDone)
		return fmt.Errorf("plugin did not exit within %s after closing stdout", watchdog)
	case err := <-waitDone:
		return err
	}
}

func terminatePluginProcess(cmd *exec.Cmd, waitDone <-chan error) {
	if cmd.Process == nil {
		return
	}
	_ = trawlkit.SignalExternalPluginChildProcess(cmd, syscall.SIGTERM)
	timer := time.NewTimer(pluginTerminationGracePeriod)
	defer timer.Stop()
	select {
	case <-waitDone:
		return
	case <-timer.C:
		_ = trawlkit.KillExternalPluginChildProcess(cmd)
		<-waitDone
	}
}

func terminatePluginAndDrain(
	cmd *exec.Cmd,
	waitDone <-chan error,
	frames <-chan *plugincontract.PluginStreamFrame,
	decodeErrs <-chan error,
) {
	if cmd.Process == nil {
		return
	}
	_ = trawlkit.SignalExternalPluginChildProcess(cmd, syscall.SIGTERM)
	timer := time.NewTimer(pluginTerminationGracePeriod)
	defer timer.Stop()
	timerC := timer.C
	for {
		select {
		case <-waitDone:
			return
		case _, ok := <-frames:
			if !ok {
				frames = nil
			}
		case <-decodeErrs:
			decodeErrs = nil
		case <-timerC:
			_ = trawlkit.KillExternalPluginChildProcess(cmd)
			timerC = nil
		}
	}
}

func resetPluginTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

func pluginProcessExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func formatPluginExitFailure(waitErr error, stderrText string) error {
	if stderrText != "" {
		return fmt.Errorf("plugin process failed: %w: %s", waitErr, stderrText)
	}
	return fmt.Errorf("plugin process failed: %w", waitErr)
}
