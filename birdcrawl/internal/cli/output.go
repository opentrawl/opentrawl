package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
)

func (r *runtime) print(value any) error {
	if r.json {
		enc := json.NewEncoder(r.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
	switch v := value.(type) {
	case control.Manifest:
		return r.printManifest(v)
	case statusEnvelope:
		return r.printStatus(v)
	case doctorOutput:
		return r.printDoctor(v)
	case searchEnvelope:
		return r.printSearch(v)
	case openEnvelope:
		return r.printOpen(v)
	case importEnvelope:
		return r.printImport(v)
	case statsEnvelope:
		return r.printStats(v)
	default:
		enc := json.NewEncoder(r.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(value)
	}
}

func (r *runtime) printManifest(value control.Manifest) error {
	if _, err := fmt.Fprintf(r.stdout, "%s: %s\nversion: %s\n", value.ID, value.Description, value.Version); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "database: %s\nlogs: %s\n", value.Paths.DefaultDatabase, value.Paths.DefaultLogs); err != nil {
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "capabilities: %s\n", strings.Join(value.Capabilities, ", "))
	return err
}

func (r *runtime) printStatus(value statusEnvelope) error {
	return render.WriteStatus(r.stdout, render.Status{
		State:   render.StatusState(value.State),
		Summary: value.Summary,
		Sections: []render.Section{
			{Title: "Archive", Fields: statusRenderFields(value.Counts)},
			{Title: "Spend", Fields: []render.Field{
				{Label: "Month", Value: value.Spend.Month},
				{Label: "Spent", Value: "$" + value.Spend.SpentUSD},
				{Label: "Cap", Value: "$" + value.Spend.MonthlyBudgetUSD},
				{Label: "Remaining", Value: "$" + value.Spend.RemainingUSD},
			}},
			{Title: "Auth", Fields: []render.Field{
				{Label: "Credentials present", Value: strconv.FormatBool(value.Auth.CredentialsPresent)},
				{Label: "Token valid at last sync", Value: strconv.FormatBool(value.Auth.TokenValidAtLastSync)},
			}},
		},
		Freshness: statusRenderFreshness(value.Freshness),
		Log:       value.logTail,
	})
}

func statusRenderFields(counts []countEnvelope) []render.Field {
	fields := make([]render.Field, 0, len(counts))
	for _, count := range counts {
		fields = append(fields, render.Field{Label: humanLabel(count.Label), Value: groupDigits64(count.Value)})
	}
	return fields
}

func statusRenderFreshness(value freshnessEnvelope) *render.Freshness {
	switch {
	case value.LastSync != "":
		return &render.Freshness{LastSync: value.LastSync}
	case value.LastImport != "":
		return &render.Freshness{LastSync: value.LastImport, State: "archive import only"}
	default:
		return nil
	}
}

func (r *runtime) printDoctor(value doctorOutput) error {
	return render.WriteDoctor(r.stdout, doctorRenderChecks(value.Checks), value.logTail)
}

func doctorRenderChecks(checks []doctorCheck) []render.Check {
	out := make([]render.Check, 0, len(checks))
	for _, check := range checks {
		out = append(out, render.Check{
			Name:    check.ID,
			State:   render.CheckState(check.State),
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return out
}

func (r *runtime) printSearch(value searchEnvelope) error {
	for _, item := range value.Results {
		if _, err := fmt.Fprintf(r.stdout, "%s %s in %s\n%s\nref: %s\n\n", item.Time, item.Who, item.Where, item.Snippet, item.Ref); err != nil {
			return err
		}
	}
	if value.Truncated {
		_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches; narrow with --limit, --after, or --before\n", len(value.Results), value.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches\n", len(value.Results), value.TotalMatches)
	return err
}

func (r *runtime) printOpen(value openEnvelope) error {
	if _, err := fmt.Fprintf(r.stdout, "ref: %s\n", value.Ref); err != nil {
		return err
	}
	if err := printOpenTweet(r.stdout, "tweet", value.Tweet); err != nil {
		return err
	}
	if len(value.Ancestors) > 0 {
		if _, err := io.WriteString(r.stdout, "\nAncestors:\n"); err != nil {
			return err
		}
		for _, tweet := range value.Ancestors {
			if err := printOpenTweet(r.stdout, "-", tweet); err != nil {
				return err
			}
		}
	}
	if len(value.Replies) > 0 {
		if _, err := io.WriteString(r.stdout, "\nReplies:\n"); err != nil {
			return err
		}
		for _, tweet := range value.Replies {
			if err := printOpenTweet(r.stdout, "-", tweet); err != nil {
				return err
			}
		}
	}
	if value.AncestorsTruncated || value.RepliesTruncated {
		_, err := io.WriteString(r.stdout, "\ncontext is bounded; more tweets omitted\n")
		return err
	}
	return nil
}

func printOpenTweet(w io.Writer, label string, tweet openTweet) error {
	if tweet.Unavailable {
		_, err := fmt.Fprintf(w, "%s %s: %s\n", label, tweet.Ref, tweet.Text)
		return err
	}
	_, err := fmt.Fprintf(w, "%s %s %s\n%s\n", label, tweet.Time, tweet.Who, tweet.Text)
	return err
}

func humanLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
