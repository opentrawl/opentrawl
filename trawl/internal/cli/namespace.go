package cli

import (
	"io"
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

var builtInCommandNames = []string{"status", "update", "search", "who", "conversations", "messages", "open", "help"}

func reservedCommand(name string) bool {
	for _, builtInCommandName := range builtInCommandNames {
		if name == builtInCommandName {
			return true
		}
	}
	return false
}

// topLevelCommandNames is every word that may follow trawl: the built-in
// commands and the installed trawlers.
func topLevelCommandNames(installedTrawlers []InstalledTrawler) []string {
	return append(append([]string(nil), builtInCommandNames...), installedTrawlerCommandNames(installedTrawlers)...)
}

func installedTrawlerCommandNames(installedTrawlers []InstalledTrawler) []string {
	trawlerNames := make([]string, 0, len(installedTrawlers))
	for _, trawler := range installedTrawlers {
		trawlerNames = append(trawlerNames, trawlerCommandToken(trawler))
	}
	return trawlerNames
}

// trawlerCommandNames is every word that may follow trawl TRAWLER.
func trawlerCommandNames(trawler InstalledTrawler) []string {
	commandHelps := namespaceCommandHelp(trawler)
	commandNames := make([]string, 0, len(commandHelps))
	for _, commandHelp := range commandHelps {
		if len(commandHelp.CommandWordsAfterTrawlInvocation) > 1 {
			commandNames = append(commandNames, commandHelp.CommandWordsAfterTrawlInvocation[1])
		}
	}
	return commandNames
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
		return render.UnknownCommand(token, topLevelCommandNames(installedTrawlers))
	}
	if trawler.TrawlerDiscoveryError != nil {
		r.logInfo(
			"trawler_discovery_failed",
			trawlerField(trawler)+" error="+logQuote(cklog.InternalErrorLogMessage(trawler.TrawlerDiscoveryError)),
		)
		description := ckoutput.ErrorDescriptionFor(trawler.TrawlerDiscoveryError)
		if errorDescriptionMeansArchiveUnavailable(description.Code) {
			return render.TrawlerArchiveIsNotAvailable(trawlerHumanName(trawler))
		}
		return render.CommandDidNotCompleteForTrawler(trawlerHumanName(trawler))
	}
	rest := argsAfter(args, token)
	if firstNonFlag(rest) == "" {
		return r.parseNamespaceOptionsAndRenderHelp(trawler, rest)
	}
	return r.runNamespaceCommand(trawler, token, rest)
}

