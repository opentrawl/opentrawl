package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

func printSearchText(w io.Writer, value searchListOutput) error {
	returned := len(value.Results)
	var hints []string
	if value.WhoResolved != nil {
		hints = append(hints, fmt.Sprintf("%s → %s", value.Who, value.WhoResolved.Who))
	}
	if value.Truncated {
		hints = append(hints, searchMoreHint(value))
	}
	hints = append(hints,
		"Open: imsgcrawl open REF",
		"Use --json when you need refs for follow-up commands.",
	)
	items := make([]render.ListItem, 0, len(value.TextItems))
	for _, item := range value.TextItems {
		items = append(items, render.ListItem{
			Time:  parseArchiveTime(item.Time),
			Who:   senderName(item.FromMe, item.SenderLabel),
			Where: searchConversation(item),
			Ref:   searchDisplayRef(item),
			Text:  searchSnippet(item),
		})
	}
	return render.WriteList(w, render.List{
		Heading:   fmt.Sprintf("Search %q: showing %d of %d.", value.Query, returned, value.TotalMatches),
		Hints:     hints,
		Items:     items,
		ClampText: 2,
		Empty:     fmt.Sprintf("No matches for %q.", value.Query),
	})
}

func searchMoreHint(value searchListOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "More: imsgcrawl search --limit %d", nextLimit(value.Limit, value.TotalMatches))
	if value.After != "" {
		fmt.Fprintf(&b, " --after %s", strconv.Quote(value.After))
	}
	if value.Before != "" {
		fmt.Fprintf(&b, " --before %s", strconv.Quote(value.Before))
	}
	if value.Who != "" {
		fmt.Fprintf(&b, " --who %s", strconv.Quote(value.Who))
	}
	if strings.TrimSpace(value.Query) != "" {
		fmt.Fprintf(&b, " %s", strconv.Quote(value.Query))
	}
	return b.String()
}
