package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// `./trawl <trawler>` opens one trawler's commands as a namespace.

// namespaceCandidate reports the first non-flag token that is not a built-in command.
func namespaceCandidate(args []string) (string, bool) {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if reservedCommand(arg) {
			return "", false
		}
		return arg, true
	}
	return "", false
}

func reservedCommand(name string) bool {
	switch name {
	case "status", "update", "search", "who", "conversations", "messages", "open", "help":
		return true
	default:
		return false
	}
}

// namespaceRoot reads the global flags off the raw args, since the
// namespace path runs before kong parses them.
func namespaceRoot(args []string) *CLI {
	return &CLI{Verbose: verboseLevel(args)}
}

func verboseLevel(args []string) int {
	level := 0
	for _, arg := range args {
		switch arg {
		case "-vv":
			level = 2
		case "-v", "--verbose":
			if level < 1 {
				level = 1
			}
		}
	}
	return level
}

func (r *Runtime) dispatchNamespace(args []string, token string) error {
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	trawler, ok := findInstalledTrawler(installedTrawlers, token)
	if !ok {
		return unknownCommandErr(token)
	}
	if trawler.TrawlerDiscoveryError != nil {
		r.logInfo(
			"trawler_discovery_failed",
			trawlerField(trawler)+" error="+logQuote(cklog.InternalErrorLogMessage(trawler.TrawlerDiscoveryError)),
		)
		description := ckoutput.ErrorDescriptionFor(trawler.TrawlerDiscoveryError)
		if errorDescriptionMeansArchiveUnavailable(description.Code) {
			r.writeTrawlerArchiveUnavailableError(trawlerHumanName(trawler))
		} else {
			_, _ = fmt.Fprintf(r.stderr, "The command did not complete for %s.\n", trawlerHumanName(trawler))
		}
		return exitErr{code: 1}
	}
	rest := argsAfter(args, token)
	if firstNonFlag(rest) == "" {
		return r.renderNamespace(trawler)
	}
	return r.runNamespaceCommand(trawler, token, rest)
}

func (r *Runtime) runNamespaceCommand(trawler InstalledTrawler, token string, rest []string) error {
	if firstNonFlag(rest) == "conversations" &&
		namespaceShowsSharedTrawlerOperation(
			trawler,
			federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
		) {
		return r.runNamespaceConversations(trawler, token, rest)
	}
	command, ok := namespaceMatch(trawler, rest)
	if containsArg(rest, "--help") || containsArg(rest, "-h") {
		if ok {
			return writeNamespaceCommandHelp(r.stdout, trawler, token, command)
		}
		return r.writeNamespaceCommandGroupHelp(trawler, token, leadingLiterals(rest))
	}
	if !ok {
		leading := leadingLiterals(rest)
		if len(leading) == 0 {
			// The first token is a trawler flag: the command came after its
			// flags. Name the shape, not the flag value.
			return usageErr{humanFacingUsageErrorMessage("The command must come before its options.")}
		}
		return usageErr{humanFacingUsageErrorMessage(fmt.Sprintf("Unknown %s command %q.", trawlerHumanName(trawler), strings.Join(leading, " ")))}
	}
	return r.runNamespaceTrawlerCommand(trawler, token, rest)
}

func (r *Runtime) runNamespaceTrawlerCommand(trawler InstalledTrawler, token string, arguments []string) error {
	argumentsWithSelectedTrawlerLocalConversationShortReference, globallyRoutableConversationLinkWasReplaced, err := replaceGloballyRoutableConversationLinkWithLocalShortReferenceForSelectedTrawler(
		arguments,
		trawler,
	)
	if err != nil {
		return err
	}
	var moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount []string
	if globallyRoutableConversationLinkWasReplaced {
		moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount = namespaceCommandArgumentsBeforeMaximumReturnedRowCount(
			token,
			arguments,
		)
	}
	return r.runDeclaredTrawlerCommand(
		trawler,
		token,
		argumentsWithSelectedTrawlerLocalConversationShortReference,
		moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount,
	)
}

