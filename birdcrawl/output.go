package birdcrawl

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
	case spendEnvelope:
		return r.printSpend(v)
	case doctorOutput:
		return r.printDoctor(v)
	case searchEnvelope:
		return r.printSearch(v)
	case listEnvelope:
		return r.printList(v)
	case openEnvelope:
		return r.printOpen(v)
	case importEnvelope:
		return r.printImport(v)
	case statsEnvelope:
		return r.printStats(v)
	default:
		return fmt.Errorf("internal: no human renderer for %T", value)
	}
}

func (r *runtime) printSpend(value spendEnvelope) error {
	return render.WriteCard(r.stdout, render.Card{
		Title: "Monthly X API spend",
		Fields: []render.CardField{
			{Label: "Month", Value: value.Month},
			{Label: "Spent", Value: "$" + value.SpentUSD},
			{Label: "Cap", Value: "$" + value.MonthlyBudgetUSD},
			{Label: "Remaining", Value: "$" + value.RemainingUSD},
		},
	})
}

func (r *runtime) printManifest(value control.Manifest) error {
	name := strings.TrimSpace(value.DisplayName)
	if name == "" {
		name = value.ID
	}
	if _, err := fmt.Fprintf(r.stdout, "%s: %s\nversion: %s\n", name, value.Description, value.Version); err != nil {
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "database: %s\nlogs: %s\n", value.Paths.DefaultDatabase, value.Paths.DefaultLogs)
	return err
}

func (r *runtime) printStatus(value statusEnvelope) error {
	return render.WriteStatus(r.stdout, render.Status{
		State:   render.StatusState(value.State),
		Summary: value.humanSummary(),
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
		return &render.Freshness{LastSync: statusHumanTime(value.LastSync, value.lastSyncTime)}
	case value.LastImport != "":
		return &render.Freshness{LastSync: statusHumanTime(value.LastImport, value.lastImportTime), State: "archive import only"}
	default:
		return nil
	}
}

func statusHumanTime(value string, t time.Time) string {
	if !t.IsZero() {
		return render.ShortLocalTime(t)
	}
	return value
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
	items := make([]render.ListItem, 0, len(value.Results))
	for _, item := range value.Results {
		items = append(items, render.ListItem{
			Time: item.timeValue,
			Who:  humanWhoCell(item.rawWho, item.authorID, item.inReplyTo, item.inReplyToAuthorID, value.ownerAuthorID),
			Ref:  item.Ref,
			Text: item.Snippet,
		})
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   fmt.Sprintf("Search %q: showing %s of %s, newest first.", value.Query, render.FormatInteger(int64(len(value.Results))), render.FormatInteger(int64(value.TotalMatches))),
		Hints:     searchHints(value.Query, value.Limit, value.Truncated),
		Items:     items,
		ClampText: 2,
		Empty:     fmt.Sprintf("No matches for %q.", value.Query),
	})
}

func (r *runtime) printList(value listEnvelope) error {
	command := browseCommands[value.Kind]
	items := make([]render.ListItem, 0, len(value.Results))
	for _, item := range value.Results {
		items = append(items, render.ListItem{
			Time: item.timeValue,
			Who:  browseWho(item, value.ownerAuthorID),
			Ref:  item.Ref,
			Text: item.Text,
		})
	}
	return render.WriteList(r.stdout, render.List{
		Heading:   fmt.Sprintf("%s: showing %s of %s, newest first.", command.title, render.FormatInteger(int64(len(value.Results))), render.FormatInteger(int64(value.Total))),
		Hints:     browseHints(value.Kind, value.Limit, value.Truncated),
		Items:     items,
		ClampText: 0,
		Empty:     command.empty,
	})
}

