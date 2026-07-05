package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	ckrender "github.com/openclaw/crawlkit/render"
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
	copied := append([]archive.WhoCandidate(nil), candidates...)
	return &cliError{
		code:    4,
		name:    "ambiguous_who",
		message: "ambiguous --who " + quote(who),
		human:   renderAmbiguousWho(query, who, candidates),
		fields:  map[string]any{"candidates": copied},
	}
}

func unknownWhoError(query, who string, didYouMean []archive.WhoCandidate) error {
	copied := append([]archive.WhoCandidate(nil), didYouMean...)
	fields := map[string]any{}
	hint := ""
	if len(copied) == 0 {
		hint = "Search without --who to check whether the text exists."
		fields["hint"] = hint
	} else {
		fields["did_you_mean"] = copied
	}
	return &cliError{
		code:    5,
		name:    "unknown_who",
		message: "unknown --who " + quote(who),
		remedy:  hint,
		human:   renderUnknownWho(query, who, didYouMean),
		fields:  fields,
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
	if hint := retryHint(query, candidates); hint != "" {
		_, _ = fmt.Fprintf(&b, "\n%s\n", hint)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderUnknownWho(query, who string, didYouMean []archive.WhoCandidate) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "No person matched --who %q.\n", who)
	if len(didYouMean) > 0 {
		_, _ = io.WriteString(&b, "\nDid you mean:\n")
		_ = writeWhoTable(&b, didYouMean)
		if hint := retryHint(query, didYouMean); hint != "" {
			_, _ = fmt.Fprintf(&b, "\n%s\n", hint)
		}
		return strings.TrimRight(b.String(), "\n")
	}
	_, _ = fmt.Fprintf(&b, "Search without --who to check whether the text exists: %s\n", searchWithoutWhoExample(query))
	return strings.TrimRight(b.String(), "\n")
}

func writeWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			shortLocalTime(parseEventTime(candidate.LastSeen)),
			strconv.FormatInt(candidate.Messages, 10),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	return ckrender.WriteTable(w, []ckrender.TableColumn{
		{Header: "who", Wrap: true},
		{Header: "last seen"},
		{Header: "events", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

// retryHint suggests one concrete retry. An identifier qualifies only when
// exactly one listed candidate carries it: a shared mailbox fronts several
// of them, so retrying it comes straight back to this list. When every
// identifier is shared, the name is the only thing that tells them apart.
func retryHint(query string, candidates []archive.WhoCandidate) string {
	carriers := map[string]int{}
	for _, candidate := range candidates {
		for _, identifier := range candidate.Identifiers {
			carriers[strings.ToLower(normalizeIdentity(identifier))]++
		}
	}
	for _, candidate := range candidates {
		for _, identifier := range candidate.Identifiers {
			if carriers[strings.ToLower(normalizeIdentity(identifier))] == 1 {
				return "Retry with an identifier: " + searchWithWhoExample(query, identifier)
			}
		}
	}
	for _, candidate := range candidates {
		if candidate.Who != "" {
			return "Retry with a name: " + searchWithWhoExample(query, candidate.Who)
		}
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
