package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/wacrawl/internal/store"
)

const (
	defaultMessageLimit = 20
	maxMessageLimit     = 200
	messageRefPrefix    = store.MessageRefPrefix
	openWindowEachSide  = 10
)

type cliError struct {
	code int
	err  error
}

func (e *cliError) Error() string { return e.err.Error() }

func (e *cliError) Unwrap() error { return e.err }

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
	stdout io.Writer
	stderr io.Writer
	json   bool
	dbPath string
	source string
	runLog *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	args, jsonAnywhere := extractJSONFlag(args)
	global := flag.NewFlagSet("wacrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil
		}
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	a := &app{stdout: stdout, stderr: stderr, json: *jsonOut || jsonAnywhere, dbPath: *dbPath, source: *source}
	rest := global.Args()
	if len(rest) == 0 {
		printUsage(stdout)
		return nil
	}
	return a.runCommand(ctx, rest)
}

func (a *app) runCommand(ctx context.Context, rest []string) error {
	run, err := a.newLogRun(logCommandName(rest))
	if err != nil {
		return err
	}
	a.runLog = run
	err = a.dispatch(ctx, rest)
	if err != nil {
		_ = run.Error(errorEvent(rest, err), err)
	}
	if finishErr := run.Finish(err); err == nil {
		return finishErr
	}
	return err
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

func defaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "wacrawl.db"
	}
	return filepath.Join(home, ".wacrawl", "wacrawl.db")
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
