package render

import (
	"io"
	"strings"
)

// Help is written here, and only here, for bare trawl, trawl --help, every
// command's --help and every trawler's command list. Commands describe
// themselves with these typed values; the library chooses the layout and the
// syntax of placeholders and flags.

// How --after and --before read a date or time, said once for every command
// that takes them. A time without an offset is in this computer's time zone,
// the zone lists show messages in; a time copied from trawl keeps its own
// offset.
const (
	OnOrAfterDateOptionDescription = "Results on or after this date or time: 2026-10-04, " +
		"2026-10-04 21:00 in this computer's time zone, or a time copied from trawl, " +
		"such as 2026-10-04 21:00 +02:00 (19:00 UTC)"
	OnOrBeforeDateOptionDescription = "Results on or before this date or time, read as --after reads it"
)

const trawlOrientation = "Search your app history in local archives. Each trawler copies one app's history " +
	"to its own archive. OpenTrawl searches them together."

// CommandHelp describes one command a person can type after trawl.
type CommandHelp struct {
	CommandWordsAfterTrawlInvocation  []string
	CommandPositionalArgumentsInOrder []CommandPositionalArgumentHelp
	CommandDescription                string
	CommandFlagsInDisplayOrder        []CommandFlagHelp
}

// CommandPositionalArgumentHelp describes one argument that follows the
// command words.
type CommandPositionalArgumentHelp struct {
	ArgumentPlaceholder string
	ArgumentIsOptional  bool
	ArgumentRepeats     bool
	ArgumentDescription string
}

// CommandFlagHelp describes one flag. A flag with no value placeholder is
// a switch.
type CommandFlagHelp struct {
	FlagLongName         string
	FlagShortName        string
	FlagValuePlaceholder string
	FlagIsRequired       bool
	FlagDescription      string
}

// TrawlerOverviewHelp lists one installed trawler, by the word a person types
// after trawl: the command names bare trawl shows, and every command
// trawl --help shows.
type TrawlerOverviewHelp struct {
	TrawlerCommandName                 string
	TrawlerCommandNamesShownInOverview []string
	TrawlerCommandsInDisplayOrder      []CommandHelp
}

// TrawlOverviewHelp is what bare trawl and trawl --help show.
type TrawlOverviewHelp struct {
	BuiltInCommandsInDisplayOrder   []CommandHelp
	GlobalFlagsInDisplayOrder       []CommandFlagHelp
	InstalledTrawlersInDisplayOrder []TrawlerOverviewHelp
}

// TrawlerNamespaceHelp is what trawl TRAWLER shows.
type TrawlerNamespaceHelp struct {
	TrawlerDisplayName            string
	TrawlerCommandsInDisplayOrder []CommandHelp
	CommandWordsToStartWith       []string
	TrawlerSearchCommandArguments []string
}

const (
	helpTwoColumnGap                            = 2
	helpRowIndent                               = "  "
	helpDescriptionIndentWhenWrapped            = "    "
	trawlerOverviewCommandSeparator             = " · "
	helpMinimumDescriptionWidthBesideLeftColumn = 24
)

// The commands bare trawl offers first, in order.
var trawlStartHereCommandWords = [][]string{{"search"}, {"open"}, {"update"}, {"status"}}

// WriteTrawlOverview writes bare trawl: what OpenTrawl is, the trawlers, and
// where to start.
func (output TrawlCommandOutput) WriteTrawlOverview(overview TrawlOverviewHelp) error {
	writer := output.standardOutput
	sections := [][]string{
		Wrap(trawlOrientation, OutputWidth(writer)),
		trawlerOverviewLines(writer, overview.InstalledTrawlersInDisplayOrder),
		startHereLines(writer, overview.BuiltInCommandsInDisplayOrder),
	}
	return writeHelpSections(writer, sections)
}

