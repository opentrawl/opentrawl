package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/openclaw/telecrawl/internal/store"
)

func (c *Crawler) Who(ctx context.Context, req *crawlkit.Request, person string) ([]whomatch.Candidate, error) {
	query := normalizeWords(person)
	if query == "" {
		return nil, usageErr(errors.New("who takes a name"))
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	candidates, err := st.ResolveWho(ctx, query)
	if err != nil {
		return nil, err
	}
	return whoMatchCandidates(candidates), nil
}

func whoMatchCandidates(candidates []store.WhoCandidate) []whomatch.Candidate {
	out := make([]whomatch.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    candidate.LastSeen,
			Messages:    int64(candidate.Messages),
		})
	}
	return out
}

func (r *runtime) ambiguousWhoError(query, who string, candidates []store.WhoCandidate) error {
	return commandErrFields(4, "ambiguous_who", fmt.Errorf("ambiguous --who %q", who), "Retry with one identifier from candidates.", map[string]any{"candidates": whoCandidates(candidates)})
}

func (r *runtime) unknownWhoError(who string, didYouMean []store.WhoCandidate) error {
	hint := "Search without --who to check whether matching messages exist."
	fields := map[string]any{"hint": hint}
	if len(didYouMean) > 0 {
		fields["did_you_mean"] = whoCandidates(didYouMean)
	}
	return commandErrFields(5, "unknown_who", fmt.Errorf("unknown --who %q", who), "run trawl telegram who <name>, or search without --who to check whether matching messages exist.", fields)
}

func ambiguousWhoText(query, who string, candidates []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "ambiguous --who %q: %s people match.\n\n", who, render.FormatInteger(int64(len(candidates))))
	_ = writeWhoTable(&out, candidates)
	if retry := retrySearchExample(query, candidates); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func unknownWhoText(who string, didYouMean []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "unknown --who %q: no person matched.", who)
	if len(didYouMean) == 0 {
		out.WriteString("\nSearch without --who to check whether matching messages exist.")
		return out.String()
	}
	out.WriteString("\n\nDid you mean:\n")
	_ = writeWhoTable(&out, didYouMean)
	if retry := retrySearchExample("", didYouMean); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func retrySearchExample(query string, candidates []store.WhoCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	who := firstRetryIdentifier(candidates[0])
	if who == "" {
		return ""
	}
	parts := []string{"trawl", "telegram", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteShellArg(query))
	}
	parts = append(parts, "--who", quoteShellArg(who))
	return strings.Join(parts, " ")
}

func firstRetryIdentifier(candidate store.WhoCandidate) string {
	for _, identifier := range candidate.Identifiers {
		if strings.TrimSpace(identifier) != "" {
			return identifier
		}
	}
	return candidate.Who
}

func quoteShellArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"'") {
		return strconv.Quote(value)
	}
	return value
}

func (r *runtime) printWho(value whoEnvelope) error {
	candidates := make([]store.WhoCandidate, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		candidates = append(candidates, store.WhoCandidate{
			Who:         candidate.Who,
			Identifiers: candidate.Identifiers,
			LastSeen:    parseRenderTime(candidate.LastSeen),
			Messages:    candidate.Messages,
		})
	}
	if len(candidates) == 0 {
		_, err := fmt.Fprintf(r.stdout, "No people matched %q.\n", value.Query)
		return err
	}
	return writeWhoTable(r.stdout, candidates)
}

func writeWhoTable(w io.Writer, candidates []store.WhoCandidate) error {
	rows := make([][]string, 0, len(candidates)+1)
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
		{Header: "identifiers", Wrap: true, Width: 0},
	}, rows)
}
