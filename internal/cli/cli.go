package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/imsgcrawl/internal/messages"
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
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	json   bool
	dbPath string
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	jsonOut, args := pullJSONFlag(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return nil
	}
	global := flag.NewFlagSet("imsgcrawl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dbPath := global.String("db", messages.DefaultChatDBPath(), "")
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
	if rest[0] == "version" {
		_, _ = io.WriteString(stdout, version+"\n")
		return nil
	}
	r := &runtime{ctx: ctx, stdout: stdout, stderr: stderr, json: jsonOut, dbPath: *dbPath}
	return r.dispatch(rest)
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

func (r *runtime) dispatch(args []string) error {
	switch args[0] {
	case "metadata":
		return r.print(controlManifest())
	case "status":
		return r.runStatus(args[1:])
	case "contacts":
		return r.runContacts(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown command %q", args[0]))
	}
}

func (r *runtime) runStatus(args []string) error {
	fs := flag.NewFlagSet("imsgcrawl status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("status takes no arguments"))
	}
	status, err := messages.Status(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	return r.print(status)
}

func (r *runtime) runContacts(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("usage: imsgcrawl contacts export"))
	}
	switch args[0] {
	case "export":
		return r.runContactsExport(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown contacts command %q", args[0]))
	}
}

type contactExport struct {
	Contacts []messages.ExportedContact `json:"contacts"`
}

func (r *runtime) runContactsExport(args []string) error {
	fs := flag.NewFlagSet("imsgcrawl contacts export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	contacts, err := messages.ExportContacts(r.ctx, r.dbPath)
	if err != nil {
		return err
	}
	return r.print(contactExport{Contacts: contacts})
}

func (r *runtime) print(v any) error {
	enc := json.NewEncoder(r.stdout)
	if r.json {
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	switch value := v.(type) {
	case messages.StatusReport:
		_, err := fmt.Fprintf(r.stdout, "db_path: %s\nstate: %s\nhandles: %d\nchats: %d\nmessages: %d\nphone_handles: %d\nemail_handles: %d\nother_handles: %d\n",
			value.DatabasePath, value.State, value.Handles, value.Chats, value.Messages, value.PhoneHandles, value.EmailHandles, value.OtherHandles)
		return err
	case contactExport:
		for _, contact := range value.Contacts {
			_, err := fmt.Fprintf(r.stdout, "%s\t%s\n", contact.DisplayName, strings.Join(contact.PhoneNumbers, ","))
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return enc.Encode(v)
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `imsgcrawl reads local iMessage Messages data.

Usage:
  imsgcrawl [--json] [--db PATH] metadata
  imsgcrawl [--json] [--db PATH] status
  imsgcrawl [--json] [--db PATH] contacts export
  imsgcrawl --version
`)
}

func usageErr(err error) error {
	return &cliError{code: 2, err: err}
}

func defaultBaseDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".imsgcrawl")
	}
	return ".imsgcrawl"
}
