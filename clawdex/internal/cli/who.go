package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/index"
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
	Who          string   `json:"who"`
	Identifiers  []string `json:"identifiers"`
	Sources      []string `json:"sources"`
	LastSeen     string   `json:"last_seen,omitempty"`
	MatchQuality string   `json:"match_quality,omitempty"`
	Identity     string   `json:"identity,omitempty"`
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
	return printWhoTable(r.stdout, envelope.Candidates)
}

func whoCandidates(candidates []index.WhoCandidate) []whoCandidate {
	out := make([]whoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidate{
			Who:          candidate.Who,
			Identifiers:  append([]string(nil), candidate.Identifiers...),
			Sources:      append([]string(nil), candidate.Sources...),
			LastSeen:     candidate.LastSeen,
			MatchQuality: candidate.MatchQuality,
			Identity:     candidate.Who,
		})
	}
	return out
}

func printWhoTable(w io.Writer, candidates []whoCandidate) error {
	if len(candidates) == 0 {
		_, err := fmt.Fprintln(w, "No people found.")
		return err
	}
	width := textOutputWidth(w)
	columns := whoTableColumns(width)
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			candidate.MatchQuality,
			formatWhoLastSeen(candidate.LastSeen),
			strings.Join(candidate.Sources, ", "),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	return renderTextTable(w, columns, rows)
}

func whoTableColumns(width int) []textColumn {
	whoWidth := 24
	sourceWidth := 20
	if width < 90 {
		whoWidth = 18
		sourceWidth = 14
	}
	identifierWidth := textColumnWidth(width, whoWidth, 8, 10, sourceWidth)
	return []textColumn{
		{header: "WHO", width: whoWidth},
		{header: "MATCH", width: 8},
		{header: "LAST SEEN", width: 10},
		{header: "SOURCES", width: sourceWidth, wrap: true},
		{header: "IDENTIFIERS", width: identifierWidth, wrap: true},
	}
}

func formatWhoLastSeen(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC().Format("2006-01-02")
		}
	}
	return value
}
