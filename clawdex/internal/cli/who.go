package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/index"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/crawlkit/render"
)

type WhoCmd struct {
	Query []string `arg:"" name:"query" help:"Name, alias, email, phone, or handle fragment"`
}

func (WhoCmd) Help() string {
	return `Examples:
  clawdex who alice
  clawdex who alice@example.com --json`
}

type whoEnvelope struct {
	Query      string         `json:"query"`
	Candidates []whoCandidate `json:"candidates"`
}

type whoCandidate struct {
	Who          string               `json:"who"`
	Identifiers  []string             `json:"identifiers"`
	Addresses    []model.ContactValue `json:"addresses,omitempty"`
	Sources      []string             `json:"sources"`
	LastSeen     string               `json:"last_seen,omitempty"`
	MatchQuality string               `json:"match_quality,omitempty"`
	Identity     string               `json:"identity,omitempty"`
}

func (c *WhoCmd) Run(r *Runtime) error {
	queryWords := make([]string, 0, len(c.Query))
	for _, word := range c.Query {
		if word == "--json" {
			r.root.JSON = true
			continue
		}
		queryWords = append(queryWords, word)
	}
	query := strings.Join(queryWords, " ")
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return usageErr{fmt.Errorf("who requires a name fragment")}
	}
	store := r.readOnlyStore()
	store.Log = nil
	candidates, err := store.ResolvePeople(query)
	if err != nil {
		return err
	}
	envelope := whoEnvelope{
		Query:      query,
		Candidates: whoCandidates(candidates),
	}
	if r.root.JSON {
		return r.print(envelope)
	}
	return printWhoTable(r.stdout, envelope)
}

func whoCandidates(candidates []index.WhoCandidate) []whoCandidate {
	out := make([]whoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidate{
			Who:          candidate.Who,
			Identifiers:  append([]string(nil), candidate.Identifiers...),
			Addresses:    append([]model.ContactValue(nil), candidate.Addresses...),
			Sources:      append([]string(nil), candidate.Sources...),
			LastSeen:     candidate.LastSeen,
			MatchQuality: candidate.MatchQuality,
			Identity:     candidate.Who,
		})
	}
	return out
}

func printWhoTable(w io.Writer, envelope whoEnvelope) error {
	if len(envelope.Candidates) == 0 {
		_, err := fmt.Fprintf(w, "No people match %q. Search instead: clawdex search %q\n", envelope.Query, envelope.Query)
		return err
	}
	if _, err := fmt.Fprintf(w, "Who is %q: %s, best match first.\n", envelope.Query, countNoun(len(envelope.Candidates), "person", "people")); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Show one: clawdex person show NAME"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	// The contract's who table: who, last seen, sources, identifiers.
	// Match quality stays in JSON; rows are already best match first.
	rows := make([][]string, 0, len(envelope.Candidates))
	for _, candidate := range envelope.Candidates {
		rows = append(rows, []string{
			candidate.Who,
			formatWhoLastSeen(candidate.LastSeen),
			strings.Join(candidate.Sources, ", "),
			strings.Join(humanIdentifiers(candidate.Identifiers), ", "),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who"},
		{Header: "last seen"},
		{Header: "sources", Wrap: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

// humanIdentifiers hides internal sync record refs (apple:UUID:abperson,
// google:people/…) from the human table: no one can read or retype them.
// JSON keeps the full list for federation.
func humanIdentifiers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "apple:") || strings.HasPrefix(value, "google:") {
			continue
		}
		out = append(out, value)
	}
	return out
}

func formatWhoLastSeen(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return render.ShortLocalTime(parsed)
		}
	}
	return value
}
