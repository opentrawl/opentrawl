package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

type AroundCmd struct {
	Target  string `arg:"" optional:"" name:"target" help:"Trawl link (for example money:abc123) or date (YYYY-MM-DD)"`
	Trip    string `name:"trip" placeholder:"NAME" help:"Indexed trip or place window name"`
	Window  string `name:"window" default:"3d" placeholder:"DURATION" help:"Time window around target (for example 12h, 3d, 2w)"`
	Trawler string `name:"trawler" placeholder:"TRAWLERS" help:"Limit joined records to these trawlers (comma-separated)"`
	Limit   int    `name:"limit" default:"30" placeholder:"COUNT" help:"Maximum number of related records"`
}

func (c *AroundCmd) Run(r *Runtime) error {
	window, err := parseAroundWindowDuration(c.Window)
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, err.Error())
	}
	limit, err := normalizeSearchLimit(c.Limit)
	if err != nil {
		return err
	}
	if strings.TrimSpace(c.Target) == "" && strings.TrimSpace(c.Trip) == "" {
		return render.CommandFailureFromTrawler(
			render.CommandFailureKindCommandIsWrong,
			"Provide a trawl link, a date (YYYY-MM-DD), or --trip.",
		)
	}

	installed := discoverInstalledTrawlers(r.ctx)
	if err := r.ensureUnifiedIndexBootstrappedForTrawlers(r.ctx, installed); err != nil {
		return err
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return err
	}
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(r.ctx, stateRoot, false)
	if err != nil {
		return err
	}
	if indexStore == nil {
		return render.CommandFailureFromTrawler(
			render.CommandFailureKindArchiveOrOperationFailed,
			"Unified index has not been built yet; run `trawl index rebuild`.",
		)
	}
	defer func() { _ = indexStore.Close() }()

	var trawlerFilter []string
	if strings.TrimSpace(c.Trawler) != "" {
		selected, selErr := r.selectInstalledTrawlers(installed, splitTrawlerCSV(c.Trawler))
		if selErr != nil {
			return selErr
		}
		for _, t := range selected {
			trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
		}
	}

	res, err := unifiedindex.FindAround(r.ctx, indexStore.DB(), unifiedindex.AroundQuery{
		Target:        c.Target,
		TripQuery:     c.Trip,
		Window:        window,
		Limit:         limit,
		TrawlerFilter: trawlerFilter,
	})
	if err != nil {
		return render.CommandFailureFromTrawler(render.CommandFailureKindNothingOrSeveralThingsMatched, err.Error())
	}

	windowRange := ""
	if !res.WindowStart.IsZero() && !res.WindowEnd.IsZero() {
		windowRange = res.WindowStart.Local().Format("2006-01-02") + " to " + res.WindowEnd.Local().Format("2006-01-02")
	}
	rows := make([]render.AroundMatchRowPresentation, 0, len(res.Matches))
	for _, m := range res.Matches {
		rows = append(rows, render.AroundMatchRowPresentation{
			When:    formatAroundMatchTime(m),
			How:     m.JoinReason,
			Trawler: m.TrawlerDisplay,
			Summary: formatAroundMatchSummary(m),
			Link:    m.Link,
		})
	}
	return r.output.WriteAroundResults(render.AroundJoinPresentation{
		AnchorSummary: res.AnchorSummary,
		AnchorLink:    res.AnchorLink,
		WindowRange:   windowRange,
		Matches:       rows,
		TotalFound:    res.TotalFound,
	})
}

func parseAroundWindowDuration(raw string) (time.Duration, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 3 * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --window %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --window %q", raw)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --window %q (use e.g. 12h, 3d, 2w)", raw)
	}
	return d, nil
}

func formatAroundMatchTime(m unifiedindex.AroundMatch) string {
	if m.OccurredUnix == 0 {
		return ""
	}
	t := time.Unix(m.OccurredUnix, 0).Local()
	if !m.HasTimeOfDay {
		return t.Format("2006-01-02")
	}
	return t.Format("2006-01-02 15:04 -07:00")
}

func formatAroundMatchSummary(m unifiedindex.AroundMatch) string {
	var parts []string
	if m.MoneyDecimal != "" && m.MoneyCurrency != "" {
		amt := m.MoneyDecimal + " " + m.MoneyCurrency
		if m.MoneyPending {
			amt += " (pending)"
		}
		parts = append(parts, amt)
	}
	if title := strings.TrimSpace(m.Title); title != "" {
		parts = append(parts, title)
	} else if body := strings.TrimSpace(m.BodyPreview); body != "" {
		parts = append(parts, body)
	}
	if ppl := strings.TrimSpace(m.PeopleText); ppl != "" && len(parts) < 2 {
		parts = append(parts, ppl)
	}
	return strings.Join(parts, " · ")
}
