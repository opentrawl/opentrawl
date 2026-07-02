package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

type cliError struct {
	code    int
	name    string
	message string
	remedy  string
	err     error
}

func (e *cliError) Error() string {
	if e.remedy == "" {
		return e.message
	}
	return e.message + "; " + e.remedy
}

func (e *cliError) Unwrap() error { return e.err }

type runtime struct {
	ctx            context.Context
	stdout         io.Writer
	stderr         io.Writer
	json           bool
	archivePath    string
	backupRepoPath string
	gog            gog.Client
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonOut, args := pullFlag(args, "--json")
	versionOut, args := pullFlag(args, "--version")
	archivePath, args, err := pullValueFlag(args, "--archive")
	if err != nil {
		return writeJSONErrorIfNeeded(stdout, jsonOut, usageErr(err))
	}
	if strings.TrimSpace(archivePath) == "" {
		archivePath = archive.DefaultPath()
	}
	backupRepoPath, args, err := pullValueFlag(args, "--backup-repo")
	if err != nil {
		return writeJSONErrorIfNeeded(stdout, jsonOut, usageErr(err))
	}
	if strings.TrimSpace(backupRepoPath) == "" {
		backupRepoPath = archive.DefaultBackupRepoPath()
	}
	if versionOut {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
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
	r := &runtime{
		ctx:            ctx,
		stdout:         stdout,
		stderr:         stderr,
		json:           jsonOut,
		archivePath:    archivePath,
		backupRepoPath: backupRepoPath,
		gog:            gog.New(gog.DefaultBinary),
	}
	err = r.dispatch(args)
	return writeJSONErrorIfNeeded(stdout, jsonOut, err)
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
	return &cliError{code: 1, name: "usage", message: err.Error(), remedy: "run gogcrawl help", err: err}
}

func commandErr(name, message, remedy string, err error) error {
	return &cliError{code: 1, name: name, message: message, remedy: remedy, err: err}
}

func writeJSONErrorIfNeeded(w io.Writer, jsonOut bool, err error) error {
	if err == nil || !jsonOut {
		return err
	}
	var codeErr *cliError
	name := "command_failed"
	message := err.Error()
	remedy := ""
	if errors.As(err, &codeErr) {
		name = codeErr.name
		message = codeErr.message
		remedy = codeErr.remedy
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    name,
			"message": message,
			"remedy":  remedy,
		},
	})
	return err
}
