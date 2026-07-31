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
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
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
	if firstNonFlag(rest) == "open" {
		return r.runNamespaceOpen(trawler, rest)
	}
	if firstNonFlag(rest) == "conversations" &&
		supportsSharedTrawlerOperation(
			trawler,
			federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
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

// runNamespaceOpen joins the shared root open path.
func (r *Runtime) runNamespaceOpen(trawler InstalledTrawler, rest []string) error {
	if namespaceOpenNeedsRootGrammar(rest) {
		return r.runRootOpenGrammar(rest)
	}
	args := namespaceOpenArgs(rest)
	if len(args) != 2 || args[0] != "open" {
		return r.runRootOpenGrammar(rest)
	}
	requestedLink := args[1]
	var localShortReferenceAcceptedBySelectedTrawler string
	requestedTrawlLink := trawlkit.NewGloballyRoutableTrawlLink(requestedLink)
	if route, err := trawlkit.ParseGloballyRoutableTrawlLink(requestedTrawlLink); err == nil {
		routeTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)
		if routeTrawlerIdentity == installedTrawlerIdentityText(trawler) {
			localShortReferenceAcceptedBySelectedTrawler =
				trawlkit.LocalTrawlerShortReferenceText(route.LocalShortReference)
		} else if _, found := findInstalledTrawler(discoverInstalledTrawlers(r.ctx), routeTrawlerIdentity); found {
			return r.renderOpenResponse(openFailureForRequestedLink(
				requestedTrawlLink,
				federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
				"This link belongs to another trawler.",
			))
		}
	}
	if !trawlkit.ValidShortRef(localShortReferenceAcceptedBySelectedTrawler) {
		return r.renderOpenResponse(openFailureForRequestedLink(
			requestedTrawlLink,
			federationv1.FailureCode_FAILURE_CODE_INVALID_INPUT,
			"The link is not valid.",
		))
	}
	return r.renderOpenResponse(r.canonicalOpen(
		r.federationOpenTrawlers([]InstalledTrawler{trawler}),
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
		trawlkit.NewLocalTrawlerShortReference(localShortReferenceAcceptedBySelectedTrawler),
		requestedTrawlLink,
	))
}

func namespaceOpenNeedsRootGrammar(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || strings.HasPrefix(arg, "-") && !isGlobalFlag(arg) {
			return true
		}
	}
	return false
}

func (r *Runtime) runRootOpenGrammar(args []string) error {
	args = append([]string(nil), args...)
	return execute(args, r.stdout, r.stderr, r.timeout)
}

func namespaceOpenArgs(args []string) []string {
	values := make([]string, 0, len(args))
	for _, arg := range args {
		if isGlobalFlag(arg) {
			continue
		}
		values = append(values, arg)
	}
	return values
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
	if overviewCommands := trawler.RegisteredTrawlerManifest.GetTrawlerCommandNamesShownInBareTrawlOverview(); len(overviewCommands) > 0 {
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
	if supportsSharedTrawlerOperation(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
	) {
		searchCommand := fmt.Sprintf("%s search \"boat trip\" --trawler %s", render.TrawlInvocationDisplay(r.stdout), trawlerCommandToken(trawler))
		if _, err := fmt.Fprintln(r.stdout, "\nSearch:"); err != nil {
			return err
		}
		if render.OutputWidth(r.stdout) < 80 {
			if err := render.WriteIndentedTrawlCommand(r.stdout, searchCommand); err != nil {
				return err
			}
			for _, line := range render.WrapWithIndent(
				"    ",
				"Find anything in "+displayName,
				render.OutputWidth(r.stdout),
				"    ",
			) {
				if _, err := fmt.Fprintln(r.stdout, line); err != nil {
					return err
				}
			}
		} else if _, err := fmt.Fprintln(
			r.stdout,
			strings.Join(alignRows([][2]string{{searchCommand, "Find anything in " + displayName}}, 4), "\n"),
		); err != nil {
			return err
		}
	}
	if len(commands) == 0 {
		return nil
	}
	primary, secondary := splitSecondaryCommands(commands)
	if len(primary) > 0 {
		if _, err := fmt.Fprintln(r.stdout); err != nil {
			return err
		}
		if err := writeCommandGroup(r.stdout, "Commands:", primary); err != nil {
			return err
		}
	}
	if len(secondary) > 0 {
		if _, err := fmt.Fprintln(r.stdout); err != nil {
			return err
		}
		if err := writeCommandGroup(r.stdout, "More commands:", secondary); err != nil {
			return err
		}
	}
	return nil
}

// splitSecondaryCommands keeps common commands first.
func splitSecondaryCommands(commands []namespaceCommand) (primary, secondary []namespaceCommand) {
	for _, command := range commands {
		if command.Secondary {
			secondary = append(secondary, command)
			continue
		}
		primary = append(primary, command)
	}
	return primary, secondary
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
	command *federationv1.RegisteredTrawlerCommandDeclaration,
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

func namespaceCommandFlags(command *federationv1.RegisteredTrawlerCommandDeclaration) []namespaceCommandFlag {
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
	Command   string
	Title     string
	Secondary bool
}

func namespaceCommandList(trawler InstalledTrawler) []namespaceCommand {
	declarations := trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations()
	commands := make([]namespaceCommand, 0, len(declarations)+1)
	if supportsSharedTrawlerOperation(
		trawler,
		federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
	) {
		commands = append(commands, namespaceCommand{
			Command: "conversations",
			Title:   "List conversations",
		})
	}
	for _, command := range declarations {
		if command == nil || command.GetTrawlerCommandHelpPlacement() == federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_HIDDEN_FROM_HUMAN_HELP {
			continue
		}
		invocation := commandInvocation(command)
		if invocation == "" || rootOwnedNamespaceCommand(invocation) {
			continue
		}
		commands = append(commands, namespaceCommand{
			Command:   invocation,
			Title:     strings.TrimSpace(command.GetTrawlerCommandHelpDescription()),
			Secondary: command.GetTrawlerCommandHelpPlacement() == federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_LISTED_ONLY_UNDER_MORE_TRAWLER_COMMANDS,
		})
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
	return commands
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
func namespaceMatch(trawler InstalledTrawler, rest []string) (*federationv1.RegisteredTrawlerCommandDeclaration, bool) {
	leading := leadingLiterals(rest)
	if len(leading) == 0 {
		return nil, false
	}
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command == nil {
			continue
		}
		prefix := fixedCommandTokens(command)
		if len(prefix) > 0 && rootOwnedNamespaceCommand(strings.Join(prefix, " ")) {
			continue
		}
		if len(prefix) > 0 && tokensHavePrefix(leading, prefix) {
			return command, true
		}
	}
	return nil, false
}

func rootOwnedNamespaceCommand(invocation string) bool {
	commandName := firstNonFlag(strings.Fields(invocation))
	switch commandName {
	case "metadata", "status", "update", "search", "who", "conversations", "open":
		return true
	default:
		return false
	}
}

// fixedCommandTokens is the declared command path a person types.
func fixedCommandTokens(command *federationv1.RegisteredTrawlerCommandDeclaration) []string {
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
func commandInvocation(command *federationv1.RegisteredTrawlerCommandDeclaration) string {
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
	command *federationv1.RegisteredTrawlerCommandDeclaration,
) string {
	if command == nil {
		return ""
	}
	if sharedTrawlerOperation := command.GetSharedTrawlerOperation(); sharedTrawlerOperation != federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
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
