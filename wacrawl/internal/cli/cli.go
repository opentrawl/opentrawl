package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/config"
	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

const (
	defaultMessageLimit = 20
	messageRefPrefix    = store.MessageRefPrefix
	openWindowEachSide  = 10
)

type cliError struct {
	code    int
	name    string
	message string
	remedy  string
	fields  map[string]any
	err     error
}

func (e *cliError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.message
}

func (e *cliError) Unwrap() error { return e.err }

func (e *cliError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    e.name,
		Message: e.message,
		Remedy:  e.remedy,
		Fields:  e.fields,
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	return 1
}

type app struct {
	stdout    io.Writer
	stderr    io.Writer
	json      bool
	verbosity int
	dbPath    string
	source    string
	runLog    *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	verbosity, args := extractVerbosityFlag(args)
	args, jsonAnywhere := extractJSONFlag(args)
	global := flag.NewFlagSet("wacrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	helpFlag := global.Bool("help", false, "")
	helpShortFlag := global.Bool("h", false, "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil
		}
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonAnywhere, usageErr(err))
	}
	a := &app{stdout: stdout, stderr: stderr, json: *jsonOut || jsonAnywhere, verbosity: verbosity, dbPath: *dbPath, source: *source}
	rest := global.Args()
	if err := a.startLogRun(rootCommandName(rest, *versionFlag, *helpFlag || *helpShortFlag)); err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, a.json, commandErr("log_open_failed", "cannot open command log", "check the local wacrawl log directory", 1, nil, err))
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return a.finishLogRun(nil, rest)
	}
	if *helpFlag || *helpShortFlag || len(rest) == 0 {
		printUsage(stdout)
		return a.finishLogRun(nil, rest)
	}
	err := a.dispatch(ctx, rest)
	err = a.finishLogRun(err, rest)
	return ckoutput.WriteJSONErrorIfNeeded(a.stdout, a.json, err)
}

func (a *app) dispatch(ctx context.Context, rest []string) error {
	if rest[0] == "help" {
		if len(rest) == 1 {
			printUsage(a.stdout)
			return nil
		}
		if printCommandUsage(a.stdout, rest[1:]...) {
			return nil
		}
		return usageErr(fmt.Errorf("unknown help topic %q", strings.Join(rest[1:], " ")))
	}
	switch rest[0] {
	case "metadata":
		return a.print(controlManifest())
	case "doctor":
		return a.runDoctor(ctx, rest[1:])
	case "import", "sync":
		return a.runImport(ctx, rest[0], rest[1:])
	case "status":
		return a.runStatus(ctx, rest[1:])
	case "chats":
		return a.runChats(ctx, rest[1:])
	case "contacts":
		return a.runContacts(ctx, rest[1:])
	case "who":
		return a.runWho(ctx, rest[1:])
	case "unread":
		return a.runUnread(ctx, rest[1:])
	case "messages":
		return a.runMessages(ctx, rest[1:])
	case "search":
		return a.runSearch(ctx, rest[1:])
	case "open":
		return a.runOpen(ctx, rest[1:])
	case "sql":
		return a.runSQL(ctx, rest[1:])
	case "web":
		return a.runWeb(ctx, rest[1:])
	case "backup":
		return a.runBackup(ctx, rest[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", rest[0]))
	}
}

func (a *app) startLogRun(command string) error {
	run, err := a.newLogRun(command)
	if err != nil {
		return err
	}
	a.runLog = run
	return nil
}

func (a *app) finishLogRun(err error, rest []string) error {
	if a.runLog == nil {
		return err
	}
	if err != nil {
		_ = a.runLog.Error(errorEvent(rest, err), err)
	}
	if finishErr := a.runLog.Finish(err); err == nil {
		return finishErr
	}
	return err
}

func extractVerbosityFlag(args []string) (int, []string) {
	out := make([]string, 0, len(args))
	verbosity := 0
	for _, arg := range args {
		switch arg {
		case "-v", "--verbose":
			verbosity++
		case "-vv":
			verbosity += 2
		default:
			out = append(out, arg)
		}
	}
	return verbosity, out
}

func extractJSONFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	jsonOut := false
	literalArgs := false
	for _, arg := range args {
		if literalArgs {
			out = append(out, arg)
			continue
		}
		if arg == "--" {
			literalArgs = true
			out = append(out, arg)
			continue
		}
		if arg == "--json" {
			jsonOut = true
			continue
		}
		out = append(out, arg)
	}
	return out, jsonOut
}

func rootCommandName(rest []string, versionOut, helpOut bool) string {
	if versionOut {
		return "version"
	}
	if helpOut || len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" || rest[0] == "help" {
		return "help"
	}
	return logCommandName(rest)
}

// wacrawlPaths is the one archive path layout, from crawlkit/config. The
// base dir is the fleet-wide state root, ~/.opentrawl/wacrawl (TRAWL-99).
func wacrawlPaths() config.Paths {
	paths, _ := config.App{Name: "wacrawl", BaseDir: "~/.opentrawl/wacrawl"}.DefaultPaths()
	return paths
}

func defaultDBPath() string {
	return wacrawlPaths().DBPath
}

func usageErr(err error) error {
	return commandErr("usage", err.Error(), "run wacrawl help", 2, nil, err)
}

func commandErr(name, message, remedy string, code int, fields map[string]any, err error) error {
	return &cliError{code: code, name: name, message: message, remedy: remedy, fields: fields, err: err}
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty time")
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q", value)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(time.Local).Format(time.RFC3339)
}

// shortLocalTime is the human-mode time: short local. JSON keeps full
// RFC3339 via formatTime; the two surfaces never share a formatter.
func shortLocalTime(t time.Time) string {
	return render.ShortLocalTime(t)
}

// displayRef is the ref shown in human rows: the short alias when known,
// the full ref otherwise. Full refs live in JSON (docs/short-refs.md).
func displayRef(fullRef, shortRef string) string {
	if strings.TrimSpace(shortRef) != "" {
		return shortRef
	}
	return fullRef
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
