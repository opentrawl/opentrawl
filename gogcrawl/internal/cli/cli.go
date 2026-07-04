package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

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
	if e.human != "" {
		return e.human
	}
	if e.remedy == "" {
		return e.message
	}
	return e.message + "; " + e.remedy
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

type runtime struct {
	ctx            context.Context
	stdout         io.Writer
	stderr         io.Writer
	json           bool
	verbosity      int
	archivePath    string
	backupRepoPath string
	gog            gog.Client
	log            *cklog.Run
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	verbosity, args, err := pullVerbosity(args)
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, hasFlag(args, "--json"), usageErr(err))
	}
	jsonOut, args := pullFlag(args, "--json")
	versionOut, args := pullFlag(args, "--version")
	archivePath, args, err := pullValueFlag(args, "--archive")
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, usageErr(err))
	}
	if strings.TrimSpace(archivePath) == "" {
		archivePath = archive.DefaultPath()
	}
	backupRepoPath, args, err := pullValueFlag(args, "--backup-repo")
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, usageErr(err))
	}
	if strings.TrimSpace(backupRepoPath) == "" {
		backupRepoPath = archive.DefaultBackupRepoPath()
	}
	if versionOut {
		run, err := newCommandLog("version", stderr, jsonOut, verbosity)
		if err != nil {
			return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr("log_open_failed", "cannot open command log", "check the local gogcrawl log directory", err))
		}
		_, _ = io.WriteString(stdout, version+"\n")
		return finishCommandLog(run, nil)
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		run, err := newCommandLog("help", stderr, jsonOut, verbosity)
		if err != nil {
			return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr("log_open_failed", "cannot open command log", "check the local gogcrawl log directory", err))
		}
		printUsage(stdout)
		return finishCommandLog(run, nil)
	}
	if args[0] == "help" {
		run, err := newCommandLog("help", stderr, jsonOut, verbosity)
		if err != nil {
			return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr("log_open_failed", "cannot open command log", "check the local gogcrawl log directory", err))
		}
		if len(args) == 1 {
			printUsage(stdout)
			return finishCommandLog(run, nil)
		}
		err = printCommandUsage(stdout, args[1:])
		if err != nil {
			_ = run.Error(errorEvent(err), err)
		}
		if logErr := finishCommandLog(run, err); err == nil {
			err = logErr
		}
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
	}
	run, err := newCommandLog(commandName(args), stderr, jsonOut, verbosity)
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, commandErr("log_open_failed", "cannot open command log", "check the local gogcrawl log directory", err))
	}
	r := &runtime{
		ctx:            ctx,
		stdout:         stdout,
		stderr:         stderr,
		json:           jsonOut,
		verbosity:      verbosity,
		archivePath:    archivePath,
		backupRepoPath: backupRepoPath,
		gog:            gog.New(gog.DefaultBinary),
		log:            run,
	}
	err = r.dispatch(args)
	if err != nil {
		_ = run.Error(errorEvent(err), err)
	}
	if logErr := finishCommandLog(run, err); err == nil {
		err = logErr
	}
	return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 1
	}
	var codeErr *cliError
	if errors.As(err, &codeErr) && codeErr.code != 0 {
		return codeErr.code
	}
	return 1
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

func (r *runtime) runMetadata(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"metadata"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("metadata takes no arguments"))
	}
	return r.print(controlManifest())
}

func pullFlag(args []string, name string) (bool, []string) {
	out := make([]string, 0, len(args))
	found := false
	for _, arg := range args {
		if arg == name {
			found = true
			continue
		}
		out = append(out, arg)
	}
	return found, out
}

func pullVerbosity(args []string) (int, []string, error) {
	out := make([]string, 0, len(args))
	verbosity := 0
	for _, arg := range args {
		switch {
		case arg == "-v" || arg == "--verbose":
			verbosity++
			continue
		case arg == "-vv":
			verbosity += 2
			continue
		default:
			out = append(out, arg)
		}
	}
	return verbosity, out, nil
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name {
			return true
		}
	}
	return false
}

func pullValueFlag(args []string, name string) (string, []string, error) {
	out := make([]string, 0, len(args))
	var value string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s requires a value", name)
			}
			value = args[i+1]
			i++
			continue
		}
		if after, ok := strings.CutPrefix(arg, name+"="); ok {
			value = after
			continue
		}
		out = append(out, arg)
	}
	return value, out, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "-help" {
			return true
		}
	}
	return false
}

func usageErr(err error) error {
	return commandErr("usage", err.Error(), "run gogcrawl help", err)
}

func commandErr(name, message, remedy string, err error) error {
	return commandErrWith(name, message, remedy, 1, nil, "", err)
}

func commandErrWith(name, message, remedy string, code int, fields map[string]any, human string, err error) error {
	err = cklog.WorldMustChange{Err: err, Message: message, Remedy: remedy}
	return &cliError{code: code, name: name, message: message, remedy: remedy, fields: fields, human: human, err: err}
}
