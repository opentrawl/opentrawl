package birdcrawl

import (
	"errors"
	"io"

	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runStats(args []string) error {
	if len(args) > 0 {
		return usageErr(errors.New("stats takes no positional arguments"))
	}
	// The one --limit contract (crawlkit/flags): --limit N is honored exactly,
	// a limit below 1 is a usage error, no hidden cap. stats is a bounded
	// top-N ranking.
	limitN, err := ckflags.Limit(r.c.statsLimit, r.c.statsLimitSet)
	if err != nil {
		return usageErr(err)
	}
	parsedWindow, err := parseWindow(r.c.statsWindow)
	if err != nil {
		return usageErr(err)
	}
	filter := store.StatsFilter{Window: parsedWindow, By: r.c.statsBy, Limit: limitN}
	return r.withReadOnlyStore(func(st *store.Store) error {
		result, err := st.Stats(r.ctx, filter)
		if err != nil {
			return err
		}
		aliases, err := aliasesForStats(r.ctx, st, result.Rows)
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		return r.print(newStatsEnvelope(result, aliases, ownerAuthorID))
	})
}

func newStatsEnvelope(result store.StatsResult, aliases map[string]string, ownerAuthorID string) statsEnvelope {
	rows := make([]statsRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		ref := row.Ref
		rows = append(rows, statsRow{
			Ref:            ref,
			ShortRef:       aliases[ref],
			Time:           formatOptionalTime(row.Time),
			Who:            jsonWho(row.Who, row.AuthorID, "", "", ownerAuthorID),
			Text:           row.Text,
			Count:          row.Count,
			CountsAsOf:     formatOptionalTime(row.CountsAsOf),
			timeValue:      row.Time,
			countsAsOfTime: row.CountsAsOf,
		})
	}
	return statsEnvelope{
		By:                   result.By,
		Window:               formatDuration(result.Window),
		CountsFetchedFrom:    formatOptionalTime(result.FreshnessOldest),
		CountsFetchedTo:      formatOptionalTime(result.FreshnessNewest),
		Population:           result.Population,
		PopulationWithCounts: result.PopulationWithCounts,
		CountsMissing:        result.CountsMissing,
		Results:              rows,
	}
}

func (r *runtime) printImport(value importEnvelope) error {
	_, err := io.WriteString(r.stdout, "archive imported\n")
	if err != nil {
		return err
	}
	_, err = io.WriteString(r.stdout, "tweets: "+groupDigits(value.Tweets)+"\n")
	if err != nil {
		return err
	}
	_, err = io.WriteString(r.stdout, "authored: "+groupDigits(value.Authored)+"\nlikes seen: "+groupDigits(value.LikesSeen)+"\nprofiles: "+groupDigits(value.Profiles)+"\n")
	if err != nil {
		return err
	}
	if value.NoteTweetsMerged > 0 || value.NoteTweetsUnmatched > 0 {
		line := "long-form notes merged: " + itoa(value.NoteTweetsMerged)
		if value.NoteTweetsUnmatched > 0 {
			line += " (" + itoa(value.NoteTweetsUnmatched) + " could not be matched to a tweet)"
		}
		if _, err := io.WriteString(r.stdout, line+"\n"); err != nil {
			return err
		}
	}
	if value.LikesWithoutText > 0 {
		if _, err := io.WriteString(r.stdout, "likes with no text in the dump: "+itoa(value.LikesWithoutText)+"\n"); err != nil {
			return err
		}
	}
	return nil
}
