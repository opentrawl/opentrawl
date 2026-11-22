package render

import (
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"golang.org/x/sys/unix"
)

// UnconstrainedOutputWidth removes artificial 80/100-column narrowing when no
// physical human terminal edge constrains the stream (a piped command or an
// agent harness PTY). Table columns still size strictly to their content and
// never pad out to this bound.
const UnconstrainedOutputWidth = 10000

// OutputWidthSource identifies how OpenTrawl chose its output line width.
type OutputWidthSource string

const (
	OutputWidthSourceColumnsEnvironmentVariable OutputWidthSource = "columns_environment_variable"
	OutputWidthSourceAgentHarnessEnvironment    OutputWidthSource = "agent_harness_environment"
	OutputWidthSourceDumbTerminal               OutputWidthSource = "dumb_terminal"
	OutputWidthSourceInteractiveTerminal        OutputWidthSource = "interactive_terminal"
	OutputWidthSourceNonTerminalStream          OutputWidthSource = "non_terminal_stream"
)

// OutputWidthDecision records the resolved width and how OpenTrawl chose it.
type OutputWidthDecision struct {
	WidthInColumns           int
	WidthSource              OutputWidthSource
	StandardOutputIsTerminal bool
}

type writerUnwrapper interface {
	UnwrapWriter() io.Writer
}

// OutputWidth returns the human-output line budget for the writer.
func OutputWidth(w io.Writer) int {
	return ResolveOutputWidth(w).WidthInColumns
}

// ResolveOutputWidth determines the line width for w. Explicit COLUMNS always
// wins; coding-agent harnesses, dumb terminals and non-terminal streams use
// unconstrained natural width so columns do not truncate; interactive human
// terminals use their real window width.
func ResolveOutputWidth(w io.Writer) OutputWidthDecision {
	terminalColumns, isTerminal := terminalWindowWidthInColumns(w)
	if width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && width > 0 {
		return OutputWidthDecision{
			WidthInColumns:           width,
			WidthSource:              OutputWidthSourceColumnsEnvironmentVariable,
			StandardOutputIsTerminal: isTerminal,
		}
	}
	if runningInsideCodingAgentHarness() {
		return OutputWidthDecision{
			WidthInColumns:           UnconstrainedOutputWidth,
			WidthSource:              OutputWidthSourceAgentHarnessEnvironment,
			StandardOutputIsTerminal: isTerminal,
		}
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return OutputWidthDecision{
			WidthInColumns:           UnconstrainedOutputWidth,
			WidthSource:              OutputWidthSourceDumbTerminal,
			StandardOutputIsTerminal: isTerminal,
		}
	}
	if isTerminal {
		return OutputWidthDecision{
			WidthInColumns:           terminalColumns,
			WidthSource:              OutputWidthSourceInteractiveTerminal,
			StandardOutputIsTerminal: true,
		}
	}
	return OutputWidthDecision{
		WidthInColumns:           UnconstrainedOutputWidth,
		WidthSource:              OutputWidthSourceNonTerminalStream,
		StandardOutputIsTerminal: false,
	}
}

func terminalWindowWidthInColumns(w io.Writer) (int, bool) {
	for {
		if file, ok := w.(*os.File); ok {
			size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
			if err == nil && size != nil && size.Col > 0 {
				return int(size.Col), true
			}
			return 0, false
		}
		wrappedWriter, ok := w.(writerUnwrapper)
		if !ok {
			return 0, false
		}
		w = wrappedWriter.UnwrapWriter()
	}
}

func runningInsideCodingAgentHarness() bool {
	for _, environmentVariableName := range []string{
		"T3CODE_THREAD_ID",
		"CLAUDECODE",
		"CODEX_THREAD_ID",
		"ANTIGRAVITY_AGENT",
	} {
		if strings.TrimSpace(os.Getenv(environmentVariableName)) != "" {
			return true
		}
	}
	return false
}

func DisplayWidth(value string) int {
	return uniseg.StringWidth(expandTabs(value))
}

// Truncate cuts value to width display cells, always on a grapheme-cluster
// boundary. A family emoji or a flag is one cluster made of several runes
// joined by zero-width joiners or variation selectors; cutting inside one
// leaves a dangling fragment whose rendered width no terminal agrees on,
// which is what misaligns the column after it. Clustering first keeps every
// cut clean.
func Truncate(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 {
		return ""
	}
	if DisplayWidth(value) <= width {
		return value
	}
	ellipsis := "…"
	ellipsisWidth := DisplayWidth(ellipsis)
	if width <= ellipsisWidth {
		return ellipsis
	}
	limit := width - ellipsisWidth
	var out strings.Builder
	cellWidth := 0
	clusters := uniseg.NewGraphemes(value)
	for clusters.Next() {
		cluster := clusters.Str()
		clusterWidth := DisplayWidth(cluster)
		if cellWidth+clusterWidth > limit {
			break
		}
		out.WriteString(cluster)
		cellWidth += clusterWidth
	}
	return strings.TrimRightFunc(out.String(), unicode.IsSpace) + ellipsis
}

