package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	cklog "github.com/openclaw/crawlkit/log"
)

const (
	trawlLogFileName      = "trawl.log"
	verboseLogsCapability = "verbose_logs"
)

type logRun = cklog.Run

func (r *Runtime) startLogRun(command string) error {
	stateRoot, crawlerID, err := trawlLogParts()
	if err != nil {
		return err
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: stateRoot,
		CrawlerID: crawlerID,
		FileName:  trawlLogFileName,
		Command:   logCommandName(command),
		Version:   Version,
		Platform:  goruntime.GOOS + "/" + goruntime.GOARCH,
		Verbosity: r.verbosity(),
		Stderr:    r.lockedStderr(),
	})
	if err != nil {
		return err
	}
	r.log = run
	return nil
}

func (r *Runtime) finishLogRun(err error) error {
	if r == nil || r.log == nil {
		return err
	}
	if logErr := r.log.Finish(err); err == nil && logErr != nil {
		return logErr
	}
	return err
}

func (r *Runtime) logInfo(event, message string) {
	if r == nil || r.log == nil {
		return
	}
	_ = r.log.Info(event, message)
}

func (r *Runtime) logDebug(event, message string) {
	if r == nil || r.log == nil {
		return
	}
	_ = r.log.Debug(event, message)
}

func (r *Runtime) logSourceStart(source Source, verb string) time.Time {
	started := r.now()
	r.logInfo("source_start", strings.Join([]string{
		sourceField(source),
		"verb=" + logQuote(verb),
	}, " "))
	return started
}

func (r *Runtime) logSourceDone(source Source, verb string, started time.Time, err error, fields ...string) {
	out := []string{
		sourceField(source),
		"verb=" + logQuote(verb),
		elapsedField(started, r.now()),
	}
	if err != nil {
		if isTimeoutError(err) {
			out = append(out, "outcome=timeout")
		} else {
			out = append(out, "outcome=error")
		}
		out = append(out, "error="+logQuote(err.Error()))
	} else {
		out = append(out, "outcome=ok")
		out = append(out, fields...)
	}
	r.logInfo("source_done", strings.Join(out, " "))
}

func (r *Runtime) verbosity() int {
	if r == nil || r.root == nil || r.root.Verbose < 0 {
		return 0
	}
	return r.root.Verbose
}

func trawlHelpPrinter(options kong.HelpOptions, ctx *kong.Context) error {
	if err := kong.DefaultHelpPrinter(options, ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(ctx.Stdout)
	_, err := fmt.Fprintln(ctx.Stdout, "Diagnostics: run with -v, or read ~/.trawl/logs/trawl.log")
	return err
}

func commandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "status", "sync", "search", "summaries", "who", "open", "doctor":
			return arg
		}
	}
	return "help"
}

func logCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "command"
	}
	var b strings.Builder
	for i, char := range command {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9' && i > 0:
			b.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			b.WriteRune(char + ('a' - 'A'))
		case char == '_' || char == '-' || char == '.':
			b.WriteRune(char)
		}
	}
	if b.Len() == 0 {
		return "command"
	}
	return b.String()
}

func trawlLogParts() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", "", fmt.Errorf("resolve home for trawl log: %w", err)
	}
	return home, ".trawl", nil
}

func (r *Runtime) childVerboseArgs(source Source) []string {
	if r.verbosity() <= 0 || !hasCapability(source, verboseLogsCapability) {
		return nil
	}
	if r.verbosity() >= 2 {
		return []string{"-vv"}
	}
	return []string{"-v"}
}

func (r *Runtime) runSourceCommandJSON(source Source, args ...string) ([]byte, error) {
	return r.runSourceCommandWithTimeout(source, r.timeout, args...)
}

func (r *Runtime) runSourceJSONVerb(source Source, verb string, args ...string) ([]byte, error) {
	commandArgs := append([]string{verb}, args...)
	commandArgs = append(commandArgs, "--json")
	commandArgs = append(commandArgs, r.childVerboseArgs(source)...)
	return r.runSourceCommandWithTimeout(source, r.timeout, commandArgs...)
}

