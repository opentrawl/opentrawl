package twitter

import (
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

func (r *runtime) runStats() (*command.TrawlerCommandResponse, error) {
	// The one --limit contract (trawlkit/flags): --limit N is honored exactly,
	// a limit below 1 is a usage error, no hidden cap. stats is a bounded
	// top-N ranking.
	limitN, err := ckflags.Limit(r.c.statsLimit, r.c.statsLimitSet)
	if err != nil {
		return nil, usageErr(err)
	}
	parsedWindow, err := parseWindow(r.c.statsWindow)
	if err != nil {
		return nil, usageErr(render.OptionValueIsNotValid("window"))
	}
	statsMetric, err := store.ParseTweetStatsRankingMetric(r.c.statsBy)
	if err != nil {
		return nil, usageErr(render.OptionValueMustBeOneOf("by", []string{"likes", "retweets", "replies"}))
	}
	filter := store.StatsFilter{Window: parsedWindow, Metric: statsMetric, Limit: limitN}
	var response *command.TrawlerCommandResponse
	err = r.withReadOnlyStore(func(st *store.Store) error {
		result, err := st.Stats(r.ctx, filter)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		response = twitterStatsCommandResponse(newStatsEnvelope(result, ownerAuthorID))
		return nil
	})
	return response, err
}

func newStatsEnvelope(result store.StatsResult, ownerAuthorID string) statsEnvelope {
	rows := make([]statsRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		ref := row.Ref
		rows = append(rows, statsRow{
			Ref:       ref,
			Text:      row.Text,
			Count:     row.Count,
			timeValue: row.Time,
		})
	}
	return statsEnvelope{
		By:         result.By,
		Population: result.Population,
		Results:    rows,
	}
}
