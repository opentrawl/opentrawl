package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runWho(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("who takes a name"))
	}
	query := normalizeCLIWords(strings.Join(args, " "))
	if query == "" {
		return usageErr(errors.New("who takes a name"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		candidates, err := st.ResolveWho(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(newWhoEnvelope(query, candidates))
	})
}

func (r *runtime) ambiguousWhoError(query, who string, candidates []store.WhoCandidate) error {
	body := contractErrorBody{
		Code:       "ambiguous_who",
		Message:    "--who matched more than one person",
		Remedy:     "Retry with one identifier from candidates.",
		Candidates: whoCandidates(candidates),
	}
	return r.contractBodyError(4, body, ambiguousWhoText(query, who, candidates))
}

func (r *runtime) unknownWhoError(who string, didYouMean []store.WhoCandidate) error {
	candidates := whoCandidates(didYouMean)
	body := contractErrorBody{
		Code:    "unknown_who",
		Message: "--who did not match a person",
		Remedy:  "Run telecrawl who <name>, or search without --who to check whether matching messages exist.",
		Hint:    "Search without --who to check whether matching messages exist.",
	}
	body.DidYouMean = &candidates
	return r.contractBodyError(5, body, unknownWhoText(who, didYouMean))
}

func ambiguousWhoText(query, who string, candidates []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "ambiguous --who %q: %d people match.\n\n", who, len(candidates))
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
	parts := []string{"telecrawl", "search"}
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
		{Header: "Who"},
		{Header: "Last seen"},
		{Header: "Messages", AlignRight: true},
		{Header: "Identifiers", Wrap: true, Width: 0},
	}, rows)
}
