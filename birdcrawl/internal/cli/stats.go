package cli

import (
	"flag"
	"io"

	ckflags "github.com/openclaw/crawlkit/flags"
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
	// The one --limit contract (crawlkit/flags): --limit N is honored exactly,
	// a limit below 1 is a usage error, no hidden cap. stats is a bounded
	// top-N ranking, so it offers no --all.
	limitN, err := ckflags.Limit(*limit, flagPassed(fs, "limit"), false)
	if err != nil {
		return usageErr(err)
	}
	parsedWindow, err := parseWindow(*window)
	if err != nil {
		return usageErr(err)
	}
	filter := store.StatsFilter{Window: parsedWindow, By: *by, Limit: limitN}
	return r.withStore(func(st *store.Store) error {
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

// flagPassed reports whether a flag was set explicitly on the command line
// rather than left at its default, so flags.Limit can tell a real --limit from
// the fallback.
func flagPassed(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
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