func namespaceCommandArgumentsBeforeMaximumReturnedRowCount(
	registeredTrawlerCommandName string,
	arguments []string,
) []string {
	arguments = namespaceCommandArguments(arguments)
	argumentsBeforeMaximumReturnedRowCount := make([]string, 0, len(arguments)+1)
	argumentsBeforeMaximumReturnedRowCount = append(argumentsBeforeMaximumReturnedRowCount, registeredTrawlerCommandName)
	for argumentIndex := 0; argumentIndex < len(arguments); argumentIndex++ {
		argument := arguments[argumentIndex]
		switch {
		case argument == "--limit" || argument == "-limit":
			if argumentIndex+1 < len(arguments) {
				argumentIndex++
			}
		case strings.HasPrefix(argument, "--limit=") || strings.HasPrefix(argument, "-limit="):
			continue
		default:
			argumentsBeforeMaximumReturnedRowCount = append(argumentsBeforeMaximumReturnedRowCount, argument)
		}
	}
	return argumentsBeforeMaximumReturnedRowCount
}

func (r *Runtime) runDeclaredTrawlerCommand(
	trawler InstalledTrawler,
	token string,
	arguments []string,
	moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount []string,
) error {
	arguments = namespaceCommandArguments(arguments)
	commandName := firstNonFlag(arguments)
	started := r.logTrawlerStart(trawler, commandName)
	response, localShortReferencesByCanonicalRecordReference, renderContext, err := r.trawlerExecutor().ExecuteDeclaredTrawlerCommand(r.ctx, trawler.Trawler, arguments)
	r.logTrawlerDone(trawler, commandName, started, err)
	if err != nil {
		description := ckoutput.ErrorDescriptionFor(err)
		if description.Code == "usage" {
			_, _ = fmt.Fprintf(r.stderr, "%s\n", humanUsageErrorMessage(description.Message))
			return exitErr{code: 2}
		}
		if description.Code == "not_found" || description.Code == "ambiguous" || description.Code == "ambiguous_short_ref" {
			return r.writeError(description.Message)
		}
		if errorDescriptionMeansArchiveUnavailable(description.Code) {
			r.writeTrawlerArchiveUnavailableError(trawlerHumanName(trawler))
			return exitErr{code: 1}
		}
		if commandName == "debug" {
			return r.writeError(description.Message)
		}
		_, _ = fmt.Fprintf(r.stderr, "The command did not complete for %s.\n", trawlerHumanName(trawler))
		return exitErr{code: 1}
	}
	globallyRoutableTrawlLinksByCanonicalRecordReference, err := composeGloballyRoutableTrawlLinksByCanonicalArchiveRecordReferenceForRendering(
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		localShortReferencesByCanonicalRecordReference,
	)
	if err != nil {
		return err
	}
	if len(moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount) > 0 {
		renderContext = renderContext.WithMoreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount(
			moreTrawlerCommandArgumentsBeforeMaximumReturnedRowCount,
		)
	}
	return render.WriteTrawlerCommandResponse(r.stdout, response, globallyRoutableTrawlLinksByCanonicalRecordReference, renderContext)
}

func errorDescriptionMeansArchiveUnavailable(errorDescriptionCode string) bool {
	switch strings.ToLower(strings.TrimSpace(errorDescriptionCode)) {
	case "unavailable", "archive", "archive_missing", "archive_unreadable", "permission", "permission_denied":
		return true
	default:
		return false
	}
}

func composeGloballyRoutableTrawlLinksByCanonicalArchiveRecordReferenceForRendering(
	registeredTrawler *trawlkit.RegisteredTrawlerIdentity,
	localShortReferencesByCanonicalRecordReference []trawlkit.CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference,
) (render.GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference, error) {
	globallyRoutableTrawlLinksByCanonicalRecordReference := make(
		render.GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
		0,
		len(localShortReferencesByCanonicalRecordReference),
	)
	for _, references := range localShortReferencesByCanonicalRecordReference {
		trawlLink, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   registeredTrawler,
			LocalShortReference: references.LocalTrawlerShortReference,
		})
		if err != nil {
			return nil, err
		}
		globallyRoutableTrawlLinksByCanonicalRecordReference = append(
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			render.GloballyRoutableTrawlLinkForCanonicalArchiveRecordReference{
				CanonicalArchiveRecordReference: references.CanonicalArchiveRecordReference,
				GloballyRoutableTrawlLink:       trawlLink,
			},
		)
	}
	return globallyRoutableTrawlLinksByCanonicalRecordReference, nil
}

