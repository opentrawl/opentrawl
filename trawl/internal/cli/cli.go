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
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
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
	ctx               context.Context
	stdout            io.Writer
	stderr            io.Writer
	stderrMu          sync.Mutex
	root              *CLI
	now               func() time.Time
	timeout           time.Duration
	log               *logRun
	canonicalObserver canonicalConsumerObserver
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

// execute carries the per-source read deadline so tests can drive the
// real timeout path against a slow crawler without a 30s wait. It is
// the same seam as Runtime.now; production always passes the const.
func execute(args []string, stdout, stderr io.Writer, timeout time.Duration) (err error) {
	return executeWithCanonicalObserver(args, stdout, stderr, timeout, nil)
}

func executeWithCanonicalObserver(args []string, stdout, stderr io.Writer, timeout time.Duration, observer canonicalConsumerObserver) (err error) {
	if isAppWireCommand(args) {
		return executeAppWire(args, stdout, stderr, timeout)
	}
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
	// Bare `trawl` (no flags, no arguments) is its own short front door,
	// split from the fuller `trawl --help` page. It renders the live Sources
	// block and three worked first steps, then returns — it never reaches
	// kong.
	if len(args) == 0 {
		return writeFrontDoor(stdout)
	}
	var root CLI
	parser, err := kong.New(&root,
		kong.Name("trawl"),
		kong.Description(trawlDescription()),
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
	// Progressive discovery: a first token that is not a built-in command
	// opens a crawler namespace (trawl <source> <verb>). This runs on the
	// raw args, before kong and flag normalization, so a source's own
	// flags reach the crawler verb untouched.
	if token, ok := namespaceCandidate(args); ok {
		runtime := &Runtime{
			ctx:               context.Background(),
			stdout:            stdout,
			stderr:            stderr,
			root:              namespaceRoot(args),
			now:               time.Now,
			timeout:           timeout,
			canonicalObserver: observer,
		}
		if err := runtime.startLogRun("namespace"); err != nil {
			return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, err)
		}
		defer func() {
			err = runtime.finishLogRun(err)
		}()
		return runtime.dispatchNamespace(args, token)
	}
	args = rewriteHelp(normalizeGlobalFlags(args))
	kctx, err := parser.Parse(args)
	if err != nil {
		return ckoutput.WriteJSONErrorIfNeeded(stdout, jsonOut, usageErr{err})
	}
	runtime := &Runtime{
		ctx:               context.Background(),
		stdout:            stdout,
		stderr:            stderr,
		root:              &root,
		now:               time.Now,
		timeout:           timeout,
		canonicalObserver: observer,
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
	var sources []Source
	if r.root.JSON && c.Source != "" {
		source, found := findSource(discoverCrawlers(r.ctx), c.Source)
		if !found {
			response := statusSourceNotFoundResponse(c.Source)
			if err := writeCanonicalJSON(r.stdout, response); err != nil {
				return err
			}
			return outcomeExit(response.GetOutcome())
		}
		sources = []Source{source}
	} else {
		var err error
		sources, err = r.selectedSources(c.Source)
		if err != nil {
			return err
		}
	}
	response := r.canonicalStatus(sources)
	if r.root.JSON {
		if err := writeCanonicalJSON(r.stdout, response); err != nil {
			return err
		}
		return outcomeExit(response.GetOutcome())
	}
	results, err := statusResultsFromResponse(sources, response)
	if err != nil {
		return err
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
	r.reportFederationOutcomes(response.GetFailures(), response.GetSkippedSources(), "status")
	return outcomeExit(response.GetOutcome())
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
	sources := discoverCrawlers(r.ctx)
	if source == "" {
		return sources, nil
	}
	selected, ok := findSource(sources, source)
	if ok {
		return []Source{selected}, nil
	}
	return nil, r.writeSourceNotFound(source)
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
		result, err := r.doctorSource(source)
		if err != nil {
			r.logSourceDone(source, "doctor", started, err)
			checks = append(checks, doctorCommandFailed(source))
			results = append(results, DoctorResult{Source: source.ID, Checks: checks, sourceInfo: source})
			continue
		}
		r.logSourceDone(source, "doctor", started, nil, "checks="+strconv.Itoa(len(result.Checks)))
		checks = append(checks, result.Checks...)
		results = append(results, DoctorResult{Source: source.ID, Checks: checks, sourceInfo: source})
	}
	return results
}

func statusEnvelopeFromControl(source Source, status *control.Status) (StatusEnvelope, error) {
	if status == nil {
		return errorStatus(source, "the crawler did not report its status"), nil
	}
	data, err := json.Marshal(status)
	if err != nil {
		return StatusEnvelope{}, err
	}
	var out StatusEnvelope
	if err := decodeContractJSON(data, &out); err != nil {
		return StatusEnvelope{}, err
	}
	return normalizeStatus(source, out), nil
}

func (r *Runtime) doctorSource(source Source) (DoctorResult, error) {
	var doctor *trawlkit.Doctor
	err := r.withSourceRequest(source, "doctor", sourceStoreOptional, ckoutput.JSON, io.Discard, func(ctx context.Context, req *trawlkit.Request) error {
		var runErr error
		doctor, runErr = source.Crawler.Doctor(ctx, req)
		return runErr
	})
	if err != nil {
		return DoctorResult{}, err
	}
	checks := []DoctorCheck{}
	if doctor != nil {
		for _, check := range doctor.Checks {
			checks = append(checks, DoctorCheck{
				ID:      check.ID,
				State:   check.State,
				Message: check.Message,
				Remedy:  check.Remedy,
			})
		}
	}
	return DoctorResult{Source: source.ID, Checks: normalizeChecks(checks), sourceInfo: source}, nil
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

func (r *Runtime) reportDoctorFailures(results []DoctorResult) {
	for _, result := range results {
		for _, check := range result.Checks {
			if !checkFailed(check) {
				continue
			}
			_, _ = fmt.Fprintf(r.stderr, "%s %s failed.\n", doctorSourceName(result), humanLabel(check.ID))
			_, _ = fmt.Fprintf(r.stderr, "  Remedy: %s\n", firstNonEmpty(check.Remedy, fmt.Sprintf("run trawl doctor %s", doctorSourceCommandToken(result))))
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

const trawlIntro = `Search your own life: every installed crawler archives one source (iMessage, Telegram, WhatsApp, Gmail, Calendar, …) and trawl is the one door to all of them.`

const trawlOutro = `The commands below run across every source. Each source is also its own namespace: 'trawl <source>' lists that crawler's verbs, and 'trawl <source> <verb>' runs one.

Examples:
  trawl status                          # every source: state, freshness, counts
  trawl search "boat trip"              # all sources, newest first
  trawl search imessage falafel         # one source, no quotes needed
  trawl imessage                        # list what the iMessage crawler can do
  trawl imessage chats                  # run one source's own verb
  trawl summaries                       # precomputed answers: subscriptions, possessions, spending
  trawl open imessage:msg/8842          # expand a ref search returned
  trawl search falafel --json           # structured output; agents, prefer this`

// trawlDescription is the framing text at the top of `trawl --help`: the
// tagline and the cross-source examples. The live Sources block and the
// agents appendix are appended by trawlHelpPrinter (only for root help), so
// their columns survive without kong re-wrapping them into the description.
func trawlDescription() string {
	return trawlIntro + "\n\n" + trawlOutro
}

// unknownCommandErr names both token spaces a first argument can be — a
// built-in command or an installed source — and lists the sources found,
// so a mistyped source name reveals the namespace instead of hiding it.
func unknownCommandErr(name string, sources []string) error {
	message := fmt.Sprintf("unknown command %q - commands are status, sync, search, summaries, who, open, doctor", name)
	if len(sources) > 0 {
		message += "; sources are " + strings.Join(sources, ", ")
	}
	message += "; run trawl --help"
	return usageErr{errors.New(message)}
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
