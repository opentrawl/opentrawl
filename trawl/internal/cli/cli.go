package cli

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

var Version = "dev"

type CLI struct {
	Verbose     int                     `short:"v" name:"verbose" type:"counter" help:"Show detailed progress; use -vv for debug detail"`
	ShowVersion trawlVersionDisplayFlag `name:"version" help:"Show the version"`

	Status        StatusCmd        `cmd:"" help:"Show archive contents, last update and whether it works"`
	Update        UpdateCmd        `cmd:"" help:"Update local archives"`
	Search        SearchCmd        `cmd:"" help:"Search local archives"`
	Who           WhoCmd           `cmd:"" help:"Find a person"`
	Conversations ConversationsCmd `cmd:"" help:"List conversations"`
	Messages      MessagesCmd      `cmd:"" help:"List messages in a conversation"`
	Open          OpenCmd          `cmd:"" help:"Open an archive item"`
}

// Runtime carries one command run. It has no writer: every word a command
// shows goes through output, the shared output library.
type Runtime struct {
	ctx                        context.Context
	output                     render.TrawlCommandOutput
	appWireProtobufFrameStream io.Writer
	root                       *CLI
	now                        func() time.Time
	timeout                    time.Duration
	log                        *logRun
	canonicalObserver          canonicalConsumerObserver
	stateRoot                  string
}

type StatusCmd struct {
	Trawler string `arg:"" optional:"" name:"trawler" help:"Trawler name"`
}

// helpShown unwinds the stack when kong renders help, so help works
// the same from the binary and from tests without exiting the process.
type helpShown struct{}

type trawlVersionDisplayFlag bool

func (trawlVersionDisplayFlag) BeforeReset(output render.TrawlCommandOutput, parsingContext *kong.Context, parsedFlagPath *kong.Path) error {
	if !parsingContext.FlagValue(parsedFlagPath.Flag).(trawlVersionDisplayFlag) {
		return nil
	}
	if err := output.WriteTrawlVersion(Version); err != nil {
		return err
	}
	panic(helpShown{})
}

// ExecuteWithTrawlInvocationDisplay runs one trawl command and returns the
// process exit code. Every failure is written by the output library here.
func ExecuteWithTrawlInvocationDisplay(args []string, stdout, stderr io.Writer, trawlInvocationDisplay string) int {
	output := render.NewTrawlCommandOutput(stdout, stderr, trawlInvocationDisplay)
	err := execute(args, output, stdout, registeredTrawlerCommandTimeout, nil)
	if err == nil {
		return 0
	}
	var outcome commandOutcomeAlreadyShown
	if errors.As(err, &outcome) {
		return outcome.processExitCode
	}
	if failure, worded := render.AsCommandFailure(err); worded &&
		failure.CommandFailureKind() == render.CommandFailureKindCommandIsWrong {
		err = failure.WithHelpCommand(trawlCommandArgumentsShowingHelpForCommandLine(args))
	}
	_ = output.WriteCommandFailure(err)
	return render.ProcessExitCodeForCommandFailure(err)
}

// trawlCommandArgumentsShowingHelpForCommandLine finds the help for what the
// reader was typing: trawl search --help, trawl calendar events --help,
// trawl calendar --help, or trawl --help when the first word is unknown.
func trawlCommandArgumentsShowingHelpForCommandLine(args []string) []string {
	firstWord := firstNonFlag(args)
	if firstWord == "" || firstWord == "help" {
		return []string{"--help"}
	}
	if reservedCommand(firstWord) {
		return []string{firstWord, "--help"}
	}
	trawler, isTrawler := findInstalledTrawler(discoverInstalledTrawlers(context.Background()), firstWord)
	if !isTrawler {
		return []string{"--help"}
	}
	if command, isTrawlerCommand := namespaceMatch(trawler, argsAfter(args, firstWord)); isTrawlerCommand {
		return append(append([]string{firstWord}, fixedCommandTokens(command)...), "--help")
	}
	return []string{firstWord, "--help"}
}

