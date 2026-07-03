package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/alecthomas/kong"
)

var Version = "dev"

type CLI struct {
	JSON        bool             `name:"json" help:"Write JSON to stdout"`
	VersionFlag kong.VersionFlag `name:"version" help:"Print version and exit"`

	Status StatusCmd `cmd:"" help:"Show crawler health"`
	Sync   SyncCmd   `cmd:"" help:"Run crawls"`
	Search SearchCmd `cmd:"" help:"Search crawler archives"`
	Who    WhoCmd    `cmd:"" help:"Resolve a person or sender identity"`
	Open   OpenCmd   `cmd:"" help:"Open a crawler ref"`
	Doctor DoctorCmd `cmd:"" help:"Run crawler diagnostics"`
}

type Runtime struct {
	ctx     context.Context
	stdout  io.Writer
	stderr  io.Writer
	root    *CLI
	appsDir string
	now     func() time.Time
}

type StatusCmd struct {
	Source string `arg:"" optional:"" help:"Source id"`
}

type DoctorCmd struct {
	Source string `arg:"" optional:"" help:"Source id"`
}

// helpShown unwinds the stack when kong renders help, so help works
// the same from the binary and from tests without exiting the process.
type helpShown struct{}

func Execute(args []string, stdout, stderr io.Writer) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(helpShown); ok {
				err = nil
				return
			}
			panic(recovered)
		}
	}()
	var root CLI
	parser, err := kong.New(&root,
		kong.Name("trawl"),
		kong.Description(`Search your own life: every installed crawler archives one source (iMessage, Telegram, WhatsApp, Gmail, Calendar, …) and trawl is the one door to all of them.

Sources go by id or surface name: imsgcrawl/imessage, telecrawl/telegram, wacrawl/whatsapp, gogcrawl/gmail, calcrawl/calendar — trawl status lists yours.

Examples:
  trawl status                          # every source: state, freshness, counts
  trawl search "boat trip"              # all sources, newest first
  trawl search imessage falafel         # one source, no quotes needed
  trawl open imsgcrawl:msg/8842         # expand a ref search returned
  trawl search falafel --json           # structured output; agents, prefer this`),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Exit(func(int) { panic(helpShown{}) }),
		kong.Vars{"version": Version},
	)
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(stdout, Version)
		return err
	}
	args = rewriteHelp(normalizeGlobalFlags(args))
	if err := unknownCommand(args); err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return usageErr{err}
	}
	runtime := &Runtime{
		ctx:     context.Background(),
		stdout:  stdout,
		stderr:  stderr,
		root:    &root,
		appsDir: defaultAppsDir(),
		now:     time.Now,
	}
	kctx.Bind(runtime)
	if err := kctx.Run(runtime); err != nil {
		return err
	}
	return nil
}

func (c *StatusCmd) Run(r *Runtime) error {
	sources, err := r.selectedSources(c.Source)
	if err != nil {
		return err
	}
	results := collectStatus(r.ctx, sources)
	if r.root.JSON {
		statuses := make([]StatusEnvelope, 0, len(results))
		for _, result := range results {
			statuses = append(statuses, result.Status)
		}
		if err := writeJSON(r.stdout, statuses); err != nil {
			return err
		}
		return statusExit(results)
	}
	if c.Source == "" {
		if err := renderStatusTable(r.stdout, results, r.now()); err != nil {
			return err
		}
	} else if len(results) == 1 {
		if err := renderStatusDetail(r.stdout, results[0], r.now()); err != nil {
			return err
		}
	}
	r.reportStatusFailures(results)
	return statusExit(results)
}

func (c *DoctorCmd) Run(r *Runtime) error {
	sources, err := r.selectedSources(c.Source)
	if err != nil {
		return err
	}
	results := collectDoctor(r.ctx, sources)
	if r.root.JSON {
		if err := writeJSON(r.stdout, results); err != nil {
			return err
		}
		r.reportDoctorFailures(results)
		return doctorExit(results)
	}
	if err := renderDoctor(r.stdout, results); err != nil {
		return err
	}
	return doctorExit(results)
}

func (r *Runtime) selectedSources(source string) ([]Source, error) {
	sources := discoverCrawlers(r.ctx, r.appsDir)
	if source == "" {
		return sources, nil
	}
	selected, ok := findSource(sources, source)
	if ok {
		return []Source{selected}, nil
	}
	return nil, r.writeSourceNotFound(source)
}