func (r *runtime) printOpen(value openEnvelope) error {
	if err := render.WriteCard(r.stdout, render.Card{
		Title:  humanName(value.Tweet.Who, value.Tweet.authorID, value.ownerAuthorID) + " at " + render.ShortLocalTime(value.Tweet.timeValue),
		Fields: openCardFields(value),
		Body:   value.Tweet.Text,
	}); err != nil {
		return err
	}
	if err := r.printOpenContext("Earlier in thread", value.Ancestors, value.ownerAuthorID); err != nil {
		return err
	}
	if err := r.printOpenContext("Replies", value.Replies, value.ownerAuthorID); err != nil {
		return err
	}
	if value.AncestorsTruncated || value.RepliesTruncated {
		_, err := io.WriteString(r.stdout, "\ncontext is bounded; more tweets omitted\n")
		return err
	}
	return nil
}

func openCardFields(value openEnvelope) []render.CardField {
	fields := []render.CardField{
		{Label: "counts", Value: countsLine(value.Tweet)},
		{Label: "counts as of", Value: render.ShortLocalTime(value.Tweet.countsAsOfTime)},
		{Label: "ref", Value: value.Ref},
	}
	if value.Tweet.ReplyingTo != "" {
		fields = append([]render.CardField{{
			Label: "replying to",
			Value: humanName(value.Tweet.ReplyingTo, value.Tweet.replyingToAuthorID, value.ownerAuthorID),
		}}, fields...)
	}
	if value.Tweet.Note != "" {
		fields = append(fields, render.CardField{Label: "note", Value: value.Tweet.Note})
	}
	return fields
}

func (r *runtime) printOpenContext(title string, tweets []openTweet, ownerAuthorID string) error {
	if len(tweets) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(r.stdout, "\n%s:\n", title); err != nil {
		return err
	}
	width := render.OutputWidth(r.stdout)
	for _, tweet := range tweets {
		ref := tweet.Ref
		who := humanName(tweet.Who, tweet.authorID, ownerAuthorID)
		if tweet.Unavailable {
			who = "unavailable"
		}
		header := strings.Join(nonEmpty(ref, render.ShortLocalTime(tweet.timeValue), who), "  ")
		for _, line := range render.WrapWithIndent("  ", header, width, "  ") {
			if _, err := fmt.Fprintln(r.stdout, line); err != nil {
				return err
			}
		}
		for _, line := range render.WrapWithIndent("    ", tweet.Text, width, "    ") {
			if _, err := fmt.Fprintln(r.stdout, line); err != nil {
				return err
			}
		}
		if tweet.Note != "" {
			for _, line := range render.WrapWithIndent("    ", "note: "+tweet.Note, width, "    ") {
				if _, err := fmt.Fprintln(r.stdout, line); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *runtime) printStats(value statsEnvelope) error {
	if _, err := fmt.Fprintf(r.stdout, "Your top tweets by %s, last %s.\n", value.By, humanWindow(value.Window)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(r.stdout, "Showing %s of %s.\n", render.FormatInteger(int64(len(value.Results))), render.FormatInteger(int64(value.Population))); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout, statsFreshnessHint(value.Results)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout, "Open: trawl twitter open REF"); err != nil {
		return err
	}
	if value.Population > len(value.Results) {
		if _, err := fmt.Fprintf(r.stdout, "More: trawl twitter stats --by %s --limit %d\n", value.By, statsNextLimit(len(value.Results))); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Results))
	for _, row := range value.Results {
		rows = append(rows, []string{
			render.ShortLocalTime(row.timeValue),
			groupDigits64(row.Count),
			row.Ref,
			row.Text,
		})
	}
	return render.WriteTable(r.stdout, []render.TableColumn{
		{Header: "date", Width: 16},
		{Header: value.By, AlignRight: true},
		{Header: "ref"},
		{Header: "text", Wrap: true},
	}, rows)
}

func searchHints(query string, limit int, truncated bool) []string {
	hints := []string{"Open: trawl twitter open REF"}
	if truncated {
		hints = append(hints,
			"More: trawl twitter search "+quoteSearchQuery(query)+" --limit "+itoa(nextLimit(limit)),
			"All: trawl twitter search "+quoteSearchQuery(query)+" --all")
	}
	return hints
}

func browseHints(kind string, limit int, truncated bool) []string {
	hints := []string{"Open: trawl twitter open REF"}
	if truncated {
		hints = append(hints,
			"More: trawl twitter "+kind+" --limit "+itoa(nextLimit(limit)),
			"All: trawl twitter "+kind+" --all")
	}
	return hints
}

func quoteSearchQuery(query string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), `"`, `\"`) + `"`
}

