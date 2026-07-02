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
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type cliError struct {
	code   int
	err    error
	kind   string
	remedy string
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

type printedError struct {
	err  error
	code int
}

func (e printedError) Error() string { return e.err.Error() }
func (e printedError) Unwrap() error { return e.err }

func ErrorPrinted(err error) bool {
	var printed printedError
	return errors.As(err, &printed)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var printed printedError
	if errors.As(err, &printed) {
		return printed.code
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
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonOut, args := pullJSONFlag(args)
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	if args[0] == "help" {
		if len(args) == 1 {
			printUsage(stdout)
			return nil
		}
		return printCommandUsage(stdout, args[1:])
	}
	if args[0] == "--version" || args[0] == "version" {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	r := &runtime{ctx: ctx, stdout: stdout, stderr: stderr, json: jsonOut}
	err := r.dispatch(args)
	if err == nil || !jsonOut {
		return err
	}
	if writeErr := r.printJSONError(err); writeErr != nil {
		return writeErr
	}
	return printedError{err: err, code: ExitCode(err)}
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
	case archive.EventDetail:
		return printOpenText(r.stdout, typed)
	case control.ContactExport:
		return printContactsText(r.stdout, typed)
	default:
		return enc.Encode(value)
	}
}

func (r *runtime) printJSONLine(value any) error {
	enc := json.NewEncoder(r.stdout)
	return enc.Encode(value)
}

func (r *runtime) printJSONError(err error) error {
	var codeErr *cliError
	out := errorOutput{}
	if errors.As(err, &codeErr) {
		out.Error.Code = codeErr.kind
		out.Error.Message = codeErr.err.Error()
		out.Error.Remedy = codeErr.remedy
	} else {
		out.Error.Code = "command_failed"
		out.Error.Message = err.Error()
	}
	if out.Error.Code == "" {
		out.Error.Code = "command_failed"
	}
	return json.NewEncoder(r.stdout).Encode(out)
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err, kind: "usage"}
}

func archiveErr(err error) error {
	return &cliError{code: 1, err: err, kind: "archive", remedy: "run: calcrawl sync"}
}

func sourceErr(err error) error {
	return &cliError{code: 1, err: err, kind: "source_store", remedy: fullDiskAccessRemedy}
}

type errorOutput struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Remedy  string `json:"remedy,omitempty"`
	} `json:"error"`
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