// WriteTrawlHelp writes trawl --help: what OpenTrawl is, every command and
// global flag, and the trawlers.
func (output TrawlCommandOutput) WriteTrawlHelp(overview TrawlOverviewHelp) error {
	writer := output.standardOutput
	outputWidth := OutputWidth(writer)
	commandRows := make([][2]string, 0, len(overview.BuiltInCommandsInDisplayOrder))
	for _, commandHelp := range overview.BuiltInCommandsInDisplayOrder {
		commandRows = append(commandRows, [2]string{
			trawlInvocationDisplayForWriter(writer) + " " + commandUsageSyntax(commandHelp),
			commandHelp.CommandDescription,
		})
	}
	sections := [][]string{
		Wrap(trawlOrientation, outputWidth),
		Wrap("Usage: "+trawlInvocationDisplayForWriter(writer)+" COMMAND [flags]", outputWidth),
		append([]string{"Commands:"}, twoColumnHelpRows(commandRows, helpTwoColumnGap, outputWidth)...),
		append([]string{"Flags:"}, twoColumnHelpRows(flagHelpRows(overview.GlobalFlagsInDisplayOrder), helpTwoColumnGap, outputWidth)...),
		trawlerCommandLines(writer, overview.InstalledTrawlersInDisplayOrder),
	}
	return writeHelpSections(writer, sections)
}

// trawlerCommandLines lists every trawler command as a person types it, so
// trawl calendar events is found without guessing.
func trawlerCommandLines(writer io.Writer, installedTrawlers []TrawlerOverviewHelp) []string {
	rows := make([][2]string, 0)
	for _, trawler := range installedTrawlers {
		for _, commandHelp := range trawler.TrawlerCommandsInDisplayOrder {
			rows = append(rows, [2]string{
				trawlInvocationDisplayForWriter(writer) + " " + commandUsageSyntax(commandHelp),
				commandHelp.CommandDescription,
			})
		}
	}
	if len(rows) == 0 {
		return trawlerOverviewLines(writer, installedTrawlers)
	}
	return append([]string{"Trawler commands:"}, twoColumnHelpRows(rows, helpTwoColumnGap, OutputWidth(writer))...)
}

// WriteCommandHelp writes one command's --help.
func (output TrawlCommandOutput) WriteCommandHelp(commandHelp CommandHelp) error {
	writer := output.standardOutput
	outputWidth := OutputWidth(writer)
	usage := trawlInvocationDisplayForWriter(writer) + " " + commandUsageSyntax(commandHelp)
	if len(commandHelp.CommandFlagsInDisplayOrder) > 0 {
		usage += " [flags]"
	}
	sections := [][]string{WrapWithIndent("Usage: ", usage, outputWidth, helpDescriptionIndentWhenWrapped)}
	if description := strings.TrimSpace(commandHelp.CommandDescription); description != "" {
		sections = append(sections, Wrap(description, outputWidth))
	}
	argumentRows := make([][2]string, 0, len(commandHelp.CommandPositionalArgumentsInOrder))
	for _, argument := range commandHelp.CommandPositionalArgumentsInOrder {
		if strings.TrimSpace(argument.ArgumentDescription) == "" {
			continue
		}
		argumentRows = append(argumentRows, [2]string{positionalArgumentSyntax(argument), argument.ArgumentDescription})
	}
	if len(argumentRows) > 0 {
		sections = append(sections, append([]string{"Arguments:"}, twoColumnHelpRows(argumentRows, helpTwoColumnGap, outputWidth)...))
	}
	if len(commandHelp.CommandFlagsInDisplayOrder) > 0 {
		sections = append(sections, append(
			[]string{"Flags:"},
			twoColumnHelpRows(flagHelpRows(commandHelp.CommandFlagsInDisplayOrder), helpTwoColumnGap, outputWidth)...,
		))
	}
	return writeHelpSections(writer, sections)
}