// statsNextLimit doubles the shown count for the stats "More" hint.
func statsNextLimit(shown int) int {
	if shown < 1 {
		return defaultStatsLimit
	}
	return shown * 2
}

func nextLimit(limit int) int {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	return limit * 2
}

func browseWho(item listResult, ownerAuthorID string) string {
	return humanWhoCell(item.rawWho, item.authorID, item.InReplyTo, item.inReplyToAuthorID, ownerAuthorID)
}

func humanName(value, authorID, ownerAuthorID string) string {
	if ownerAuthorID != "" && authorID == ownerAuthorID {
		return selfDisplayName(value)
	}
	return value
}

func selfDisplayName(value string) string {
	if handle := displayHandle(value); handle != "" {
		return "me (" + handle + ")"
	}
	return "me"
}

// jsonWho is the sender only; the reply target lives in where/in_reply_to.
// Composing the arrow here would duplicate that field (telecrawl keeps who
// bare, and trawl federates both).
func jsonWho(value, authorID, replyTo, replyToAuthorID, ownerAuthorID string) string {
	return humanName(value, authorID, ownerAuthorID)
}

func humanWhoCell(value, authorID, replyTo, replyToAuthorID, ownerAuthorID string) string {
	who := humanWhoPerson(value, authorID, ownerAuthorID, 24)
	if strings.TrimSpace(replyTo) == "" {
		return who
	}
	suffixPerson := humanWhoPerson(replyTo, replyToAuthorID, ownerAuthorID, 24)
	suffix := " → " + suffixPerson
	remaining := 24 - render.DisplayWidth(suffix)
	if remaining < 1 {
		remaining = 1
	}
	return humanWhoPerson(value, authorID, ownerAuthorID, remaining) + suffix
}

func humanWhoPerson(value, authorID, ownerAuthorID string, budget int) string {
	value = humanName(value, authorID, ownerAuthorID)
	if render.DisplayWidth(value) <= budget {
		return value
	}
	if handle := displayHandle(value); handle != "" {
		return handle
	}
	return value
}

func displayHandle(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") {
		return strings.Fields(value)[0]
	}
	start := strings.LastIndex(value, "(@")
	if start < 0 || !strings.HasSuffix(value, ")") {
		return ""
	}
	return strings.TrimSuffix(value[start+1:], ")")
}

func countsLine(tweet openTweet) string {
	return groupDigits64(tweet.LikeCount) + " likes · " + groupDigits64(tweet.RetweetCount) + " retweets · " + groupDigits64(tweet.ReplyCount) + " replies"
}

func statsFreshnessHint(rows []statsRow) string {
	var oldest, newest string
	for _, row := range rows {
		if row.countsAsOfTime.IsZero() {
			continue
		}
		value := render.ShortLocalTime(row.countsAsOfTime)
		if oldest == "" || value < oldest {
			oldest = value
		}
		if newest == "" || value > newest {
			newest = value
		}
	}
	switch oldest {
	case "":
		return "Engagement counts have not been fetched."
	case newest:
		return "Engagement counts fetched as of " + oldest + "."
	default:
		return "Engagement counts fetched between " + oldest + " and " + newest + "."
	}
}

func humanWindow(value string) string {
	if strings.HasSuffix(value, "d") {
		days := strings.TrimSuffix(value, "d")
		if days == "1" {
			return "1 day"
		}
		return days + " days"
	}
	return value
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func humanLabel(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", " "))
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