func collectStatus(ctx context.Context, sources []Source) []StatusResult {
	results := make([]StatusResult, 0, len(sources))
	for _, source := range sources {
		status := StatusEnvelope{}
		if source.MetadataErr != nil {
			status = errorStatus(source, fmt.Sprintf("metadata failed — run: trawl doctor %s", source.ID))
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		data, err := runCrawlerJSON(ctx, source.Path, "status")
		if err != nil {
			status = errorStatus(source, fmt.Sprintf("status failed — run: trawl doctor %s", source.ID))
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		if err := decodeContractJSON(data, &status); err != nil {
			status = errorStatus(source, fmt.Sprintf("status failed — run: trawl doctor %s", source.ID))
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		results = append(results, StatusResult{Source: source, Status: normalizeStatus(source, status)})
	}
	return results
}

func collectDoctor(ctx context.Context, sources []Source) []DoctorResult {
	results := make([]DoctorResult, 0, len(sources))
	for _, source := range sources {
		checks := []DoctorCheck{}
		if source.MetadataErr != nil {
			checks = append(checks, DoctorCheck{
				ID:      "metadata",
				State:   "fail",
				Message: "metadata --json did not return a valid crawler manifest",
				Remedy:  "fix the crawler so metadata --json returns a non-empty id",
			})
		}
		data, err := runCrawlerJSON(ctx, source.Path, "doctor")
		if err != nil {
			checks = append(checks, doctorCommandFailed(source))
			results = append(results, DoctorResult{Source: source.ID, Checks: checks})
			continue
		}
		var envelope DoctorEnvelope
		if err := decodeContractJSON(data, &envelope); err != nil {
			checks = append(checks, doctorCommandFailed(source))
			results = append(results, DoctorResult{Source: source.ID, Checks: checks})
			continue
		}
		checks = append(checks, normalizeChecks(envelope.Checks)...)
		results = append(results, DoctorResult{Source: source.ID, Checks: checks})
	}
	return results
}

func normalizeChecks(checks []DoctorCheck) []DoctorCheck {
	out := make([]DoctorCheck, 0, len(checks))
	for _, check := range checks {
		if check.ID == "" {
			check.ID = "doctor"
		}
		if check.State == "" {
			check.State = "fail"
		}
		out = append(out, check)
	}
	return out
}

func doctorCommandFailed(source Source) DoctorCheck {
	return DoctorCheck{
		ID:      "doctor",
		State:   "fail",
		Message: "doctor --json did not return the contract JSON",
		Remedy:  fmt.Sprintf("fix %s so doctor --json emits diagnostic checks", source.Binary),
	}
}

func statusExit(results []StatusResult) error {
	failures := 0
	successes := 0
	for _, result := range results {
		if statusFailed(result.Status) {
			failures++
			continue
		}
		successes++
	}
	if failures == 0 {
		return nil
	}
	if successes > 0 {
		return exitErr{code: 3}
	}
	return exitErr{code: 1}
}

func doctorExit(results []DoctorResult) error {
	for _, result := range results {
		for _, check := range result.Checks {
			if checkFailed(check) {
				return exitErr{code: 3}
			}
		}
	}
	return nil
}

func (r *Runtime) reportStatusFailures(results []StatusResult) {
	for _, result := range results {
		if !statusFailed(result.Status) {
			continue
		}
		_, _ = fmt.Fprintf(r.stderr, "%s failed. Remedy: run: trawl doctor %s\n", result.Source.ID, result.Source.ID)
	}
}

func (r *Runtime) reportDoctorFailures(results []DoctorResult) {
	for _, result := range results {
		for _, check := range result.Checks {
			if !checkFailed(check) {
				continue
			}
			remedy := firstNonEmpty(check.Remedy, fmt.Sprintf("run: trawl doctor %s", result.Source))
			_, _ = fmt.Fprintf(r.stderr, "%s %s failed. Remedy: %s\n", result.Source, check.ID, remedy)
		}
	}
}

func (r *Runtime) writeError(code, message, remedy string) error {
	if r.root.JSON {
		_ = writeJSON(r.stdout, ErrorEnvelope{Error: ErrorBody{
			Code:    code,
			Message: message,
			Remedy:  remedy,
		}})
	} else {
		_, _ = fmt.Fprintf(r.stderr, "%s Remedy: %s\n", message, remedy)
	}
	return exitErr{code: 1}
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	return encoder.Encode(value)
}

func normalizeGlobalFlags(args []string) []string {
	var globals []string
	var rest []string
	for _, arg := range args {
		if arg == "--json" {
			globals = append(globals, arg)
			continue
		}
		rest = append(rest, arg)
	}
	return append(globals, rest...)
}

// rewriteHelp keeps `trawl` and `trawl help [command]` working the way
// people type them: both become the --help kong already renders.
func rewriteHelp(args []string) []string {
	var flags, rest []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			continue
		}
		rest = append(rest, arg)
	}
	if len(rest) == 0 && len(flags) == 0 {
		return []string{"--help"}
	}
	if len(rest) > 0 && rest[0] == "help" {
		return append(rest[1:], "--help")
	}
	return args
}

func unknownCommand(args []string) error {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "status", "sync", "search", "who", "open", "doctor", "help":
			return nil
		default:
			return usageErr{fmt.Errorf("unknown command %q — commands are status, sync, search, who, open, doctor; run: trawl --help", arg)}
		}
	}
	return nil
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit exitErr
	if errors.As(err, &exit) {
		return exit.code
	}
	var usage usageErr
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

func ShouldPrintError(err error) bool {
	var exit exitErr
	return err != nil && !errors.As(err, &exit)
}

type exitErr struct {
	code int
}

func (e exitErr) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

type usageErr struct {
	error
}
