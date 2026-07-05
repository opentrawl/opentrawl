package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/control"
	crawlog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

// cliError carries a command failure's exit code and the crawlkit error body
// (crawlkit/output). One shape: WriteJSONErrorIfNeeded renders it as
// {"error": {...}} in JSON mode; in text mode main prints Error().
type cliError struct {
	code    int
	name    string
	message string
	remedy  string
	fields  map[string]any
	human   string
	err     error
}

func (e *cliError) Error() string {
	if strings.TrimSpace(e.human) != "" {
		return e.human
	}
	if e.remedy == "" {
		return e.message
	}
	return e.message + ". " + e.remedy
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
	var codeErr *cliError
	if errors.As(err, &codeErr) {
		return codeErr.code
	}
	return 1
}

type runtime struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	json   bool
	log    *crawlog.Run
}

const calcrawlLogFileName = "calcrawl.log"

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	verbosity, args := pullVerbosity(args)
	jsonOut, args := pullJSONFlag(args)
	run, err := newLogRun(args, jsonOut, stderr, verbosity)
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr(1, "log_open_failed", fmt.Errorf("cannot open command log: %w", err), "check the local calcrawl log directory"))
	}
	r := &runtime{ctx: ctx, stdout: stdout, stderr: stderr, json: jsonOut, log: run}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, r.finish(nil))
	}
	if args[0] == "help" {
		if len(args) == 1 {
			printUsage(stdout)
			return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, r.finish(nil))
		}
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, r.finish(printCommandUsage(stdout, args[1:])))
	}
	if args[0] == "--version" || args[0] == "version" {
		_, _ = io.WriteString(stdout, version+"\n")
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, r.finish(nil))
	}
	return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, r.finish(r.dispatch(args)))
}

func newLogRun(args []string, jsonOut bool, stderr io.Writer, verbosity int) (*crawlog.Run, error) {
	stateRoot, crawlerID := logPathParts(defaultLogDir())
	return crawlog.NewRun(crawlog.Options{
		StateRoot:    stateRoot,
		CrawlerID:    crawlerID,
		FileName:     calcrawlLogFileName,
		Command:      logCommand(args),
		Version:      version,
		JSONProgress: jsonOut,
		Stderr:       stderr,
		Verbosity:    verbosity,
	})
}

func logCommand(args []string) string {
	if len(args) == 0 {
		return "help"
	}
	command := strings.TrimSpace(args[0])
	switch command {
	case "-h", "--help", "help":
		return "help"
	case "--version":
		return "version"
	case "contacts":
		if len(args) > 1 && strings.TrimSpace(args[1]) != "" && !strings.HasPrefix(args[1], "-") {
			return "contacts_" + strings.TrimSpace(args[1])
		}
	}
	command = strings.TrimLeft(command, "-")
	command = strings.ReplaceAll(command, "-", "_")
	if command == "" {
		return "help"
	}
	return command
}

func (r *runtime) finish(err error) error {
	if err != nil {
		if logErr := r.log.Error(errorEvent(err), loggableError(err)); logErr != nil {
			err = errors.Join(err, logErr)
		}
	}
	if finishErr := r.log.Finish(loggableError(err)); err == nil {
		return finishErr
	}
	return err
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
	case "status":
		return r.runStatus(args[1:])
	case "sync":
		return r.runSync(args[1:])
	case "search":
		return r.runSearch(args[1:])
	case "who":
		return r.runWho(args[1:])
	case "open":
		return r.runOpen(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "contacts":
		return r.runContacts(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) parseNoFlags(command string, args []string) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet("calcrawl "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, usageErr(err)
	}
	return fs, nil
}

func (r *runtime) print(value any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch typed := value.(type) {
	case manifestOutput:
		return printManifestText(r.stdout, typed)
	case statusText:
		return printStatusText(r.stdout, typed)
	case doctorOutput:
		return printDoctorText(r.stdout, typed)
	case searchOutput:
		return printSearchText(r.stdout, typed)
	case whoOutput:
		return printWhoText(r.stdout, typed)
	case archive.EventDetail:
		return printOpenText(r.stdout, typed)
	case control.ContactExport:
		return printContactsText(r.stdout, typed)
	default:
		return fmt.Errorf("internal: no human renderer for %T", value)
	}
}

func (r *runtime) printJSONLine(value any) error {
	enc := json.NewEncoder(r.stdout)
	return enc.Encode(value)
}

// usageRemedy is the one next-step hint for every caller mistake. It rides the
// error body's remedy field, kept out of the message (rules §2.4).
const usageRemedy = "Run 'calcrawl --help'."

func usageErr(err error) error {
	return &cliError{code: 2, name: "usage", message: err.Error(), remedy: usageRemedy, err: err}
}

func archiveErr(err error) error {
	return commandErr(1, "archive", err, "run: calcrawl sync")
}

func sourceErr(err error) error {
	return commandErr(1, "source_store", err, fullDiskAccessRemedy)
}

func commandErr(code int, kind string, err error, remedy string) error {
	message := err.Error()
	wrapped := err
	if strings.TrimSpace(remedy) != "" {
		wrapped = crawlog.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	}
	return &cliError{code: code, name: kind, message: message, remedy: remedy, err: wrapped}
}

func errorEvent(err error) string {
	var codeErr *cliError
	if errors.As(err, &codeErr) && strings.TrimSpace(codeErr.name) != "" {
		return codeErr.name
	}
	return "command_failed"
}

// loggableError keeps the health log clean: it records a command failure's
// short machine message, never the rendered human table a who error carries.
func loggableError(err error) error {
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.message != "" {
		return errors.New(codeErr.message)
	}
	return err
}

func oneArg(args []string, name string) (string, error) {
	if len(args) != 1 {
		return "", usageErr(fmt.Errorf("%s requires one argument", name))
	}
	value := strings.TrimSpace(args[0])
	if value == "" {
		return "", usageErr(fmt.Errorf("%s argument cannot be empty", name))
	}
	return value, nil
}
