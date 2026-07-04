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
	ctx          context.Context
	stdout       io.Writer
	stderr       io.Writer
	json         bool
	dbPath       string
	source       string
	logStateRoot string
	log          *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonFlag, args := pullJSONFlag(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	global := flag.NewFlagSet("telecrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	jsonOut := global.Bool("json", false, "")
	dbPath := global.String("db", defaultDBPath(), "")
	source := global.String("source", "", "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		return usageErr(err)
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	rest := global.Args()
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	r := &runtime{
		ctx:          ctx,
		stdout:       stdout,
		stderr:       stderr,
		json:         jsonFlag || *jsonOut,
		dbPath:       *dbPath,
		source:       *source,
		logStateRoot: logStateRoot(*dbPath),
	}
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: r.logStateRoot,
		CrawlerID: "telecrawl",
		Command:   logCommandName(rest[0]),
		Version:   version,
		Stderr:    stderr,
	})
	if err != nil {
		return err
	}
	r.log = run
	err = r.dispatch(rest)
	if err != nil {
		_ = r.log.Error(errorEventCode(err), err)
	}
	if finishErr := r.log.Finish(err); finishErr != nil {
		return errors.Join(err, finishErr)
	}
	return err
}

func logCommandName(command string) string {
	switch command {
	case "metadata", "import", "sync", "wiretap", "doctor", "status", "chats", "folders", "contacts", "topics", "messages", "search", "who", "open", "backup", "version":
		return command
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
		return r.runImport(args[1:])
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
		return r.runImport(args[1:])
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