// clipToWidth returns the longest prefix of value that fits in width display
// cells, trimming a trailing space so a clip never ends mid-gap. It adds no
// marker: callers that want an ellipsis append their own.
func clipToWidth(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(value) <= width {
		return value
	}
	var out strings.Builder
	cellWidth := 0
	clusters := uniseg.NewGraphemes(value)
	for clusters.Next() {
		cluster := clusters.Str()
		clusterWidth := DisplayWidth(cluster)
		if cellWidth+clusterWidth > width {
			break
		}
		out.WriteString(cluster)
		cellWidth += clusterWidth
	}
	return strings.TrimRightFunc(out.String(), unicode.IsSpace)
}

func Wrap(value string, width int) []string {
	value = normalizeText(value)
	if width <= 0 {
		return []string{value}
	}
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapLine(line, width)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func WrapWithIndent(prefix, value string, width int, continuationIndent string) []string {
	if continuationIndent == "" {
		continuationIndent = strings.Repeat(" ", DisplayWidth(prefix))
	}
	value = normalizeText(value)
	if strings.TrimSpace(value) == "" {
		return []string{strings.TrimRight(prefix, " ")}
	}
	var out []string
	nextPrefix := prefix
	for _, logicalLine := range strings.Split(value, "\n") {
		if len(out) > 0 && logicalLine == "" {
			out = append(out, strings.TrimRight(nextPrefix, " "))
			nextPrefix = continuationIndent
			continue
		}
		lineWidth := width - DisplayWidth(nextPrefix)
		if lineWidth < 1 {
			lineWidth = 1
		}
		for _, line := range wrapLine(logicalLine, lineWidth) {
			out = append(out, nextPrefix+line)
			nextPrefix = continuationIndent
		}
	}
	return out
}

func writeWrappedField(w io.Writer, label, value string) error {
	return writeWrappedFieldAtOutputWidth(w, label, value, OutputWidth(w))
}

func writeWrappedFieldAtOutputWidth(w io.Writer, label, value string, outputWidth int) error {
	prefix := displayLabel(label) + ": "
	return writeLines(w, WrapWithIndent(prefix, strings.TrimSpace(value), outputWidth, ""))
}

func normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return expandTabs(value)
}

func expandTabs(value string) string {
	return strings.ReplaceAll(value, "\t", "    ")
}

func wrapLine(line string, width int) []string {
	if line == "" || DisplayWidth(line) <= width {
		return []string{line}
	}
	var out []string
	for DisplayWidth(line) > width {
		partEnd, nextStart := splitLineAtWidth(line, width)
		part := strings.TrimRightFunc(line[:partEnd], unicode.IsSpace)
		if part == "" {
			part = line[:nextStart]
		}
		out = append(out, part)
		line = strings.TrimLeftFunc(line[nextStart:], unicode.IsSpace)
		if line == "" {
			return out
		}
	}
	return append(out, line)
}

// splitLineAtWidth breaks at the last space that fits. A time is never
// broken, and a line never ends in the arrow between senders and recipients:
// "→ me" moves to the next line whole.
func splitLineAtWidth(line string, width int) (partEnd int, nextStart int) {
	writtenTimeSpans := writtenArchiveEventTimePattern.FindAllStringIndex(line, -1)
	spaceIsInsideWrittenTime := func(spaceStart int) bool {
		for _, writtenTimeSpan := range writtenTimeSpans {
			if spaceStart > writtenTimeSpan[0] && spaceStart < writtenTimeSpan[1] {
				return true
			}
		}
		return false
	}
	cellWidth := 0
	lastSpaceStart := -1
	lastSpaceEnd := -1
	clusters := uniseg.NewGraphemes(line)
	for clusters.Next() {
		cluster := clusters.Str()
		clusterStart, clusterEnd := clusters.Positions()
		clusterWidth := DisplayWidth(cluster)
		if strings.TrimSpace(cluster) == "" &&
			!strings.HasSuffix(line[:clusterStart], strings.TrimRight(sendersToRecipientsSeparator, " ")) &&
			!spaceIsInsideWrittenTime(clusterStart) {
			lastSpaceStart = clusterStart
			lastSpaceEnd = clusterEnd
		}
		if cellWidth+clusterWidth > width {
			if lastSpaceStart > 0 {
				return lastSpaceStart, lastSpaceEnd
			}
			if webAddressEnd, lineStartsWithWebAddress := endOfWebAddressAtLineStart(line); lineStartsWithWebAddress {
				return webAddressEnd, webAddressEnd
			}
			if clusterStart == 0 {
				return clusterEnd, clusterEnd
			}
			return clusterStart, clusterStart
		}
		cellWidth += clusterWidth
	}
	return len(line), len(line)
}

// endOfWebAddressAtLineStart keeps a web address whole on its own line, even
// past the width, because a split address no longer works when copied.
func endOfWebAddressAtLineStart(line string) (int, bool) {
	firstWordEnd := strings.IndexFunc(line, unicode.IsSpace)
	if firstWordEnd < 0 {
		firstWordEnd = len(line)
	}
	return firstWordEnd, strings.Contains(line[:firstWordEnd], "://")
}