// WriteTrawlerNamespaceHelp writes trawl TRAWLER: its commands and where to
// start.
func (output TrawlCommandOutput) WriteTrawlerNamespaceHelp(namespaceHelp TrawlerNamespaceHelp) error {
	writer := output.standardOutput
	outputWidth := OutputWidth(writer)
	sections := [][]string{Wrap(strings.TrimSpace(namespaceHelp.TrawlerDisplayName), outputWidth)}
	if len(namespaceHelp.TrawlerCommandsInDisplayOrder) > 0 {
		commandRows := make([][2]string, 0, len(namespaceHelp.TrawlerCommandsInDisplayOrder))
		for _, commandHelp := range namespaceHelp.TrawlerCommandsInDisplayOrder {
			commandRows = append(commandRows, [2]string{
				trawlInvocationDisplayForWriter(writer) + " " + commandUsageSyntax(commandHelp),
				commandHelp.CommandDescription,
			})
		}
		sections = append(sections, append([]string{"Commands:"}, twoColumnHelpRows(commandRows, helpTwoColumnGap, outputWidth)...))
	}
	if err := writeHelpSections(writer, sections); err != nil {
		return err
	}
	nextCommands := make([]NextCommand, 0, 1)
	if len(namespaceHelp.CommandWordsToStartWith) > 0 {
		nextCommands = append(nextCommands, nextCommandWithLabel(
			nextCommandLabelStartWithTrawlerCommand,
			namespaceHelp.CommandWordsToStartWith...,
		))
	} else if len(namespaceHelp.TrawlerSearchCommandArguments) > 0 {
		nextCommands = append(nextCommands, nextCommandWithLabel(
			nextCommandLabelSearchTrawlerArchive,
			namespaceHelp.TrawlerSearchCommandArguments...,
		))
	}
	return writeNextCommands(writer, nextCommands)
}

// WriteTrawlVersion writes the version number alone, for scripts.
func (output TrawlCommandOutput) WriteTrawlVersion(trawlVersion string) error {
	return writeLine(output.standardOutput, strings.TrimSpace(trawlVersion))
}

func writeHelpSections(writer io.Writer, sections [][]string) error {
	for sectionIndex, section := range sections {
		if len(section) == 0 {
			continue
		}
		if sectionIndex > 0 {
			if err := writeLine(writer, ""); err != nil {
				return err
			}
		}
		if err := writeLines(writer, section); err != nil {
			return err
		}
	}
	return nil
}

// trawlerOverviewLines writes one row per trawler that starts with what a
// person types, so each row teaches the trawl TRAWLER COMMAND form.
func trawlerOverviewLines(writer io.Writer, installedTrawlers []TrawlerOverviewHelp) []string {
	outputWidth := OutputWidth(writer)
	if len(installedTrawlers) == 0 {
		return append([]string{"Trawlers:"}, WrapWithIndent(helpRowIndent, "No trawlers are available.", outputWidth, helpRowIndent)...)
	}
	rows := make([][2]string, 0, len(installedTrawlers))
	for _, trawler := range installedTrawlers {
		rows = append(rows, [2]string{
			trawlInvocationDisplayForWriter(writer) + " " + strings.TrimSpace(trawler.TrawlerCommandName),
			strings.Join(trawler.TrawlerCommandNamesShownInOverview, trawlerOverviewCommandSeparator),
		})
	}
	return append([]string{"Trawlers:"}, twoColumnHelpRows(rows, helpTwoColumnGap, outputWidth)...)
}

func startHereLines(writer io.Writer, builtInCommands []CommandHelp) []string {
	rows := make([][2]string, 0, len(trawlStartHereCommandWords)+1)
	for _, startHereCommandWords := range trawlStartHereCommandWords {
		for _, commandHelp := range builtInCommands {
			if strings.Join(commandHelp.CommandWordsAfterTrawlInvocation, " ") != strings.Join(startHereCommandWords, " ") {
				continue
			}
			rows = append(rows, [2]string{
				trawlInvocationDisplayForWriter(writer) + " " + commandUsageSyntax(commandHelp),
				commandHelp.CommandDescription,
			})
		}
	}
	rows = append(rows, [2]string{trawlInvocationDisplayForWriter(writer) + " --help", "See every command"})
	return append([]string{"Start here:"}, twoColumnHelpRows(rows, helpTwoColumnGap, OutputWidth(writer))...)
}

