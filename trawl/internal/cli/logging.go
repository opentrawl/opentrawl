package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
)

const (
	trawlLogFileName = "trawl.log"
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
	// Only the root help page carries the Sources block and the agents
	// appendix; a subcommand's own --help (ctx.Selected() != nil) must not
	// pay for discovery or repeat them. The blocks are appended here, past
	// kong's word-wrapping, so their columns render exactly as built.
	if ctx.Selected() == nil {
		sources := discoverCrawlers(context.Background())
		_, _ = fmt.Fprintf(ctx.Stdout, "\n%s\n", sourcesBlock(sources))
		_, _ = fmt.Fprintf(ctx.Stdout, "\n%s\n", helpAgentsBlock)
	}
	_, _ = fmt.Fprintln(ctx.Stdout)
	_, err := fmt.Fprintln(ctx.Stdout, "Diagnostics: run with -v, or read ~/.opentrawl/trawl/logs/trawl.log")
	return err
}

func commandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "status", "sync", "search", "summaries", "who", "chats", "open":
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
	return filepath.Join(home, ".opentrawl"), "trawl", nil
}

func (r *Runtime) lockedStderr() io.Writer {
	return lockedWriter{dst: r.stderr, mu: &r.stderrMu}
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
