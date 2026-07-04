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
	return writeWhoTable(a.stdout, result.Candidates)
}

func writeWhoTable(w io.Writer, candidates []store.WhoCandidate) error {
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			outputField(candidate.Who),
			shortLocalTime(candidate.LastSeen),
			strconv.Itoa(candidate.Messages),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who"},
		{Header: "last seen"},
		{Header: "messages", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
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
