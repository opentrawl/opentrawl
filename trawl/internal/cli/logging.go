package cli

import (
	"errors"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
)

const (
	trawlLogFileName = "trawl.log"
)

type logRun = cklog.Run

func (r *Runtime) startLogRun(command string) error {
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: stateRoot,
		LogOwner:  cklog.NewCentralTrawlCommandLogOwner(),
		FileName:  trawlLogFileName,
		Command:   logCommandName(command),
		Version:   Version,
		Platform:  goruntime.GOOS + "/" + goruntime.GOARCH,
		Verbosity: r.verbosity(),
		Stderr:    r.output.DiagnosticLogStream(),
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
