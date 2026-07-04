package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/imsgcrawl/internal/archive"
	"github.com/openclaw/imsgcrawl/internal/messages"
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
	if errors.Is(err, context.Canceled) {
		return 1
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	return 1
}

type runtime struct {
	ctx         context.Context
	stdout      io.Writer
	stderr      io.Writer
	json        bool
	verbosity   int
	dbPath      string
	archivePath string
	command     string
	runLog      *logRun
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	verbosity, args := pullVerbosity(args)
	jsonOut, args := pullJSONFlag(args)
	global := flag.NewFlagSet("imsgcrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dbPath := global.String("db", messages.DefaultChatDBPath(), "")
	archivePath := global.String("archive", archive.DefaultPath(), "")
	helpFlag := global.Bool("help", false, "")
	helpShortFlag := global.Bool("h", false, "")
	versionFlag := global.Bool("version", false, "")
	if err := global.Parse(args); err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, usageErr(err))
	}
	rest := global.Args()
	command := commandName(rest, *versionFlag, *helpFlag || *helpShortFlag)
	r := &runtime{ctx: ctx, stdout: stdout, stderr: stderr, json: jsonOut, verbosity: verbosity, dbPath: *dbPath, archivePath: *archivePath, command: command}
	if err := r.startLogRun(); err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr("log_open_failed", "cannot open command log", "check the local imsgcrawl log directory", 1, nil, err))
	}
	if *versionFlag {
		_, _ = io.WriteString(stdout, version+"\n")
		return r.finishLogRun(nil)
	}
	if *helpFlag || *helpShortFlag || len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" {
		printUsage(stdout)
		return r.finishLogRun(nil)
	}
	if rest[0] == "help" {
		if len(rest) == 1 {
			printUsage(stdout)
			return r.finishLogRun(nil)
		}
		err := printCommandUsage(stdout, rest[1:])
		if err != nil {
			_ = r.logError(errorEvent(r.command, err), err)
		}
		if logErr := r.finishLogRun(err); err == nil {
			err = logErr
		}
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
	}
	if rest[0] == "version" {
		_, _ = io.WriteString(stdout, version+"\n")
		return r.finishLogRun(nil)
	}
	err := r.dispatch(rest)
	err = r.finishLogRun(err)
	return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
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
		if arg == "--json" {
			jsonOut = true
			continue
		}
		out = append(out, arg)
	}
	return jsonOut, out
}

func commandName(args []string, versionOut, helpOut bool) string {
	if versionOut {
		return "version"
	}
	if helpOut || len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return "help"
	}
	switch args[0] {
	case "metadata", "sync", "status", "doctor", "chats", "messages", "who", "search", "open", "contacts", "version":
		return args[0]
	default:
		return "unknown"
	}
}

func flagPassed(fs *flag.FlagSet, name string) bool {
	passed := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			passed = true
		}
	})
	return passed
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "-help" {
			return true
		}
	}
	return false
}

func (r *runtime) dispatch(args []string) error {
	switch args[0] {
	case "metadata":
		return r.runMetadata(args[1:])
	case "sync":
		return r.runSync(args[1:])
	case "status":
		return r.runStatus(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "chats":
		return r.runChats(args[1:])
	case "messages":
		return r.runMessages(args[1:])
	case "who":
		return r.runWho(args[1:])
	case "search":
		return r.runSearch(args[1:])
	case "open":
		return r.runOpen(args[1:])
	case "contacts":
		return r.runContacts(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) runMetadata(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"metadata"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("metadata takes no arguments"))
	}
	return r.print(controlManifest())
}

func usageErr(err error) error {
	return commandErr("usage", err.Error(), "run imsgcrawl help", 2, nil, err)
}

func commandErr(name, message, remedy string, code int, fields map[string]any, err error) error {
	return &cliError{code: code, name: name, message: message, remedy: remedy, fields: fields, err: err}
}

func defaultBaseDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".imsgcrawl")
	}
	return ".imsgcrawl"
}
