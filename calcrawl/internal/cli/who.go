package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type whoOutput struct {
	Query      string                 `json:"query"`
	Candidates []archive.WhoCandidate `json:"candidates"`
}

func (r *runtime) runWho(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"who"})
	}
	query, err := oneArg(args, "who")
	if err != nil {
		return err
	}
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	candidates, err := st.ResolveWho(r.ctx, query)
	if err != nil {
		return err
	}
	return r.print(whoOutput{Query: normalizeIdentity(query), Candidates: candidates})
}

func (r *runtime) resolveSearchWho(st *archive.Store, query, who string) (archive.WhoCandidate, error) {
	candidates, err := st.ResolveWho(r.ctx, who)
	if err != nil {
		return archive.WhoCandidate{}, err
	}
	resolved := resolvableWhoCandidates(who, candidates)
	switch len(resolved) {
	case 0:
		return archive.WhoCandidate{}, unknownWhoError(query, who, candidates)
	case 1:
		return resolved[0], nil
	default:
		return archive.WhoCandidate{}, ambiguousWhoError(query, who, resolved)
	}
}

func resolvableWhoCandidates(query string, candidates []archive.WhoCandidate) []archive.WhoCandidate {
	out := make([]archive.WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ResolvesWho(query) {
			out = append(out, candidate)
		}
	}
	return out
}

func ambiguousWhoError(query, who string, candidates []archive.WhoCandidate) error {
	err := errors.New("ambiguous --who " + quote(who))
	copied := append([]archive.WhoCandidate(nil), candidates...)
	return &cliError{
		code:       4,
		err:        err,
		kind:       "ambiguous_who",
		human:      renderAmbiguousWho(query, who, candidates),
		candidates: &copied,
	}
}

func unknownWhoError(query, who string, didYouMean []archive.WhoCandidate) error {
	err := errors.New("unknown --who " + quote(who))
	copied := append([]archive.WhoCandidate(nil), didYouMean...)
	hint := ""
	if len(copied) == 0 {
		hint = "Search without --who to check whether the text exists."
	}
	return &cliError{
		code:       5,
		err:        err,
		kind:       "unknown_who",
		remedy:     hint,
		human:      renderUnknownWho(query, who, didYouMean),
		didYouMean: &copied,
		hint:       hint,
	}
}

func printWhoText(w io.Writer, value whoOutput) error {
	if len(value.Candidates) == 0 {
		_, err := fmt.Fprintf(w, "No people matched %q.\n", value.Query)
		return err
	}
	return writeWhoTable(w, value.Candidates)
}

func renderAmbiguousWho(query, who string, candidates []archive.WhoCandidate) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "--who %q matched more than one person.\n\n", who)
	_ = writeWhoTable(&b, candidates)
	if example := retryExample(query, candidates); example != "" {
		_, _ = fmt.Fprintf(&b, "\nRetry with an identifier: %s\n", example)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderUnknownWho(query, who string, didYouMean []archive.WhoCandidate) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "No person matched --who %q.\n", who)
	if len(didYouMean) > 0 {
		_, _ = io.WriteString(&b, "\nDid you mean:\n")
		_ = writeWhoTable(&b, didYouMean)
		if example := retryExample(query, didYouMean); example != "" {
			_, _ = fmt.Fprintf(&b, "\nRetry with an identifier: %s\n", example)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	_, _ = fmt.Fprintf(&b, "Search without --who to check whether the text exists: %s\n", searchWithoutWhoExample(query))
	return strings.TrimRight(b.String(), "\n")
}

func writeWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "who\tidentifiers\tlast_seen\tmessages"); err != nil {
		return err
	}
	for _, candidate := range candidates {
		identifiers := strings.Join(candidate.Identifiers, ", ")
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", candidate.Who, identifiers, candidate.LastSeen, candidate.Messages); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func retryExample(query string, candidates []archive.WhoCandidate) string {
	for _, candidate := range candidates {
		if len(candidate.Identifiers) == 0 {
			continue
		}
		return searchWithWhoExample(query, candidate.Identifiers[0])
	}
	return ""
}

func searchWithWhoExample(query, identifier string) string {
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("calcrawl search --who %s", shellQuote(identifier))
	}
	return fmt.Sprintf("calcrawl search %s --who %s", shellQuote(query), shellQuote(identifier))
}

func searchWithoutWhoExample(query string) string {
	if strings.TrimSpace(query) == "" {
		return "calcrawl search QUERY"
	}
	return fmt.Sprintf("calcrawl search %s", shellQuote(query))
}

func shellQuote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if !strings.ContainsAny(value, " \t\n'\"") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func quote(value string) string {
	return fmt.Sprintf("%q", value)
}
