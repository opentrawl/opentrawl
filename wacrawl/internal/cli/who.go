package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
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
	return writeWhoCandidateTable(a.stdout, result.Candidates, render.OutputWidth(a.stdout))
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
		identifiers := whoIdentifierLines(candidate.Identifiers, identifiersWidth)
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
			if _, err := fmt.Fprintf(w, "%-*s  %-*s  %*s  %s\n", whoWidth, render.Truncate(who, whoWidth), lastWidth, render.Truncate(lastSeen, lastWidth), messagesWidth, messages, identifierLine); err != nil {
				return err
			}
		}
	}
	return nil
}

func whoIdentifierLines(identifiers []string, width int) []string {
	var out []string
	line := ""
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		identifier = render.Truncate(identifier, width)
		if line == "" {
			line = identifier
			continue
		}
		next := line + ", " + identifier
		if render.DisplayWidth(next) > width {
			out = append(out, line)
			line = identifier
			continue
		}
		line = next
	}
	if line != "" {
		out = append(out, line)
	}
	return out
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