func namespaceCommandArguments(arguments []string) []string {
	values := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		if !isGlobalFlag(argument) {
			values = append(values, argument)
		}
	}
	return values
}

// renderNamespace lists the trawler's declared commands.
func (r *Runtime) renderNamespace(trawler InstalledTrawler) error {
	commands := namespaceCommandList(trawler)
	displayName := trawlerHumanName(trawler)
	if _, err := fmt.Fprintf(r.stdout, "%s\n", displayName); err != nil {
		return err
	}
	if overviewCommands := trawlerCommandNamesShownInBareTrawlOverview(trawler); len(overviewCommands) > 0 {
		if err := render.WriteTrawlCommandHint(
			r.stdout,
			fmt.Sprintf(
				"Start with: %s %s %s",
				render.TrawlInvocationDisplay(r.stdout),
				trawlerCommandToken(trawler),
				overviewCommands[0],
			),
		); err != nil {
			return err
		}
	}
	if len(commands) == 0 {
		if _, err := fmt.Fprintf(r.stdout, "\nSearch %s:\n", displayName); err != nil {
			return err
		}
		return render.WriteIndentedTrawlCommand(
			r.stdout,
			fmt.Sprintf(
				"%s search \"words\" --trawler %s",
				render.TrawlInvocationDisplay(r.stdout),
				trawlerCommandToken(trawler),
			),
		)
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	return writeCommandGroup(r.stdout, "Commands:", commands)
}

func writeCommandGroup(w io.Writer, heading string, commands []namespaceCommand) error {
	if _, err := fmt.Fprintln(w, heading); err != nil {
		return err
	}
	rows := make([][2]string, 0, len(commands))
	for _, command := range commands {
		rows = append(rows, [2]string{command.Command, command.Title})
	}
	for _, row := range formatRowsForOutputWidth(rows, 2, render.OutputWidth(w)) {
		if _, err := fmt.Fprintln(w, row); err != nil {
			return err
		}
	}
	return nil
}

func writeNamespaceCommandHelp(
	w io.Writer,
	trawler InstalledTrawler,
	token string,
	command *federation.RegisteredTrawlerCommandDeclaration,
) error {
	invocation := commandInvocation(command)
	flags := namespaceCommandFlags(command)
	usage := fmt.Sprintf("Usage: %s %s %s [flags]", render.TrawlInvocationDisplay(w), token, invocation)
	if _, err := fmt.Fprintln(w, wrapTextForOutputWidth(usage, render.OutputWidth(w))); err != nil {
		return err
	}
	if description := strings.TrimSpace(command.GetTrawlerCommandHelpDescription()); description != "" {
		if _, err := fmt.Fprintln(w, "\n"+wrapTextForOutputWidth(description, render.OutputWidth(w))); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "\nFlags:"); err != nil {
		return err
	}
	flagRows := [][2]string{{"-h, --help", "Show help"}}
	for _, commandFlag := range flags {
		flagRows = append(flagRows, [2]string{commandFlag.humanFlagSyntax(), commandFlag.help})
	}
	for _, flagRow := range formatRowsForOutputWidth(flagRows, 4, render.OutputWidth(w)) {
		if _, err := fmt.Fprintln(w, flagRow); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) writeNamespaceCommandGroupHelp(
	trawler InstalledTrawler,
	token string,
	prefix []string,
) error {
	commands := namespaceCommandList(trawler)
	if len(prefix) > 0 {
		kept := commands[:0]
		for _, command := range commands {
			if tokensHavePrefix(strings.Fields(command.Command), prefix) {
				kept = append(kept, command)
			}
		}
		commands = kept
	}
	if len(commands) == 0 {
		return usageErr{humanFacingUsageErrorMessage(fmt.Sprintf("Unknown %s command %q.", trawlerHumanName(trawler), strings.Join(prefix, " ")))}
	}
	return writeCommandGroup(r.stdout, "Commands:", commands)
}

type namespaceCommandFlag struct {
	name                  string
	usageMetavariableName string
	help                  string
	defaultValue          string
	isBoolean             bool
}

func (commandFlag namespaceCommandFlag) humanFlagSyntax() string {
	flagSyntax := "--" + strings.TrimSpace(commandFlag.name)
	if commandFlag.isBoolean {
		return flagSyntax
	}
	valueForFlagSyntax := strings.TrimSpace(commandFlag.defaultValue)
	if valueForFlagSyntax == "" {
		valueForFlagSyntax = strings.TrimSpace(commandFlag.usageMetavariableName)
		if valueForFlagSyntax == "" {
			valueForFlagSyntax = "VALUE"
		}
	}
	return flagSyntax + "=" + valueForFlagSyntax
}

func namespaceCommandFlags(command *federation.RegisteredTrawlerCommandDeclaration) []namespaceCommandFlag {
	declarations := command.GetTrawlerCommandFlagDeclarations()
	flags := make([]namespaceCommandFlag, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration == nil {
			continue
		}
		rawHelpDescription := declaration.GetTrawlerCommandFlagHelpDescription()
		usageMetavariableName, helpDescription := flag.UnquoteUsage(&flag.Flag{Usage: rawHelpDescription})
		if helpDescription == rawHelpDescription {
			usageMetavariableName = ""
		}
		defaultValue := strings.ToLower(strings.TrimSpace(declaration.GetTrawlerCommandFlagDefaultValue()))
		flags = append(flags, namespaceCommandFlag{
			name:                  declaration.GetTrawlerCommandFlagName(),
			usageMetavariableName: usageMetavariableName,
			help:                  helpDescription,
			defaultValue:          declaration.GetTrawlerCommandFlagDefaultValue(),
			isBoolean:             defaultValue == "true" || defaultValue == "false",
		})
	}
	sort.Slice(flags, func(left, right int) bool { return flags[left].name < flags[right].name })
	return flags
}

type namespaceCommand struct {
	Command string
	Title   string
}

func namespaceCommandList(trawler InstalledTrawler) []namespaceCommand {
	declarations := trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations()
	commands := make([]namespaceCommand, 0, len(declarations))
	for _, command := range declarations {
		if command == nil || !trawlerCommandIsShownInNamespaceHelp(command) {
			continue
		}
		invocation := commandInvocation(command)
		if invocation == "" {
			continue
		}
		commands = append(commands, namespaceCommand{
			Command: invocation,
			Title:   strings.TrimSpace(command.GetTrawlerCommandHelpDescription()),
		})
	}
	return commands
}

func trawlerCommandIsShownInNamespaceHelp(command *federation.RegisteredTrawlerCommandDeclaration) bool {
	switch command.GetTrawlerCommandDiscoveryPlacement() {
	case federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_SHOWN_IN_BARE_TRAWL_OVERVIEW_AND_TRAWLER_NAMESPACE_HELP,
		federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_SHOWN_ONLY_IN_TRAWLER_NAMESPACE_HELP:
		return true
	default:
		return false
	}
}

func namespaceShowsSharedTrawlerOperation(trawler InstalledTrawler, sharedOperation federation.SharedTrawlerOperation) bool {
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command != nil && command.GetSharedTrawlerOperation() == sharedOperation && trawlerCommandIsShownInNamespaceHelp(command) {
			return true
		}
	}
	return false
}

func (r *Runtime) runNamespaceConversations(
	trawler InstalledTrawler,
	token string,
	arguments []string,
) error {
	arguments = namespaceCommandArguments(arguments)
	if len(arguments) == 0 || arguments[0] != "conversations" {
		return usageErr{humanFacingUsageErrorMessage("The command must come before its options.")}
	}
	var command ConversationsCmd
	parser, err := kong.New(
		&command,
		kong.Name(render.TrawlInvocationDisplay(r.stdout)+" "+token+" conversations"),
		kong.Description("List conversations"),
		kong.UsageOnError(),
		kong.Writers(r.stdout, r.stderr),
		kong.Help(kong.DefaultHelpPrinter),
		kong.Exit(func(int) { panic(helpShown{}) }),
	)
	if err != nil {
		return err
	}
	parser.Model.HelpFlag.Help = "Show help"
	if _, err := parser.Parse(arguments[1:]); err != nil {
		return usageErr{errors.New(humanUsageErrorMessage(err.Error()))}
	}
	installedTrawlers := discoverInstalledTrawlers(r.ctx)
	return command.runForTrawler(r, trawler, installedTrawlers)
}

// namespaceMatch finds the declared command whose literal prefix the
// request's leading tokens complete. It matches the full prefix, not just
// the first token, so an incomplete command — "contacts" without its "export"
// — gets a trawl-owned error instead of reaching trawlkit.
func namespaceMatch(trawler InstalledTrawler, rest []string) (*federation.RegisteredTrawlerCommandDeclaration, bool) {
	leading := leadingLiterals(rest)
	if len(leading) == 0 {
		return nil, false
	}
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command == nil || !trawlerCommandIsShownInNamespaceHelp(command) {
			continue
		}
		prefix := fixedCommandTokens(command)
		if len(prefix) > 0 && sharedTrawlerCommandUsesRootExecution(command) {
			continue
		}
		maximumLeadingLiteralCount := len(prefix) + len(command.GetTrawlerCommandPositionalArgumentNames())
		if len(leading) > maximumLeadingLiteralCount {
			continue
		}
		if len(prefix) > 0 && tokensHavePrefix(leading, prefix) {
			return command, true
		}
	}
	return nil, false
}

