package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckrender "github.com/opentrawl/opentrawl/trawlkit/render"
)

const (
	trawlLogFileName = "trawl.log"
)

type logRun = cklog.Run

func (r *Runtime) startLogRun(command string) error {
	stateRoot, registeredTrawlerIdentity, err := trawlLogParts(r.stateRoot)
	if err != nil {
		return err
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot:                 stateRoot,
		RegisteredTrawlerIdentity: registeredTrawlerIdentity,
		FileName:                  trawlLogFileName,
		Command:                   logCommandName(command),
		Version:                   Version,
		Platform:                  goruntime.GOOS + "/" + goruntime.GOARCH,
		Verbosity:                 r.verbosity(),
		Stderr:                    r.lockedStderr(),
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
	_ = r.log.Finish(err)
	return err
}

func (r *Runtime) logInfo(event, message string) {
	if r == nil || r.log == nil {
		return
	}
	_ = r.log.Info(event, message)
}

func (r *Runtime) logTrawlerStart(trawler InstalledTrawler, commandName string) time.Time {
	started := r.now()
	r.logInfo("trawler_start", strings.Join([]string{
		trawlerField(trawler),
		"command=" + logQuote(commandName),
	}, " "))
	return started
}

func (r *Runtime) logTrawlerDone(trawler InstalledTrawler, commandName string, started time.Time, err error, fields ...string) {
	out := []string{
		trawlerField(trawler),
		"command=" + logQuote(commandName),
		elapsedField(started, r.now()),
	}
	if err != nil {
		if isTimeoutError(err) {
			out = append(out, "outcome=timeout")
		} else {
			out = append(out, "outcome=error")
		}
		out = append(out, "error="+logQuote(cklog.InternalErrorLogMessage(err)))
	} else {
		out = append(out, "outcome=ok")
		out = append(out, fields...)
	}
	r.logInfo("trawler_done", strings.Join(out, " "))
}

func (r *Runtime) verbosity() int {
	if r == nil || r.root == nil || r.root.Verbose < 0 {
		return 0
	}
	return r.root.Verbose
}

func trawlHelpPrinter(options kong.HelpOptions, ctx *kong.Context) error {
	if ctx.Selected() != nil {
		if ckrender.OutputWidth(ctx.Stdout) < 80 {
			return writeNarrowSelectedCommandHelp(ctx)
		}
		return kong.DefaultHelpPrinter(options, ctx)
	}
	sources := discoverInstalledTrawlers(context.Background())
	outputWidth := ckrender.OutputWidth(ctx.Stdout)
	commandRows := formatRowsForOutputWidth([][2]string{
		{"status [<trawler>]", statusCommandHelpDescription},
		{"update [<trawler> ...]", "Get new items from apps"},
		{"search [<words> ...]", "Find anything in your archive"},
		{"who <name>", "Find a person"},
		{"conversations", "List conversations"},
		{"messages --conversation LINK", "List messages in one conversation"},
		{"open LINK", "Open a result"},
	}, 2, outputWidth)
	flagRows := formatRowsForOutputWidth([][2]string{
		{"-h, --help", "Show help"},
		{"-v, --verbose", "Show detailed progress; use -vv for debug detail"},
		{"--version", "Print version and exit"},
	}, 2, outputWidth)
	sections := []string{
		wrapTextForOutputWidth(trawlOrientation, outputWidth),
		fmt.Sprintf(`%s

Flags:
%s

Commands:
%s`,
			wrapTextForOutputWidth(
				"Usage: "+ckrender.TrawlInvocationDisplay(ctx.Stdout)+" <command> [flags]",
				outputWidth,
			),
			strings.Join(flagRows, "\n"),
			strings.Join(commandRows, "\n"),
		),
		trawlersBlock(sources, outputWidth),
		startHereBlock(ckrender.TrawlInvocationDisplay(ctx.Stdout), outputWidth),
	}
	_, err := fmt.Fprintln(ctx.Stdout, strings.Join(sections, "\n\n"))
	return err
}

func writeNarrowSelectedCommandHelp(ctx *kong.Context) error {
	selectedCommand := ctx.Selected()
	usageParts := []string{ckrender.TrawlInvocationDisplay(ctx.Stdout), ctx.Command()}
	for _, positionalArgument := range selectedCommand.Positional {
		usageParts = append(
			usageParts,
			narrowCommandPositionalArgumentSummary(ctx.Command(), positionalArgument.Summary()),
		)
	}
	usageParts = append(usageParts, "[flags]")
	usage := "Usage: " + strings.Join(usageParts, " ")
	if ctx.Command() == "update" {
		usage = strings.Join(
			ckrender.WrapWithIndent("Usage: ", strings.Join(usageParts, " "), ckrender.OutputWidth(ctx.Stdout), "  "),
			"\n",
		)
	}
	if _, err := fmt.Fprintln(
		ctx.Stdout,
		wrapTextForOutputWidth(usage, ckrender.OutputWidth(ctx.Stdout)),
	); err != nil {
		return err
	}
	if commandDescription := strings.TrimSpace(selectedCommand.Help); commandDescription != "" {
		if _, err := fmt.Fprintln(
			ctx.Stdout,
			"\n"+wrapTextForOutputWidth(commandDescription, ckrender.OutputWidth(ctx.Stdout)),
		); err != nil {
			return err
		}
	}
	if commandDetail := strings.TrimSpace(selectedCommand.Detail); commandDetail != "" {
		if _, err := fmt.Fprintln(ctx.Stdout); err != nil {
			return err
		}
		for _, detailLine := range strings.Split(commandDetail, "\n") {
			if strings.HasPrefix(detailLine, "  ") {
				if err := ckrender.WriteIndentedTrawlCommand(ctx.Stdout, strings.TrimSpace(detailLine)); err != nil {
					return err
				}
				continue
			}
			for _, wrappedLine := range ckrender.Wrap(detailLine, ckrender.OutputWidth(ctx.Stdout)) {
				if _, err := fmt.Fprintln(ctx.Stdout, wrappedLine); err != nil {
					return err
				}
			}
		}
	}
	if len(selectedCommand.Positional) > 0 {
		if _, err := fmt.Fprintln(ctx.Stdout, "\nArguments:"); err != nil {
			return err
		}
		argumentRows := make([][2]string, 0, len(selectedCommand.Positional))
		for _, positionalArgument := range selectedCommand.Positional {
			argumentRows = append(argumentRows, [2]string{
				narrowCommandPositionalArgumentSummary(ctx.Command(), positionalArgument.Summary()),
				positionalArgument.Help,
			})
		}
		for _, line := range formatRowsForOutputWidth(argumentRows, 2, ckrender.OutputWidth(ctx.Stdout)) {
			if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "\nFlags:"); err != nil {
		return err
	}
	var flagRows [][2]string
	for _, flagGroup := range selectedCommand.AllFlags(true) {
		for _, commandFlag := range flagGroup {
			flagRows = append(flagRows, [2]string{commandFlag.String(), commandFlag.Help})
		}
	}
	for _, line := range formatRowsForOutputWidth(flagRows, 2, ckrender.OutputWidth(ctx.Stdout)) {
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func narrowCommandPositionalArgumentSummary(commandName string, positionalArgumentSummary string) string {
	if commandName == "update" && positionalArgumentSummary == "[<trawler> ...]" {
		return "[TRAWLERS]"
	}
	return positionalArgumentSummary
}

func commandName(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "status", "update", "search", "who", "conversations", "messages", "open":
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

func trawlLogParts(configuredRoot string) (string, string, error) {
	root, err := trawlkit.ResolveStateRoot(configuredRoot)
	if err != nil {
		return "", "", err
	}
	return root, "trawl", nil
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

func (w lockedWriter) UnwrapWriter() io.Writer {
	return w.dst
}

type trawlerTimeoutError struct {
	command string
}

func (e trawlerTimeoutError) Error() string {
	return e.command + " timed out"
}

func isTimeoutError(err error) bool {
	var timeout trawlerTimeoutError
	return errors.As(err, &timeout)
}

func trawlerField(trawler InstalledTrawler) string {
	return "trawler=" + logQuote(firstNonEmpty(installedTrawlerIdentityText(trawler), trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName(), "unknown"))
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
