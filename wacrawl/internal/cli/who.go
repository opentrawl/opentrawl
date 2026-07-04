package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/openclaw/wacrawl/internal/store"
)

func (a *app) runWho(ctx context.Context, args []string) error {
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "who")
		return nil
	}
	fs := flag.NewFlagSet("who", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "who")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 1 {
		return usageErr(errors.New("who requires exactly one name"))
	}
	query := normalizeWhoValue(fs.Arg(0))
	if query == "" {
		return usageErr(errors.New("who requires a name"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		resolution, err := st.ResolveWho(ctx, query)
		if err != nil {
			return err
		}
		return a.print(whoEnvelope{Query: query, Candidates: resolution.Candidates})
	})
}

type whoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type whoEnvelope struct {
	Query      string               `json:"query"`
	Candidates []store.WhoCandidate `json:"candidates"`
}

func normalizeWhoValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (a *app) printWho(result whoEnvelope) error {
	if len(result.Candidates) == 0 {
		_, err := fmt.Fprintf(a.stdout, "No people matched %q.\n", result.Query)
		return err
	}
	return writeWhoCandidateTable(a.stdout, result.Candidates, terminalColumns())
}

func writeWhoCandidateTable(w io.Writer, candidates []store.WhoCandidate, width int) error {
	if width < 42 {
		width = 42
	}
	lastHeader := "Last seen"
	messagesHeader := "Messages"
	identifiersHeader := "Identifiers"
	whoWidth := clampInt(width/4, 14, 28)
	lastWidth := 25
	messagesWidth := len(messagesHeader)
	if width < 72 {
		lastHeader = "Last"
		messagesHeader = "Msgs"
		identifiersHeader = "IDs"
		whoWidth = clampInt(width/4, 8, 18)
		lastWidth = 16
		messagesWidth = len(messagesHeader)
	}
	gaps := 6
	identifiersWidth := width - whoWidth - lastWidth - messagesWidth - gaps
	if identifiersWidth < 10 {
		identifiersWidth = 10
		whoWidth = maxInt(6, width-lastWidth-messagesWidth-identifiersWidth-gaps)
	}
	if _, err := fmt.Fprintf(w, "%-*s  %-*s  %*s  %s\n", whoWidth, "Who", lastWidth, lastHeader, messagesWidth, messagesHeader, identifiersHeader); err != nil {
		return err
	}
	for _, candidate := range candidates {
		identifiers := wrapTableCell(strings.Join(candidate.Identifiers, ", "), identifiersWidth)
		if len(identifiers) == 0 {
			identifiers = []string{"-"}
		}
		for i, identifierLine := range identifiers {
			who := ""
			lastSeen := ""
			messages := ""
			if i == 0 {
				who = candidate.Who
				lastSeen = formatTime(candidate.LastSeen)
				messages = strconv.Itoa(candidate.Messages)
			}
			if _, err := fmt.Fprintf(w, "%-*s  %-*s  %*s  %s\n", whoWidth, fitTableCell(who, whoWidth), lastWidth, fitTableCell(lastSeen, lastWidth), messagesWidth, messages, identifierLine); err != nil {
				return err
			}
		}
	}
	return nil
}

func wrapTableCell(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(value) {
		word = strings.TrimSuffix(word, ",") + ","
		if line == "" {
			out = appendWrappedWord(out, &line, word, width)
			continue
		}
		if runeLen(line)+1+runeLen(word) > width {
			out = append(out, strings.TrimSuffix(line, ","))
			line = ""
		}
		out = appendWrappedWord(out, &line, word, width)
	}
	if line != "" {
		out = append(out, strings.TrimSuffix(line, ","))
	}
	return out
}

func appendWrappedWord(out []string, line *string, word string, width int) []string {
	for runeLen(word) > width {
		if *line != "" {
			out = append(out, strings.TrimSuffix(*line, ","))
			*line = ""
		}
		chunk, rest := splitRunes(word, width)
		out = append(out, strings.TrimSuffix(chunk, ","))
		word = rest
	}
	if *line == "" {
		*line = word
	} else {
		*line += " " + word
	}
	return out
}

func splitRunes(value string, width int) (string, string) {
	runes := []rune(value)
	if width >= len(runes) {
		return value, ""
	}
	return string(runes[:width]), string(runes[width:])
}

func fitTableCell(value string, width int) string {
	value = strings.TrimSpace(value)
	if runeLen(value) <= width {
		return value
	}
	if width <= 1 {
		return string([]rune(value)[:width])
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}

func terminalColumns() int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && value > 0 {
		return value
	}
	return 100
}

func runeLen(value string) int {
	return len([]rune(value))
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func newWhoResolved(candidate store.WhoCandidate) *whoResolved {
	return &whoResolved{Who: candidate.Who, Identifiers: candidate.Identifiers}
}

func ambiguousWhoError(value, query string, candidates []store.WhoCandidate) contractError {
	return contractError{
		Code:       "ambiguous_who",
		Message:    fmt.Sprintf("more than one person matched %q", value),
		Remedy:     searchWhoRetryExample(firstCandidateIdentifier(candidates), query),
		Candidates: candidates,
	}
}

func unknownWhoError(value string, didYouMean []store.WhoCandidate) contractError {
	if didYouMean == nil {
		didYouMean = []store.WhoCandidate{}
	}
	err := contractError{
		Code:       "unknown_who",
		Message:    fmt.Sprintf("no person matched %q", value),
		Remedy:     "run wacrawl who NAME or search without --who",
		DidYouMean: &didYouMean,
	}
	if len(didYouMean) == 0 {
		err.Hint = "search without --who to find messages that mention this text"
	}
	return err
}

func firstCandidateIdentifier(candidates []store.WhoCandidate) string {
	for _, candidate := range candidates {
		if len(candidate.Identifiers) > 0 {
			return candidate.Identifiers[0]
		}
		if strings.TrimSpace(candidate.Who) != "" {
			return candidate.Who
		}
	}
	return "IDENTIFIER"
}

func searchWhoRetryExample(identifier, query string) string {
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("retry: wacrawl search --who %q", identifier)
	}
	return fmt.Sprintf("retry: wacrawl search --who %q %q", identifier, query)
}
