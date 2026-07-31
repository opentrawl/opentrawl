package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckrender "github.com/opentrawl/opentrawl/trawlkit/render"
)

var Version = "dev"

type CLI struct {
	Verbose     int              `short:"v" name:"verbose" type:"counter" help:"Show detailed progress on stderr; use -vv for debug detail"`
	VersionFlag kong.VersionFlag `name:"version" help:"Print version and exit"`

	Status        StatusCmd        `cmd:"" help:"${status_help}"`
	Sync          SyncCmd          `cmd:"" help:"Update trawlers"`
	Search        SearchCmd        `cmd:"" help:"Find anything in your archive"`
	Who           WhoCmd           `cmd:"" help:"Find a person"`
	Conversations ConversationsCmd `cmd:"" help:"List conversations"`
	Messages      MessagesCmd      `cmd:"" help:"List messages in one conversation"`
	Open          OpenCmd          `cmd:"" help:"Open a result"`
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
	stateRoot         string
}

type StatusCmd struct {
	Trawler string `arg:"" optional:"" name:"trawler" help:"Trawler name"`
}

// helpShown unwinds the stack when kong renders help, so help works
// the same from the binary and from tests without exiting the process.
type helpShown struct{}

func Execute(args []string, stdout, stderr io.Writer) (err error) {
	return ExecuteWithTrawlInvocationDisplay(args, stdout, stderr, "./trawl")
}

func ExecuteWithTrawlInvocationDisplay(args []string, stdout, stderr io.Writer, trawlInvocationDisplay string) (err error) {
	return execute(
		args,
		ckrender.WithTrawlInvocationDisplay(stdout, trawlInvocationDisplay),
		ckrender.WithTrawlInvocationDisplay(stderr, trawlInvocationDisplay),
		crawlerCommandTimeout,
	)
}

// execute carries the per-trawler read deadline so callers can drive the
// real timeout path against a slow trawler without a 30s wait. It is
// the same seam as Runtime.now; production always passes the const.
func execute(args []string, stdout, stderr io.Writer, timeout time.Duration) (err error) {
	return executeWithCanonicalObserver(args, stdout, stderr, timeout, nil)
}

func executeWithCanonicalObserver(args []string, stdout, stderr io.Writer, timeout time.Duration, observer canonicalConsumerObserver) (err error) {
	stateRoot, err := trawlkit.ResolveStateRoot("")
	if err != nil {
		return err
	}
	if isAppWireCommand(args) {
		return executeAppWire(args, stdout, stderr, timeout, stateRoot)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(helpShown); ok {
				err = nil
				return
			}
			panic(recovered)
		}
	}()
	// Bare `trawl` has its own short front door and does not enter Kong.
	if len(args) == 0 {
		return writeFrontDoor(stdout)
	}
	root := CLI{Search: SearchCmd{trawlInvocationDisplay: ckrender.TrawlInvocationDisplay(stdout)}}
	parser, err := kong.New(&root,
		kong.Name(ckrender.TrawlInvocationDisplay(stdout)),
		kong.Description(""),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Help(trawlHelpPrinter),
		kong.Exit(func(int) { panic(helpShown{}) }),
		kong.Vars{"version": Version, "status_help": statusCommandHelpDescription},
	)
	if err != nil {
		return err
	}
	parser.Model.HelpFlag.Help = "Show help"
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(stdout, Version)
		return err
	}
	// A first token that is not a built-in command opens a trawler namespace.
	if token, ok := namespaceCandidate(args); ok {
		runtime := &Runtime{
			ctx:               context.Background(),
			stdout:            stdout,
			stderr:            stderr,
			root:              namespaceRoot(args),
			now:               time.Now,
			timeout:           timeout,
			canonicalObserver: observer,
			stateRoot:         stateRoot,
		}
		_ = runtime.startLogRun("namespace")
		defer func() {
			err = runtime.finishLogRun(err)
		}()
		return runtime.dispatchNamespace(args, token)
	}
	args = rewriteHelp(normalizeGlobalFlags(args))
	if len(args) == 1 {
		switch args[0] {
		case "messages":
			return usageErr{ckoutput.HumanFacingErrorMessage("Messages needs a conversation link.")}
		case "open":
			return usageErr{ckoutput.HumanFacingErrorMessage("Open needs a link.")}
		case "who":
			return usageErr{ckoutput.HumanFacingErrorMessage("Who needs a name.")}
		}
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return usageErr{errors.New(humanUsageErrorMessage(err.Error()))}
	}
	runtime := &Runtime{
		ctx:               context.Background(),
		stdout:            stdout,
		stderr:            stderr,
		root:              &root,
		now:               time.Now,
		timeout:           timeout,
		canonicalObserver: observer,
		stateRoot:         stateRoot,
	}
	_ = runtime.startLogRun(commandName(args))
	defer func() {
		err = runtime.finishLogRun(err)
	}()
	kctx.Bind(runtime)
	if err := kctx.Run(runtime); err != nil {
		var exit exitErr
		if errors.As(err, &exit) {
			return err
		}
		return err
	}
	return nil
}

