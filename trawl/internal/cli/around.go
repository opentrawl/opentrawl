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

func (c *AroundCmd) execute(r *Runtime) (unifiedindex.AroundResult, error) {
	window, err := parseAroundWindowDuration(c.Window)
	if err != nil {
		return unifiedindex.AroundResult{}, render.CommandFailureFromTrawler(render.CommandFailureKindCommandIsWrong, err.Error())
	}
	limit, err := normalizeSearchLimit(c.Limit)
	if err != nil {
		return unifiedindex.AroundResult{}, err
	}
	if strings.TrimSpace(c.Target) == "" && strings.TrimSpace(c.Trip) == "" {
		return unifiedindex.AroundResult{}, render.CommandFailureFromTrawler(
			render.CommandFailureKindCommandIsWrong,
			"Provide a trawl link, a date (YYYY-MM-DD), or --trip.",
		)
	}

	installed := discoverInstalledTrawlers(r.ctx)
	if err := r.ensureUnifiedIndexBootstrappedForTrawlers(r.ctx, installed); err != nil {
		return unifiedindex.AroundResult{}, err
	}
	stateRoot, err := trawlkit.ResolveStateRoot(r.stateRoot)
	if err != nil {
		return unifiedindex.AroundResult{}, err
	}
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(r.ctx, stateRoot, false)
	if err != nil {
		return unifiedindex.AroundResult{}, err
	}
	if indexStore == nil {
		return unifiedindex.AroundResult{}, render.CommandFailureFromTrawler(
			render.CommandFailureKindArchiveOrOperationFailed,
			"Unified index has not been built yet; run `trawl index rebuild`.",
		)
	}
	defer func() { _ = indexStore.Close() }()

	var trawlerFilter []string
	_, isAgentRequest := agentServingContextFrom(r.ctx)
	if isAgentRequest {
		if limit > mcpMaximumRemoteQueryLimit {
			limit = mcpMaximumRemoteQueryLimit
		}
		if err := refuseAroundTargetOutsideAgentScope(installed, c.Target); err != nil {
			return unifiedindex.AroundResult{}, err
		}
		if strings.TrimSpace(c.Trawler) == "" {
			for _, t := range installed {
				trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
			}
		}
	}
	if strings.TrimSpace(c.Trawler) != "" {
		selected, selErr := r.selectInstalledTrawlers(installed, splitTrawlerCSV(c.Trawler))
		if selErr != nil {
			return unifiedindex.AroundResult{}, selErr
		}
		for _, t := range selected {
			trawlerFilter = append(trawlerFilter, installedTrawlerIdentityText(t))
		}
	}

	res, err := unifiedindex.FindAround(r.ctx, indexStore.DB(), unifiedindex.AroundQuery{
		Target:               c.Target,
		TripQuery:            c.Trip,
		Window:               window,
		Limit:                limit,
		TrawlerFilter:        trawlerFilter,
		EnforceTrawlerFilter: isAgentRequest || len(trawlerFilter) > 0,
	})
	if err != nil {
		return unifiedindex.AroundResult{}, render.CommandFailureFromTrawler(render.CommandFailureKindNothingOrSeveralThingsMatched, err.Error())
	}
	return res, nil
}

func (c *AroundCmd) Run(r *Runtime) error {
	res, err := c.execute(r)
	if err != nil {
		return err
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

// refuseAroundTargetOutsideAgentScope stops an agent from anchoring around on
// a record from a source its token does not allow.
func refuseAroundTargetOutsideAgentScope(allowedTrawlers []InstalledTrawler, target string) error {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return nil
	}
	if route, err := trawlkit.ParseGloballyRoutableTrawlLink(trawlkit.NewGloballyRoutableTrawlLink(trimmed)); err == nil {
		trawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler)
		if _, allowed := findInstalledTrawler(allowedTrawlers, trawlerIdentity); !allowed {
			return render.AgentAccessSourceNotAllowed(trawlerIdentity, installedTrawlerCommandNames(allowedTrawlers))
		}
		return nil
	}
	if prefix, _, hasColon := strings.Cut(trimmed, ":"); hasColon {
		normalizedPrefix := strings.ToLower(strings.TrimSpace(prefix))
		if isTrawlerIdentifierPrefix(normalizedPrefix) {
			if _, allowed := findInstalledTrawler(allowedTrawlers, normalizedPrefix); !allowed {
				return render.AgentAccessSourceNotAllowed(normalizedPrefix, installedTrawlerCommandNames(allowedTrawlers))
			}
		}
	}
	return nil
}

func isTrawlerIdentifierPrefix(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}