func (r *Runtime) runSourceJSONVerbNoTimeout(source Source, verb string, args ...string) ([]byte, error) {
	commandArgs := append([]string{verb}, args...)
	commandArgs = append(commandArgs, "--json")
	commandArgs = append(commandArgs, r.childVerboseArgs(source)...)
	return r.runSourceCommand(r.ctx, source, commandArgs...)
}

func (r *Runtime) runSourceCommandWithTimeout(source Source, timeout time.Duration, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()

	data, err := r.runSourceCommand(commandCtx, source, args...)
	if err != nil {
		if commandCtx.Err() != nil {
			return data, sourceTimeoutError{command: strings.Join(args, " ")}
		}
		return data, err
	}
	return data, nil
}

func (r *Runtime) runSourceCommand(ctx context.Context, source Source, args ...string) ([]byte, error) {
	r.logSourceExec(source, args)
	cmd := exec.CommandContext(ctx, source.Path, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	var childStderr *sourceStderrWriter
	if r.verbosity() > 0 && hasCapability(source, verboseLogsCapability) {
		childStderr = r.sourceStderr(source)
		cmd.Stderr = childStderr
	}
	err := cmd.Run()
	if childStderr != nil {
		if closeErr := childStderr.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return stdout.Bytes(), crawlerCommandError{
			command: strings.Join(args, " "),
			err:     err,
		}
	}
	return stdout.Bytes(), nil
}

func (r *Runtime) lockedStderr() io.Writer {
	return lockedWriter{dst: r.stderr, mu: &r.stderrMu}
}

func (r *Runtime) sourceStderr(source Source) *sourceStderrWriter {
	return &sourceStderrWriter{
		dst:    r.stderr,
		mu:     &r.stderrMu,
		prefix: sourceField(source) + " ",
	}
}

type lockedWriter struct {
	dst io.Writer
	mu  *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dst.Write(p)
}

type sourceStderrWriter struct {
	dst    io.Writer
	mu     *sync.Mutex
	prefix string
	buf    []byte
}

func (w *sourceStderrWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.buf = append(w.buf, p...)
			return written + len(p), nil
		}
		w.buf = append(w.buf, p[:i+1]...)
		written += i + 1
		if err := w.flush(); err != nil {
			return written, err
		}
		p = p[i+1:]
	}
	return written, nil
}

func (w *sourceStderrWriter) Close() error {
	if len(w.buf) == 0 {
		return nil
	}
	return w.flush()
}

func (w *sourceStderrWriter) flush() error {
	line := make([]byte, 0, len(w.prefix)+len(w.buf))
	line = append(line, w.prefix...)
	line = append(line, w.buf...)
	w.buf = w.buf[:0]

	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.dst.Write(line)
	return err
}

func (r *Runtime) logSourceExec(source Source, args []string) {
	if r.verbosity() < 2 {
		return
	}
	argv := append([]string{source.Path}, args...)
	r.logDebug("source_exec", strings.Join([]string{
		sourceField(source),
		"argv=" + logQuote(strings.Join(argv, " ")),
	}, " "))
}

type sourceTimeoutError struct {
	command string
}

func (e sourceTimeoutError) Error() string {
	return e.command + " timed out"
}

func isTimeoutError(err error) bool {
	var timeout sourceTimeoutError
	return errors.As(err, &timeout)
}

func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func sourceField(source Source) string {
	return "source=" + logQuote(firstNonEmpty(source.ID, source.Binary, "unknown"))
}

func elapsedField(started time.Time, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	if started.IsZero() {
		return "elapsed_ms=0"
	}
	return "elapsed_ms=" + strconv.FormatInt(now.Sub(started).Milliseconds(), 10)
}

func logQuote(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}