// execute carries the per-trawler read deadline so callers can drive the
// real timeout path against a slow trawler without a 30s wait. It is
// the same seam as Runtime.now; production always passes the const.
func execute(
	args []string,
	output render.TrawlCommandOutput,
	appWireProtobufFrameStream io.Writer,
	timeout time.Duration,
	observer canonicalConsumerObserver,
) (err error) {
	stateRoot, err := trawlkit.ResolveStateRoot("")
	if err != nil {
		return err
	}
	if isAppWireCommand(args) {
		return executeAppWire(args, output, appWireProtobufFrameStream, timeout, stateRoot)
	}
	for _, argument := range args {
		if argument == "--" {
			break
		}
		if argument == "--version" {
			return output.WriteTrawlVersion(Version)
		}
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
	root := CLI{}
	parser, err := kong.New(&root,
		kong.Name("trawl"),
		kong.Description(""),
		kong.Writers(io.Discard, io.Discard),
		kong.Bind(output),
		kong.Help(trawlHelpPrinter(output)),
		kong.Exit(func(int) { panic(helpShown{}) }),
		kong.Vars{
			"on_or_after_date_option_description":  render.OnOrAfterDateOptionDescription,
			"on_or_before_date_option_description": render.OnOrBeforeDateOptionDescription,
		},
	)
	if err != nil {
		return err
	}
	parser.Model.HelpFlag.Help = "Show help"
	if len(args) == 0 {
		return output.WriteTrawlOverview(trawlOverviewHelp(parser.Model))
	}
	// A first token that is not a built-in command opens a trawler namespace.
	if token, ok := namespaceCandidate(args); ok {
		runtime := &Runtime{
			ctx:               context.Background(),
			output:            output,
			root:              namespaceRoot(args),
			now:               time.Now,
			timeout:           timeout,
			canonicalObserver: observer,
			stateRoot:         stateRoot,
		}
		defer func() {
			err = runtime.finishLogRun(err)
		}()
		return runtime.dispatchNamespace(args, token)
	}
	args = rewriteHelp(normalizeGlobalFlags(args))
	if helpTrawler, ok := trawlerNamedByHelpCommand(args); ok {
		runtime := &Runtime{ctx: context.Background(), output: output, root: &CLI{}, stateRoot: stateRoot}
		return runtime.dispatchNamespace([]string{helpTrawler}, helpTrawler)
	}
	if len(args) == 1 {
		switch args[0] {
		case "messages":
			return render.MessagesNeedsConversationLink()
		case "open":
			return render.OpenNeedsLink()
		case "who":
			return render.WhoNeedsName()
		}
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return commandFailureFromParserError(err)
	}
	runtime := &Runtime{
		ctx:               context.Background(),
		output:            output,
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
	return kctx.Run(runtime)
}

func (c *StatusCmd) Run(r *Runtime) error {
	installedTrawlers, err := r.selectedTrawlers(c.Trawler)
	if err != nil {
		return err
	}
	installedTrawlers, err = trawlersForSharedOperation(installedTrawlers,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, c.Trawler != "")
	if err != nil {
		return err
	}
	response := r.canonicalStatus(installedTrawlers)
	r.logFederationOutcomes(response.GetOperationFailures(), response.GetTrawlersSkippedFromOperation())
	if err := r.output.WriteStatus(response); err != nil {
		return err
	}
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
	return nil, render.UnknownTrawler(trawlerName, installedTrawlerCommandNames(installedTrawlers))
}

func normalizeGlobalFlags(args []string) []string {
	var globals []string
	var rest []string
	for argumentIndex, arg := range args {
		if arg == "--" {
			rest = append(rest, args[argumentIndex:]...)
			break
		}
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

// rewriteHelp keeps `trawl help [command]` working the way people type it:
// it becomes the --help kong already renders.
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

// trawlerNamedByHelpCommand reads `trawl help imessage` as the iMessage
// command list.
func trawlerNamedByHelpCommand(args []string) (string, bool) {
	if len(args) != 2 || args[1] != "--help" || reservedCommand(args[0]) || strings.HasPrefix(args[0], "-") {
		return "", false
	}
	return args[0], true
}

// commandOutcomeAlreadyShown ends a command whose output already said what
// happened, such as a search where one trawler failed, with its exit code.
type commandOutcomeAlreadyShown struct {
	processExitCode int
}

func (outcome commandOutcomeAlreadyShown) Error() string {
	return "command finished with exit code " + strconv.Itoa(outcome.processExitCode)
}

// commandFailureFromParserError turns kong's explanation of a bad command
// line into one of the library's sentences.
func commandFailureFromParserError(parserError error) render.CommandFailure {
	message := strings.TrimSpace(parserError.Error())
	if strings.HasPrefix(message, "--limit: expected a valid ") && strings.Contains(message, " bit int but got ") {
		return render.LimitMustBeWholeNumber()
	}
	// kong reads "--limit -3" as --limit followed by the short flag -3.
	if strings.HasPrefix(message, `--limit: expected int value but got "-`) {
		return render.LimitMustBeAtLeastOne()
	}
	if optionName, parserFailure, found := strings.Cut(message, ": expected "); found &&
		strings.HasPrefix(optionName, "--") &&
		strings.HasSuffix(parserFailure, ` value but got "EOL" (<EOL>)`) {
		return render.OptionNeedsValue(optionName)
	}
	if strings.HasPrefix(message, "unknown flag ") {
		name := strings.TrimSpace(strings.TrimPrefix(message, "unknown flag "))
		if before, _, found := strings.Cut(name, ","); found {
			name = before
		}
		return render.UnknownOption(strings.Trim(strings.TrimSpace(name), "\""))
	}
	if strings.HasPrefix(message, "unexpected argument") {
		return render.CommandHasTooManyArguments()
	}
	return render.CommandLineRejectedByParser(message)
}
