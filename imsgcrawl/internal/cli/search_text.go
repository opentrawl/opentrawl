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
	if err := render.WriteSearchSummary(w, value.Query, returned, value.TotalMatches); err != nil {
		return err
	}
	if value.WhoResolved != nil {
		if _, err := fmt.Fprintf(w, "%s → %s\n", value.Who, value.WhoResolved.Who); err != nil {
			return err
		}
	}
	if value.Truncated {
		if _, err := fmt.Fprintf(w, "More: imsgcrawl search --limit %d", nextLimit(value.Limit, value.TotalMatches)); err != nil {
			return err
		}
		if value.After != "" {
			if _, err := fmt.Fprintf(w, " --after %s", strconv.Quote(value.After)); err != nil {
				return err
			}
		}
		if value.Before != "" {
			if _, err := fmt.Fprintf(w, " --before %s", strconv.Quote(value.Before)); err != nil {
				return err
			}
		}
		if value.Who != "" {
			if _, err := fmt.Fprintf(w, " --who %s", strconv.Quote(value.Who)); err != nil {
				return err
			}
		}
		if strings.TrimSpace(value.Query) != "" {
			if _, err := fmt.Fprintf(w, " %s", strconv.Quote(value.Query)); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "Open: imsgcrawl open REF\nUse --json when you need refs for follow-up commands.\n\n"); err != nil {
		return err
	}
	width := normalizeTextTableWidth(render.OutputWidth(w))
	columns := searchTextColumns(width)
	rows := tableRows(len(value.TextItems))
	for _, item := range value.TextItems {
		rows = append(rows, []string{
			formatArchiveTime(item.Time),
			senderName(item.FromMe, item.SenderLabel),
			searchDisplayRef(item),
			searchConversation(item),
			outputField(searchSnippet(item)),
		})
	}
	return renderTextTable(w, columns, rows)
}
