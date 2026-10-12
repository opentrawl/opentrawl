package render

import (
	"io"
	"strings"
	"unicode"
)

// NextCommand is a trawl command the reader can run next. Every next command
// is written the same way: after the output it follows, on its own line,
// labelled, quoted for the shell and wrapped only between arguments, so it
// works with one copy and paste.
type NextCommand struct {
	nextCommandLabel                     nextCommandLabel
	trawlCommandArgumentsAfterInvocation []string
}

type nextCommandLabel string

const (
	nextCommandLabelMoreRows                 nextCommandLabel = "More"
	nextCommandLabelConversationMessages     nextCommandLabel = "Messages"
	nextCommandLabelConversationParticipants nextCommandLabel = "Participants"
	nextCommandLabelConversationsWithPerson  nextCommandLabel = "Conversations"
	nextCommandLabelOpenRecord               nextCommandLabel = "Open"
	nextCommandLabelNoteVersions             nextCommandLabel = "Versions"
	nextCommandLabelStartWithTrawlerCommand  nextCommandLabel = "Start with"
	nextCommandLabelSearchTrawlerArchive     nextCommandLabel = "Search"
	nextCommandLabelCalendarEvents           nextCommandLabel = "Events"
	nextCommandLabelTryAgain                 nextCommandLabel = "Try"
	nextCommandLabelHelp                     nextCommandLabel = "Help"
	nextCommandLabelMoreDetail               nextCommandLabel = "More detail"
	nextCommandLabelPhotos                   nextCommandLabel = "Photos"
	nextCommandLabelAllStays                 nextCommandLabel = "All stays"
)

const (
	nextCommandLabelSeparator             = ": "
	wrappedTrawlCommandContinuation       = " \\"
	wrappedTrawlCommandContinuationIndent = "  "
)

// NextCommandShowingMoreRows repeats the command the reader ran with a larger
// row limit.
func NextCommandShowingMoreRows(trawlCommandArgumentsAfterInvocation []string) NextCommand {
	return NextCommand{
		nextCommandLabel:                     nextCommandLabelMoreRows,
		trawlCommandArgumentsAfterInvocation: append([]string(nil), trawlCommandArgumentsAfterInvocation...),
	}
}

func nextCommandWithLabel(label nextCommandLabel, trawlCommandArgumentsAfterInvocation ...string) NextCommand {
	return NextCommand{
		nextCommandLabel:                     label,
		trawlCommandArgumentsAfterInvocation: trawlCommandArgumentsAfterInvocation,
	}
}

func (nextCommand NextCommand) isEmpty() bool {
	return len(nextCommand.trawlCommandArgumentsAfterInvocation) == 0
}

// writeNextCommands ends an output with its next commands, after one blank line.
func writeNextCommands(writer io.Writer, nextCommands []NextCommand) error {
	nonEmptyNextCommands := make([]NextCommand, 0, len(nextCommands))
	for _, nextCommand := range nextCommands {
		if !nextCommand.isEmpty() {
			nonEmptyNextCommands = append(nonEmptyNextCommands, nextCommand)
		}
	}
	if len(nonEmptyNextCommands) == 0 {
		return nil
	}
	if err := writeLine(writer, ""); err != nil {
		return err
	}
	for _, nextCommand := range nonEmptyNextCommands {
		if err := writeNextCommandLines(writer, nextCommand); err != nil {
			return err
		}
	}
	return nil
}

func writeNextCommandLines(writer io.Writer, nextCommand NextCommand) error {
	firstLinePrefix := string(nextCommand.nextCommandLabel) + nextCommandLabelSeparator
	for _, line := range trawlCommandLinesWrappedBetweenArguments(
		firstLinePrefix,
		wrappedTrawlCommandContinuationIndent,
		quotedTrawlCommandArguments(writer, nextCommand.trawlCommandArgumentsAfterInvocation),
		OutputWidth(writer),
	) {
		if err := writeLine(writer, line); err != nil {
			return err
		}
	}
	return nil
}

