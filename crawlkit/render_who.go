package crawlkit

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/crawlkit/whomatch"
)

type whoOutput struct {
	Query      string               `json:"query"`
	Candidates []whoCandidateOutput `json:"candidates"`
}

type whoCandidateOutput struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int64    `json:"messages"`
	lastSeen    time.Time
}

func writeWhoText(w io.Writer, value whoOutput) error {
	rows := make([][]string, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		rows = append(rows, []string{
			candidate.Who,
			render.ShortLocalTime(candidate.lastSeen),
			render.FormatInteger(candidate.Messages),
			strings.Join(humanIdentifiers(candidate.Identifiers), ", "),
		})
	}
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "No people matched %q.\n", value.Query)
		return err
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who", Wrap: true},
		{Header: "last seen"},
		{Header: "items", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

func newWhoOutput(query string, candidates []whomatch.Candidate) whoOutput {
	return whoOutput{Query: strings.Join(strings.Fields(query), " "), Candidates: whoCandidateOutputs(candidates)}
}

func whoCandidateOutputs(candidates []whomatch.Candidate) []whoCandidateOutput {
	out := make([]whoCandidateOutput, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidateOutput{
			Who:         strings.Join(strings.Fields(candidate.Who), " "),
			Identifiers: append([]string{}, candidate.Identifiers...),
			LastSeen:    formatContractTime(candidate.LastSeen),
			Messages:    candidate.Messages,
			lastSeen:    candidate.LastSeen,
		})
	}
	return out
}

func newWhoResolved(candidate whomatch.Candidate) *WhoResolved {
	return &WhoResolved{
		Who:         strings.Join(strings.Fields(candidate.Who), " "),
		Identifiers: append([]string{}, candidate.Identifiers...),
	}
}

func formatContractTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func writeWhoResolutionErrorText(w io.Writer, err error, body output.ErrorBody) bool {
	var whoErr whoAmbiguityError
	if !errors.As(err, &whoErr) {
		return false
	}
	_, _ = fmt.Fprintf(w, "Error: %s\n", body.Message)
	candidates := whoCandidateOutputs(whoErr.candidates)
	switch {
	case whoErr.code == 5 && len(candidates) == 0:
		if retry := retrySearchWithoutWho(whoErr.query); retry != "" {
			_, _ = fmt.Fprintf(w, "\nRetry without --who: %s\n", retry)
		} else if strings.TrimSpace(body.Remedy) != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n", body.Remedy)
		}
	case whoErr.code == 5:
		_, _ = fmt.Fprintln(w, "\nDid you mean:")
		_ = writeWhoText(w, whoOutput{Query: whoErr.who, Candidates: candidates})
		_, _ = fmt.Fprintf(w, "\nRetry with a suggestion: %s\n", retrySearchWithWho(whoErr.query, candidates[0]))
	case len(candidates) == 0:
		if strings.TrimSpace(body.Remedy) != "" {
			_, _ = fmt.Fprintf(w, "\n%s\n", body.Remedy)
		}
	default:
		_, _ = fmt.Fprintln(w)
		_ = writeWhoText(w, whoOutput{Query: whoErr.who, Candidates: candidates})
		_, _ = fmt.Fprintf(w, "\nRetry with one listed identifier: %s\n", retrySearchWithWho(whoErr.query, candidates[0]))
	}
	return true
}

func retrySearchWithWho(query string, candidate whoCandidateOutput) string {
	parts := []string{"search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteRetryArg(query))
	}
	parts = append(parts, "--who", quoteRetryArg(candidateWhoFilter(candidate)))
	return strings.Join(parts, " ")
}

func retrySearchWithoutWho(query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	return "search " + quoteRetryArg(query)
}

func candidateWhoFilter(candidate whoCandidateOutput) string {
	for _, identifier := range candidate.Identifiers {
		if strings.TrimSpace(identifier) != "" {
			return identifier
		}
	}
	return candidate.Who
}

func quoteRetryArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\"'") {
		return strconv.Quote(value)
	}
	return value
}

func humanIdentifiers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, render.HumanIdentity(value))
	}
	return out
}
