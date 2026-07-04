package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	ckoutput "github.com/openclaw/crawlkit/output"
)

var Version = "dev"

type CLI struct {
	JSON        bool             `name:"json" help:"Write JSON to stdout"`
	Verbose     int              `short:"v" name:"verbose" type:"counter" help:"Stream diagnostics to stderr; use -vv for debug detail"`
	VersionFlag kong.VersionFlag `name:"version" help:"Print version and exit"`

	Status    StatusCmd    `cmd:"" help:"Show crawler health"`
	Sync      SyncCmd      `cmd:"" help:"Run crawls"`
	Search    SearchCmd    `cmd:"" help:"Search crawler archives"`
	Summaries SummariesCmd `cmd:"" help:"List or read precomputed archive summaries"`
	Who       WhoCmd       `cmd:"" help:"Resolve a person or sender identity"`
	Open      OpenCmd      `cmd:"" help:"Open a crawler ref"`
	Doctor    DoctorCmd    `cmd:"" help:"Run crawler diagnostics"`
}

type Runtime struct {
	ctx      context.Context
	stdout   io.Writer
	stderr   io.Writer
	stderrMu sync.Mutex
	root     *CLI
	appsDir  string
	now      func() time.Time
	timeout  time.Duration
	log      *logRun
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
	return execute(args, stdout, stderr, crawlerCommandTimeout)
}

// execute carries the per-source subprocess deadline so tests can drive
// the real timeout path against a slow crawler without a 30s wait. It is
// the same seam as Runtime.now; production always passes the const.
func execute(args []string, stdout, stderr io.Writer, timeout time.Duration) (err error) {
	jsonOut := hasJSONFlag(args)
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
  trawl summaries                       # precomputed answers: subscriptions, possessions, spending
  trawl open imsgcrawl:msg/8842         # expand a ref search returned
  trawl search falafel --json           # structured output; agents, prefer this`),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Help(trawlHelpPrinter),
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
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, usageErr{err})
	}
	runtime := &Runtime{
		ctx:     context.Background(),
		stdout:  stdout,
		stderr:  stderr,
		root:    &root,
		appsDir: defaultAppsDir(),
		now:     time.Now,
		timeout: timeout,
	}
	if err := runtime.startLogRun(commandName(args)); err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, root.JSON, err)
	}
	defer func() {
		err = runtime.finishLogRun(err)
	}()
	kctx.Bind(runtime)
	if err := kctx.Run(runtime); err != nil {
		var exit exitErr
		if errors.As(err, &exit) {
			return err
		}
		return ckoutput.WriteJSONErrorIfNeeded(stdout, root.JSON, err)
	}
	return nil
}

func (c *StatusCmd) Run(r *Runtime) error {
	sources, err := r.selectedSources(c.Source)
	if err != nil {
		return err
	}
	results := collectStatus(r, sources)
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
	results := collectDoctor(r, sources)
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

func collectStatus(r *Runtime, sources []Source) []StatusResult {
	results := make([]StatusResult, 0, len(sources))
	for _, source := range sources {
		status := StatusEnvelope{}
		if source.MetadataErr != nil {
			started := r.logSourceStart(source, "status")
			r.logSourceDone(source, "status", started, source.MetadataErr)
			status = errorStatus(source, "the crawler did not identify itself")
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		started := r.logSourceStart(source, "status")
		data, err := r.runSourceJSONVerb(source, "status")
		if err != nil {
			r.logSourceDone(source, "status", started, err)
			status = errorStatus(source, "the crawler did not report its status")
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		if err := decodeContractJSON(data, &status); err != nil {
			r.logSourceDone(source, "status", started, err)
			status = errorStatus(source, "the crawler did not report its status")
			results = append(results, StatusResult{Source: source, Status: status})
			continue
		}
		r.logSourceDone(source, "status", started, nil)
		results = append(results, StatusResult{Source: source, Status: normalizeStatus(source, status)})
	}
	return results
}

func collectDoctor(r *Runtime, sources []Source) []DoctorResult {
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
		started := r.logSourceStart(source, "doctor")
		data, err := r.runSourceJSONVerb(source, "doctor")
		if err != nil {
			r.logSourceDone(source, "doctor", started, err)
			checks = append(checks, doctorCommandFailed(source))
			results = append(results, DoctorResult{Source: source.ID, Checks: checks, sourceInfo: source})
			continue
		}
		var envelope DoctorEnvelope
		if err := decodeContractJSON(data, &envelope); err != nil {
			r.logSourceDone(source, "doctor", started, err)
			checks = append(checks, doctorCommandFailed(source))
			results = append(results, DoctorResult{Source: source.ID, Checks: checks, sourceInfo: source})
			continue
		}
		r.logSourceDone(source, "doctor", started, nil, "checks="+strconv.Itoa(len(envelope.Checks)))
		checks = append(checks, normalizeChecks(envelope.Checks)...)
		results = append(results, DoctorResult{Source: source.ID, Checks: checks, sourceInfo: source})
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
		_, _ = fmt.Fprintf(r.stderr, "%s status failed.\n", result.Source.ID)
		_, _ = fmt.Fprintf(r.stderr, "  Remedy: run: trawl doctor %s\n", result.Source.ID)
	}
}

func (r *Runtime) reportDoctorFailures(results []DoctorResult) {
	for _, result := range results {
		for _, check := range result.Checks {
			if !checkFailed(check) {
				continue
			}
			_, _ = fmt.Fprintf(r.stderr, "%s %s failed.\n", result.Source, humanLabel(check.ID))
			_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", firstNonEmpty(check.Remedy, fmt.Sprintf("run: trawl doctor %s", result.Source)))
		}
	}
}

func (r *Runtime) writeError(code, message, remedy string) error {
	if r.root.JSON {
		_ = ckoutput.WriteError(r.stdout, ckoutput.ErrorBody{
			Code:    code,
			Message: message,
			Remedy:  remedy,
		})
	} else {
		_, _ = fmt.Fprintf(r.stderr, "%s\n", message)
		_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", remedy)
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
		if isGlobalFlag(arg) {
			globals = append(globals, arg)
			continue
		}
		rest = append(rest, arg)
	}
	return append(globals, rest...)
}

func isGlobalFlag(arg string) bool {
	return arg == "--json" ||
		arg == "-v" ||
		arg == "-vv" ||
		arg == "--verbose" ||
		strings.HasPrefix(arg, "--verbose=")
}

func hasJSONFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
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
		case "status", "sync", "search", "summaries", "who", "open", "doctor", "help":
			return nil
		default:
			return usageErr{fmt.Errorf("unknown command %q — commands are status, sync, search, summaries, who, open, doctor; run: trawl --help", arg)}
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
	return err != nil && !errors.As(err, &exit) && !ckoutput.IsRendered(err)
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

func (e usageErr) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    "usage",
		Message: e.Error(),
		Remedy:  "run trawl --help",
	}
}