func (c *StatusCmd) Run(r *Runtime) error {
	installedTrawlers, err := r.selectedTrawlers(c.Trawler)
	if err != nil {
		return err
	}
	if len(installedTrawlers) == 0 {
		_, err := fmt.Fprintln(r.stdout, "No trawlers found.")
		return err
	}
	response := r.canonicalStatus(installedTrawlers)
	if err := ckrender.WriteFederatedTrawlerStatusOperation(r.stdout, response); err != nil {
		return err
	}
	r.reportStatusFederationOutcomes(
		response.GetOperationFailures(),
		response.GetTrawlersSkippedFromOperation(),
	)
	return outcomeExit(response.GetOutcome())
}

func (r *Runtime) selectedTrawlers(trawlerName string) ([]InstalledTrawler, error) {
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	if trawlerName == "" {
		return installedTrawlers, nil
	}
	selected, ok := findInstalledTrawler(installedTrawlers, trawlerName)
	if ok {
		return []InstalledTrawler{selected}, nil
	}
	return nil, r.writeTrawlerNotFound(trawlerName)
}

func (r *Runtime) writeError(message string) error {
	_, _ = fmt.Fprintf(r.stderr, "%s\n", message)
	return exitErr{code: 1}
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
	return arg == "-v" ||
		arg == "-vv" ||
		arg == "--verbose" ||
		strings.HasPrefix(arg, "--verbose=")
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

func unknownCommandErr(name string) error {
	name = strings.Join(strings.Fields(name), " ")
	return usageErr{ckoutput.HumanFacingErrorMessage(fmt.Sprintf("Unknown command %q.", name))}
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

type humanFacingUsageErrorMessage = ckoutput.HumanFacingErrorMessage

func (e usageErr) Error() string {
	if e.error == nil {
		return "The command is not valid."
	}
	return humanUsageErrorMessage(e.error.Error())
}

func humanUsageErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	standardLibraryLimitDecodeError := strings.HasPrefix(message, "invalid value ") &&
		strings.Contains(message, " for flag -limit:") &&
		(strings.HasSuffix(message, ": parse error") || strings.HasSuffix(message, ": must be a whole number"))
	kongLimitDecodeError := strings.HasPrefix(message, "--limit: expected a valid ") &&
		strings.Contains(message, " bit int but got ")
	if standardLibraryLimitDecodeError || kongLimitDecodeError {
		return "--limit must be a whole number."
	}
	if optionName, parserFailure, found := strings.Cut(message, ": expected "); found &&
		strings.HasPrefix(optionName, "--") &&
		strings.HasSuffix(parserFailure, ` value but got "EOL" (<EOL>)`) {
		return fmt.Sprintf("%s needs a value.", optionName)
	}
	if strings.HasPrefix(message, "flag provided but not defined: -") {
		name := strings.TrimLeft(strings.TrimPrefix(message, "flag provided but not defined: "), "-")
		return fmt.Sprintf("Unknown option --%s.", name)
	}
	if strings.HasPrefix(message, "unknown flag ") {
		name := strings.TrimSpace(strings.TrimPrefix(message, "unknown flag "))
		if before, _, found := strings.Cut(name, ","); found {
			name = before
		}
		name = strings.Trim(strings.TrimSpace(name), "\"")
		return fmt.Sprintf("Unknown option --%s.", strings.TrimLeft(name, "-"))
	}
	if strings.HasPrefix(message, "unexpected argument") {
		return "The command has too many arguments."
	}
	return message
}

func (e usageErr) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    "usage",
		Message: e.Error(),
	}
}