func (r *Runtime) parseNamespaceOptionsAndRenderHelp(trawler InstalledTrawler, arguments []string) error {
	var options struct {
		Verbose     int                     `short:"v" name:"verbose" type:"counter"`
		ShowVersion trawlVersionDisplayFlag `name:"version"`
	}
	parser, err := kong.New(&options, kong.Name(trawlerCommandToken(trawler)),
		kong.Writers(io.Discard, io.Discard), kong.Bind(r.output),
		kong.Help(func(_ kong.HelpOptions, _ *kong.Context) error { return r.renderNamespace(trawler) }),
		kong.Exit(func(int) { panic(helpShown{}) }),
	)
	if err != nil {
		return err
	}
	if _, err := parser.Parse(arguments); err != nil {
		return commandFailureFromParserError(err)
	}
	return r.renderNamespace(trawler)
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
			return r.output.WriteCommandHelp(commandHelpFromTrawlerCommandDeclaration(trawler, command))
		}
		return r.writeNamespaceCommandGroupHelp(trawler, leadingLiterals(rest))
	}
	if !ok {
		leading := leadingLiterals(rest)
		if len(leading) == 0 {
			// The first token is a trawler flag: the command came after its
			// flags. Name the shape, not the flag value.
			return render.CommandMustComeBeforeItsOptions()
		}
		return render.UnknownTrawlerCommand(trawlerHumanName(trawler), strings.Join(leading, " "), trawlerCommandNames(trawler))
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
	writesNoFiles, err := trawlkit.DeclaredTrawlerCommandWritesNoFiles(trawler.Trawler, arguments)
	if err != nil {
		return commandFailureFromTrawlerError(trawler, err)
	}
	if !writesNoFiles {
		_ = r.startLogRun("namespace")
	}
	commandName := firstNonFlag(arguments)
	started := r.logTrawlerStart(trawler, commandName)
	response, localShortReferencesByCanonicalRecordReference, renderContext, err := r.trawlerExecutor().ExecuteDeclaredTrawlerCommand(r.ctx, trawler.Trawler, arguments)
	r.logTrawlerDone(trawler, commandName, started, err)
	if err != nil {
		return commandFailureFromTrawlerError(trawler, err)
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
	return r.output.WriteTrawlerCommandResponse(response, globallyRoutableTrawlLinksByCanonicalRecordReference, renderContext)
}

// commandFailureFromTrawlerError keeps a trawler's own sentence when its
// error code says the reader can act on it, and otherwise says which
// trawler failed.
func commandFailureFromTrawlerError(trawler InstalledTrawler, err error) render.CommandFailure {
	if failure, worded := render.AsCommandFailure(err); worded {
		return failure
	}
	description := ckoutput.ErrorDescriptionFor(err)
	if len(description.PhotosStepsNotBuilt) > 0 {
		return render.PhotosStepsNotBuilt(description.PhotosStepsNotBuilt)
	}
	switch description.Code {
	case "usage":
		return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, description.Message)
	case "not_found", "ambiguous":
		return render.CommandFailureFromTrawler(render.CommandFailureKindNothingOrSeveralThingsMatched, description.Message)
	case "unknown_short_ref", "ambiguous_short_ref", "invalid_ref":
		return render.CommandFailureFromTrawler(render.CommandFailureKindLinkIsUnknown, description.Message)
	}
	if errorDescriptionMeansArchiveUnavailable(description.Code) {
		return render.TrawlerArchiveIsNotAvailable(trawlerHumanName(trawler))
	}
	return render.CommandDidNotCompleteForTrawler(trawlerHumanName(trawler))
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
	namespaceHelp := render.TrawlerNamespaceHelp{
		TrawlerDisplayName:            trawlerHumanName(trawler),
		TrawlerCommandsInDisplayOrder: namespaceCommandHelp(trawler),
	}
	if supportsSharedTrawlerOperation(trawler, federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH) {
		namespaceHelp.TrawlerSearchCommandArguments = []string{"search", "WORDS", "--trawler", trawlerCommandToken(trawler)}
	}
	if overviewCommands := trawlerCommandNamesShownInBareTrawlOverview(trawler); len(overviewCommands) > 0 {
		namespaceHelp.CommandWordsToStartWith = append(
			[]string{trawlerCommandToken(trawler)},
			strings.Fields(overviewCommands[0])...,
		)
	}
	return r.output.WriteTrawlerNamespaceHelp(namespaceHelp)
}

func (r *Runtime) writeNamespaceCommandGroupHelp(
	trawler InstalledTrawler,
	prefix []string,
) error {
	commands := namespaceCommandHelp(trawler)
	if len(prefix) > 0 {
		kept := commands[:0]
		for _, command := range commands {
			if tokensHavePrefix(command.CommandWordsAfterTrawlInvocation[1:], prefix) {
				kept = append(kept, command)
			}
		}
		commands = kept
	}
	if len(commands) == 0 {
		return render.UnknownTrawlerCommand(trawlerHumanName(trawler), strings.Join(prefix, " "), trawlerCommandNames(trawler))
	}
	return r.output.WriteTrawlerNamespaceHelp(render.TrawlerNamespaceHelp{
		TrawlerDisplayName:            trawlerHumanName(trawler),
		TrawlerCommandsInDisplayOrder: commands,
	})
}

func namespaceCommandHelp(trawler InstalledTrawler) []render.CommandHelp {
	declarations := trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations()
	commands := make([]render.CommandHelp, 0, len(declarations))
	for _, command := range declarations {
		if command == nil || !trawlerCommandIsShownInNamespaceHelp(command) || registeredTrawlerCommandName(command) == "" {
			continue
		}
		commands = append(commands, commandHelpFromTrawlerCommandDeclaration(trawler, command))
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
		return render.CommandMustComeBeforeItsOptions()
	}
	var command ConversationsCmd
	parser, err := kong.New(
		&command,
		kong.Name(token+" conversations"),
		kong.Description("List conversations"),
		kong.Writers(io.Discard, io.Discard),
		kong.Help(func(_ kong.HelpOptions, ctx *kong.Context) error {
			conversationsHelp := commandHelpFromKongNode(ctx.Model.Node)
			conversationsHelp.CommandWordsAfterTrawlInvocation = []string{token, "conversations"}
			return r.output.WriteCommandHelp(conversationsHelp)
		}),
		kong.Exit(func(int) { panic(helpShown{}) }),
	)
	if err != nil {
		return err
	}
	parser.Model.HelpFlag.Help = "Show help"
	if _, err := parser.Parse(arguments[1:]); err != nil {
		return commandFailureFromParserError(err)
	}
	_ = r.startLogRun("namespace")
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
