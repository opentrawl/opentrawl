package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
)

var searchValueFlags = map[string]bool{
	"limit": true, "after": true, "before": true, "who": true,
}

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	fs := flag.NewFlagSet("gogcrawl search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", defaultSearchLimit, "")
	after := fs.String("after", "", "")
	before := fs.String("before", "", "")
	who := fs.String("who", "", "")
	flagArgs, positionals := splitFlagArgs(args, searchValueFlags)
	if err := fs.Parse(flagArgs); err != nil {
		return usageErr(err)
	}
	query := strings.TrimSpace(strings.Join(positionals, " "))
	if query == "" {
		return usageErr(errors.New("search query is required"))
	}
	if *limit < 1 || *limit > maxSearchLimit {
		return usageErr(fmt.Errorf("search --limit must be between 1 and %d", maxSearchLimit))
	}
	whoProvided := flagPassed(fs, "who")
	whoValue := normalizeWhoValue(*who)
	if whoProvided && whoValue == "" {
		return usageErr(errors.New("search --who requires an identity"))
	}
	opts := archive.SearchOptions{Query: query, Limit: *limit, Who: whoValue}
	if strings.TrimSpace(*after) != "" {
		t, err := parseTime(*after)
		if err != nil {
			return usageErr(err)
		}
		opts.After = &t
	}
	if strings.TrimSpace(*before) != "" {
		t, err := parseTime(*before)
		if err != nil {
			return usageErr(err)
		}
		opts.Before = &t
	}
	return r.withArchive(func(st *archive.Store) error {
		result, err := st.Search(r.ctx, opts)
		if err != nil {
			return err
		}
		return r.print(result)
	})
}

func splitFlagArgs(args []string, valueFlags map[string]bool) (flags, positionals []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if before, _, ok := strings.Cut(name, "="); ok {
				name = before
			}
			if !strings.Contains(arg, "=") && valueFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return flags, positionals
}

func parseTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("time must be RFC3339 or YYYY-MM-DD: %s", value)
}

func (r *runtime) withArchive(fn func(*archive.Store) error) error {
	if !archive.Exists(r.archivePath) {
		return commandErr("archive_missing", "archive database is not ready", "run gogcrawl sync", nil)
	}
	st, err := archive.Open(r.ctx, r.archivePath)
	if err != nil {
		return commandErr("archive_missing", "archive database is not ready", "run gogcrawl sync", err)
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func flagPassed(fs *flag.FlagSet, name string) bool {
	passed := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			passed = true
		}
	})
	return passed
}

func normalizeWhoValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
