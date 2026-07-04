package cli

import (
	"flag"
	"io"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func (r *runtime) runStats(args []string) error {
	fs := flag.NewFlagSet("birdcrawl stats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	window := fs.String("window", "30d", "")
	by := fs.String("by", "likes", "")
	limit := fs.Int("limit", defaultStatsLimit, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if *limit <= 0 {
		*limit = defaultStatsLimit
	}
	if *limit > maxStatsLimit {
		*limit = maxStatsLimit
	}
	parsedWindow, err := parseWindow(*window)
	if err != nil {
		return usageErr(err)
	}
	filter := store.StatsFilter{Window: parsedWindow, By: *by, Limit: *limit}
	return r.withReadOnlyStore(func(st *store.Store) error {
		result, err := st.Stats(r.ctx, filter)
		if err != nil {
			return err
		}
		return r.print(newStatsEnvelope(result))
	})
}

func newStatsEnvelope(result store.StatsResult) statsEnvelope {
	rows := make([]statsRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		rows = append(rows, statsRow{
			Ref:        row.Ref,
			Time:       formatOptionalTime(row.Time),
			Who:        row.Who,
			Text:       row.Text,
			Count:      row.Count,
			CountsAsOf: formatOptionalTime(row.CountsAsOf),
		})
	}
	return statsEnvelope{
		By:                   result.By,
		Window:               formatDuration(result.Window),
		FreshnessSpread:      freshnessSpread(result.FreshnessOldest, result.FreshnessNewest),
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

func (r *runtime) printStats(value statsEnvelope) error {
	if _, err := io.WriteString(r.stdout, "stats by "+value.By+" over "+value.Window+"\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(r.stdout, "freshness spread: "+value.FreshnessSpread+"\n"); err != nil {
		return err
	}
	for _, row := range value.Results {
		if _, err := io.WriteString(r.stdout, row.Time+" "+row.Who+" "+groupDigits64(row.Count)+" "+value.By+" as of "+emptyDash(row.CountsAsOf)+"\n"+row.Text+"\nref: "+row.Ref+"\n\n"); err != nil {
			return err
		}
	}
	return nil
}