func sharedTrawlerCommandUsesRootExecution(
	command *federation.RegisteredTrawlerCommandDeclaration,
) bool {
	switch command.GetSharedTrawlerOperation() {
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS:
		return true
	default:
		return false
	}
}

// fixedCommandTokens is the declared command path a person types.
func fixedCommandTokens(command *federation.RegisteredTrawlerCommandDeclaration) []string {
	return strings.Fields(registeredTrawlerCommandName(command))
}

// leadingLiterals returns command words until the first trawler flag.
func leadingLiterals(rest []string) []string {
	var out []string
	for _, arg := range rest {
		if isGlobalFlag(arg) {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			break
		}
		out = append(out, arg)
	}
	return out
}

func tokensHavePrefix(tokens, prefix []string) bool {
	if len(tokens) < len(prefix) {
		return false
	}
	for i, want := range prefix {
		if tokens[i] != want {
			return false
		}
	}
	return true
}

// commandInvocation is what a person types for a declared trawler command.
func commandInvocation(command *federation.RegisteredTrawlerCommandDeclaration) string {
	name := registeredTrawlerCommandName(command)
	if name == "" {
		return ""
	}
	positionalArgumentNames := command.GetTrawlerCommandPositionalArgumentNames()
	if len(positionalArgumentNames) == 0 {
		return name
	}
	return name + " " + strings.Join(positionalArgumentNames, " ")
}

func registeredTrawlerCommandName(
	command *federation.RegisteredTrawlerCommandDeclaration,
) string {
	if command == nil {
		return ""
	}
	if sharedTrawlerOperation := command.GetSharedTrawlerOperation(); sharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
		return trawlkit.SharedTrawlerOperationCommandName(sharedTrawlerOperation)
	}
	return strings.Join(strings.Fields(command.GetBespokeTrawlerCommandName()), " ")
}

func argsAfter(args []string, token string) []string {
	for i, arg := range args {
		if arg == token {
			return args[i+1:]
		}
	}
	return nil
}

func firstNonFlag(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
