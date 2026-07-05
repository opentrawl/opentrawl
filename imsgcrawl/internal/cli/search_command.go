package cli

import (
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	fs := flag.NewFlagSet("imsgcrawl search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", defaultSearchLimit, "")
	after := fs.String("after", "", "")
	before := fs.String("before", "", "")
	who := fs.String("who", "", "")
	flagTokens, queryTokens := splitSearchFlagArgs(args)
	if err := fs.Parse(flagTokens); err != nil {
		return usageErr(err)
	}
	query := strings.TrimSpace(strings.Join(queryTokens, " "))
	afterPassed := flagPassed(fs, "after")
	beforePassed := flagPassed(fs, "before")
	whoPassed := flagPassed(fs, "who")
	if query == "" && !whoPassed && !afterPassed && !beforePassed {
		_ = printCommandUsage(r.stderr, []string{"search"})
		return usageErr(errors.New("search query or filter is required"))
	}
	rows, err := flags.Limit(*limit, flagPassed(fs, "limit"), false)
	if err != nil {
		return usageErr(err)
	}
	*limit = rows
	whoValue := strings.Join(strings.Fields(*who), " ")
	if whoPassed && whoValue == "" {
		return usageErr(errors.New("search --who requires an identity"))
	}
	options := archive.SearchOptions{Limit: *limit}
	if afterPassed {
		value, err := parseSearchTimeBound(*after, "--after")
		if err != nil {
			return usageErr(err)
		}
		options.After = value
		options.HasAfter = true
	}
	if beforePassed {
		value, err := parseSearchTimeBound(*before, "--before")
		if err != nil {
			return usageErr(err)
		}
		options.Before = value
		options.HasBefore = true
	}
	return r.withArchive(func(st *archive.Store) error {
		var resolved *archive.WhoCandidate
		if whoPassed {
			resolution, err := st.ResolveWho(r.ctx, whoValue)
			if err != nil {
				return err
			}
			if resolution.TotalMatches == 0 || resolution.MatchesOnlyByCloseSpelling() {
				return r.unknownWhoError(whoValue, query, resolution)
			}
			candidate, ok := resolution.FilterCandidate()
			if !ok {
				return r.ambiguousWhoError(whoValue, query, resolution)
			}
			resolved = &candidate
			options.Who = resolved
		}
		page, err := st.SearchPage(r.ctx, query, options)
		if err != nil {
			return err
		}
		return r.print(newSearchListOutput(query, page, *limit, searchOutputFilters{
			WhoQuery:    whoValue,
			WhoResolved: resolved,
			After:       *after,
			Before:      *before,
		}))
	})
}
