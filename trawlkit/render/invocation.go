package render

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

const defaultTrawlInvocationDisplay = "./trawl"

type trawlInvocationDisplayWriter struct {
	io.Writer
	trawlInvocationDisplay string
}

func (writer trawlInvocationDisplayWriter) TrawlInvocationDisplay() string {
	return writer.trawlInvocationDisplay
}

func (writer trawlInvocationDisplayWriter) UnwrapWriter() io.Writer {
	return writer.Writer
}

func WithTrawlInvocationDisplay(writer io.Writer, trawlInvocationDisplay string) io.Writer {
	trawlInvocationDisplay = strings.TrimSpace(trawlInvocationDisplay)
	if trawlInvocationDisplay == "" {
		trawlInvocationDisplay = defaultTrawlInvocationDisplay
	}
	return trawlInvocationDisplayWriter{
		Writer:                 writer,
		trawlInvocationDisplay: trawlInvocationDisplay,
	}
}

func TrawlInvocationDisplay(writer io.Writer) string {
	if writerWithTrawlInvocationDisplay, ok := writer.(interface{ TrawlInvocationDisplay() string }); ok {
		if trawlInvocationDisplay := strings.TrimSpace(writerWithTrawlInvocationDisplay.TrawlInvocationDisplay()); trawlInvocationDisplay != "" {
			return trawlInvocationDisplay
		}
	}
	return defaultTrawlInvocationDisplay
}

func WriteTrawlCommandHint(writer io.Writer, hint string) error {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return nil
	}
	outputWidth := OutputWidth(writer)
	if DisplayWidth(hint) <= outputWidth {
		_, err := fmt.Fprintln(writer, hint)
		return err
	}
	label, shellCommand, found := strings.Cut(hint, ": ")
	if !found || strings.TrimSpace(label) == "" || strings.TrimSpace(shellCommand) == "" {
		for _, line := range Wrap(hint, outputWidth) {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}
		return nil
	}
	for _, line := range shellCommandHintLines(label, shellCommand, outputWidth) {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func WriteIndentedTrawlCommand(writer io.Writer, shellCommand string) error {
	for _, line := range shellCommandLines("  ", "  ", shellCommand, OutputWidth(writer)) {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func shellCommandHintLines(label, shellCommand string, outputWidth int) []string {
	return shellCommandLines(strings.TrimSpace(label)+": ", "  ", shellCommand, outputWidth)
}

func shellCommandLines(firstLinePrefix, continuationLinePrefix, shellCommand string, outputWidth int) []string {
	shellArguments := shellCommandArgumentsForWrapping(shellCommand)
	if len(shellArguments) == 0 {
		return []string{strings.TrimRight(firstLinePrefix, " ")}
	}
	var lines []string
	for len(shellArguments) > 0 {
		prefix := continuationLinePrefix
		if len(lines) == 0 {
			prefix = firstLinePrefix
		}
		argumentsOnLine := 1
		for argumentsOnLine < len(shellArguments) {
			candidate := prefix + strings.Join(shellArguments[:argumentsOnLine+1], " ")
			if argumentsOnLine+1 < len(shellArguments) {
				candidate += " \\"
			}
			if DisplayWidth(candidate) > outputWidth {
				break
			}
			argumentsOnLine++
		}
		line := prefix + strings.Join(shellArguments[:argumentsOnLine], " ")
		if argumentsOnLine < len(shellArguments) {
			line += " \\"
		}
		lines = append(lines, line)
		shellArguments = shellArguments[argumentsOnLine:]
	}
	return lines
}

func shellCommandArgumentsForWrapping(shellCommand string) []string {
	var arguments []string
	var argument strings.Builder
	var quote rune
	escaped := false
	flushArgument := func() {
		if argument.Len() == 0 {
			return
		}
		arguments = append(arguments, argument.String())
		argument.Reset()
	}
	for _, character := range strings.TrimSpace(shellCommand) {
		if escaped {
			argument.WriteRune(character)
			escaped = false
			continue
		}
		switch {
		case character == '\\' && quote != '\'':
			argument.WriteRune(character)
			escaped = true
		case quote == 0 && unicode.IsSpace(character):
			flushArgument()
		case quote == 0 && (character == '\'' || character == '"'):
			quote = character
			argument.WriteRune(character)
		case quote == character:
			quote = 0
			argument.WriteRune(character)
		default:
			argument.WriteRune(character)
		}
	}
	flushArgument()
	return arguments
}