// commandUsageSyntax writes what a person types: the command words, the
// arguments, then any flag the command cannot run without.
func commandUsageSyntax(commandHelp CommandHelp) string {
	parts := append([]string(nil), commandHelp.CommandWordsAfterTrawlInvocation...)
	for _, argument := range commandHelp.CommandPositionalArgumentsInOrder {
		parts = append(parts, positionalArgumentSyntax(argument))
	}
	for _, commandFlag := range commandHelp.CommandFlagsInDisplayOrder {
		if commandFlag.FlagIsRequired {
			parts = append(parts, flagSyntax(CommandFlagHelp{
				FlagLongName:         commandFlag.FlagLongName,
				FlagValuePlaceholder: commandFlag.FlagValuePlaceholder,
			}))
		}
	}
	return strings.Join(parts, " ")
}

// positionalArgumentSyntax writes WORD, [WORD] or WORDS... in one style for
// every command.
func positionalArgumentSyntax(argument CommandPositionalArgumentHelp) string {
	syntax := strings.ToUpper(strings.TrimSpace(argument.ArgumentPlaceholder))
	if argument.ArgumentRepeats {
		syntax += "..."
	}
	if argument.ArgumentIsOptional {
		syntax = "[" + syntax + "]"
	}
	return syntax
}

func flagHelpRows(flags []CommandFlagHelp) [][2]string {
	rows := make([][2]string, 0, len(flags))
	for _, commandFlag := range flags {
		rows = append(rows, [2]string{flagSyntax(commandFlag), commandFlag.FlagDescription})
	}
	return rows
}

// flagSyntax writes -v, --verbose or --limit=COUNT in one style for every
// command.
func flagSyntax(commandFlag CommandFlagHelp) string {
	syntax := "--" + strings.TrimSpace(commandFlag.FlagLongName)
	if placeholder := strings.ToUpper(strings.TrimSpace(commandFlag.FlagValuePlaceholder)); placeholder != "" {
		syntax += "=" + placeholder
	}
	if shortName := strings.TrimSpace(commandFlag.FlagShortName); shortName != "" {
		syntax = "-" + shortName + ", " + syntax
	}
	return syntax
}

// twoColumnHelpRows aligns "  left  right" rows, wrapping each right value
// under itself; only when the left column leaves too little room does each
// description go on the lines under its left value.
func twoColumnHelpRows(rows [][2]string, gap int, outputWidth int) []string {
	leftColumnWidth := 0
	for _, row := range rows {
		leftColumnWidth = max(leftColumnWidth, DisplayWidth(row[0]))
	}
	descriptionIndent := strings.Repeat(" ", DisplayWidth(helpRowIndent)+leftColumnWidth+gap)
	if outputWidth-DisplayWidth(descriptionIndent) >= helpMinimumDescriptionWidthBesideLeftColumn {
		alignedRows := make([]string, 0, len(rows))
		for _, row := range rows {
			if strings.TrimSpace(row[1]) == "" {
				alignedRows = append(alignedRows, helpRowIndent+row[0])
				continue
			}
			firstLinePrefix := helpRowIndent + row[0] + strings.Repeat(" ", leftColumnWidth-DisplayWidth(row[0])+gap)
			alignedRows = append(alignedRows, WrapWithIndent(firstLinePrefix, row[1], outputWidth, descriptionIndent)...)
		}
		return alignedRows
	}
	stackedRows := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		stackedRows = append(stackedRows, WrapWithIndent(helpRowIndent, row[0], outputWidth, helpRowIndent)...)
		if strings.TrimSpace(row[1]) == "" {
			continue
		}
		stackedRows = append(stackedRows, WrapWithIndent(
			helpDescriptionIndentWhenWrapped,
			row[1],
			outputWidth,
			helpDescriptionIndentWhenWrapped,
		)...)
	}
	return stackedRows
}
