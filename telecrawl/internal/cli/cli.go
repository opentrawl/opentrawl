package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/telecrawl/internal/store"
)

type cliError struct {
	code  int
	err   error
	quiet bool
	event string
}

func (e *cliError) Error() string {
	return e.err.Error()
}

func (e *cliError) Unwrap() error {
	return e.err
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 1
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	return 1
}

func ShouldPrintError(err error) bool {
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return !codeErr.quiet
	}
	return err != nil
}

type runtime struct {
	ctx       context.Context
	stdout    io.Writer
	stderr    io.Writer
	json      bool
	verbosity int
	dbPath    string
	source    string
	log       *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	verbosity, args := pullVerbosity(args)
	jsonFlag, args := pullJSONFlag(args)
	global := flag.NewFlagSet("telecrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	helpFlag := global.Bool("help", false, "")
	helpShortFlag := global.Bool("h", false, "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		return usageErr(err)
	}
	rest := global.Args()
	r := &runtime{
		ctx:       ctx,
		stdout:    stdout,
		stderr:    stderr,
		json:      jsonFlag || *jsonOut,
		verbosity: verbosity,
		dbPath:    *dbPath,
		source:    *source,
	}
	if err := r.startLogRun(commandName(rest, *versionFlag, *helpFlag || *helpShortFlag)); err != nil {
		return r.contractError("log_open_failed", "cannot open command log: "+err.Error(), "check the local telecrawl log directory")
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return r.finishLogRun(nil)
	}
	if *helpFlag || *helpShortFlag || len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(stdout)
		return r.finishLogRun(nil)
	}
	return r.finishLogRun(r.dispatch(rest))
}

func commandName(args []string, versionOut, helpOut bool) string {
	if versionOut {
		return "version"
	}
	if helpOut || len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return "help"
	}
	switch args[0] {
	case "metadata", "import", "sync", "wiretap", "doctor", "status", "chats", "folders", "contacts", "topics", "messages", "search", "who", "open", "backup", "version":
		return args[0]
	default:
		return "unknown"
	}
}

func errorEventCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "command_canceled"
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.event != "" {
		return codeErr.event
	}
	return "command_failed"
}

func pullVerbosity(args []string) (int, []string) {
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

func pullJSONFlag(args []string) (bool, []string) {
	out := make([]string, 0, len(args))
	jsonOut := false
	for _, arg := range args {
		if arg == "--json" || arg == "-json" {
			jsonOut = true
			continue
		}
		out = append(out, arg)
	}
	return jsonOut, out
}

func (r *runtime) dispatch(args []string) error {
	if args[0] == "help" {
		if len(args) == 1 {
			printUsage(r.stdout)
			return nil
		}
		printCommandUsage(r.stdout, args[1:])
		return nil
	}
	if len(args) > 1 && hasHelpFlag(args[1:]) {
		printCommandUsage(r.stdout, args)
		return nil
	}
	switch args[0] {
	case "metadata":
		if r.json {
			return r.print(contractMetadata())
		}
		return r.print(controlManifest())
	case "import", "sync":
		return r.runImport(args[0], args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "status":
		return r.runStatus(args[1:])
	case "chats":
		return r.runChats(args[1:])
	case "folders":
		return r.runFolders(args[1:])
	case "contacts":
		return r.runContacts(args[1:])
	case "topics":
		return r.runTopics(args[1:])
	case "messages":
		return r.runMessages(args[1:])
	case "search":
		return r.runSearch(args[1:])
	case "who":
		return r.runWho(args[1:])
	case "open":
		return r.runOpen(args[1:])
	case "backup":
		return r.runBackup(args[1:])
	case "wiretap":
		return r.runImport(args[0], args[1:])
	case "version":
		_, _ = io.WriteString(r.stdout, version+"\n")
		return nil
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Open(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}