// formatTrawlCommandLine writes a whole command on one line, for a table cell.
func formatTrawlCommandLine(writer io.Writer, trawlCommandArgumentsAfterInvocation []string) string {
	return strings.Join(quotedTrawlCommandArguments(writer, trawlCommandArgumentsAfterInvocation), " ")
}

func quotedTrawlCommandArguments(writer io.Writer, trawlCommandArgumentsAfterInvocation []string) []string {
	quotedArguments := make([]string, 0, len(trawlCommandArgumentsAfterInvocation)+1)
	quotedArguments = append(quotedArguments, quoteShellArgument(trawlInvocationDisplayForWriter(writer)))
	for _, argument := range trawlCommandArgumentsAfterInvocation {
		quotedArguments = append(quotedArguments, quoteShellArgument(argument))
	}
	return quotedArguments
}

// quoteShellArgument is the one way OpenTrawl quotes a value for a shell.
// Plain values stay bare; anything else is single-quoted, which no POSIX
// shell expands.
func quoteShellArgument(argument string) string {
	if argument != "" && strings.IndexFunc(argument, func(character rune) bool {
		return !unicode.IsLetter(character) &&
			!unicode.IsDigit(character) &&
			!strings.ContainsRune("_-./:@%+=,", character)
	}) == -1 {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", `'"'"'`) + "'"
}

func trawlCommandLinesWrappedBetweenArguments(
	firstLinePrefix string,
	continuationLinePrefix string,
	quotedArguments []string,
	outputWidth int,
) []string {
	if len(quotedArguments) == 0 {
		return []string{strings.TrimRight(firstLinePrefix, " ")}
	}
	var lines []string
	for len(quotedArguments) > 0 {
		prefix := continuationLinePrefix
		if len(lines) == 0 {
			prefix = firstLinePrefix
		}
		argumentsOnLine := 1
		for argumentsOnLine < len(quotedArguments) {
			candidate := prefix + strings.Join(quotedArguments[:argumentsOnLine+1], " ")
			if argumentsOnLine+1 < len(quotedArguments) {
				candidate += wrappedTrawlCommandContinuation
			}
			if DisplayWidth(candidate) > outputWidth {
				break
			}
			argumentsOnLine++
		}
		line := prefix + strings.Join(quotedArguments[:argumentsOnLine], " ")
		if argumentsOnLine < len(quotedArguments) {
			line += wrappedTrawlCommandContinuation
		}
		lines = append(lines, line)
		quotedArguments = quotedArguments[argumentsOnLine:]
	}
	return lines
}

func nextCommandOpeningRecord(globallyRoutableTrawlLink string) NextCommand {
	if globallyRoutableTrawlLink == "" {
		return NextCommand{}
	}
	return nextCommandWithLabel(nextCommandLabelOpenRecord, "open", globallyRoutableTrawlLink)
}

func nextCommandListingConversationMessages(globallyRoutableConversationLink string) NextCommand {
	if globallyRoutableConversationLink == "" {
		return NextCommand{}
	}
	return nextCommandWithLabel(
		nextCommandLabelConversationMessages,
		"messages", "--conversation", globallyRoutableConversationLink,
	)
}

func nextCommandListingConversationsWithPerson(globallyRoutablePersonLink string) NextCommand {
	if globallyRoutablePersonLink == "" {
		return NextCommand{}
	}
	return nextCommandWithLabel(
		nextCommandLabelConversationsWithPerson,
		"conversations", "--with", globallyRoutablePersonLink,
	)
}

func nextCommandListingCalendarEvents(globallyRoutableCalendarLink string) NextCommand {
	if globallyRoutableCalendarLink == "" {
		return NextCommand{}
	}
	return nextCommandWithLabel(nextCommandLabelCalendarEvents, "calendar", "events", globallyRoutableCalendarLink)
}
