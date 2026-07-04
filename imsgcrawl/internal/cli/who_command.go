package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/imsgcrawl/internal/archive"
)

func (r *runtime) runWho(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"who"})
	}
	fs := flag.NewFlagSet("imsgcrawl who", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	query := strings.Join(strings.Fields(strings.Join(fs.Args(), " ")), " ")
	if query == "" {
		return usageErr(errors.New("who requires a name"))
	}
	return r.withArchive(func(st *archive.Store) error {
		resolution, err := st.ResolveWho(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(resolution)
	})
}

func parseSearchTimeBound(value, flagName string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("search %s requires a time", flagName)
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return archive.AppleDateFromTime(parsed), nil
		}
	}
	return 0, fmt.Errorf("search %s must be RFC3339 or YYYY-MM-DD", flagName)
}

func (r *runtime) ambiguousWhoError(query, searchQuery string, resolution archive.WhoResolution) error {
	message := fmt.Sprintf("ambiguous_who: %q matches more than one person", query)
	remedy := "retry with one identifier from the table"
	_ = r.logError("ambiguous_who", worldMustChange(nil, message, remedy))
	if !r.json {
		_ = printAmbiguousWhoText(r.stderr, query, searchQuery, resolution)
	}
	return commandErr("ambiguous_who", message, remedy, 4, map[string]any{
		"candidates":      resolution.Candidates,
		"candidate_total": resolution.TotalMatches,
	}, errors.New(message))
}

func (r *runtime) unknownWhoError(query, searchQuery string, resolution archive.WhoResolution) error {
	message := fmt.Sprintf("unknown_who: %q did not match a person", query)
	remedy := "run imsgcrawl who with a broader name, or search without --who"
	hint := "Search without --who to inspect matching messages."
	_ = r.logError("unknown_who", worldMustChange(nil, message, remedy))
	didYouMean := resolution.Candidates
	if !r.json {
		_ = printUnknownWhoText(r.stderr, query, searchQuery, resolution, hint)
	}
	return commandErr("unknown_who", message, remedy, 5, map[string]any{
		"did_you_mean":       didYouMean,
		"did_you_mean_total": resolution.TotalMatches,
		"hint":               hint,
	}, errors.New(message))
}

func printAmbiguousWhoText(w io.Writer, query, searchQuery string, resolution archive.WhoResolution) error {
	if _, err := fmt.Fprintf(w, "ambiguous_who: %q matches more than one person.\n", query); err != nil {
		return err
	}
	if err := printWhoMatchCount(w, resolution); err != nil {
		return err
	}
	if err := renderWhoCandidatesTable(w, resolution.Candidates); err != nil {
		return err
	}
	return printWhoRetry(w, searchQuery, resolution.Candidates)
}

func printUnknownWhoText(w io.Writer, query, searchQuery string, resolution archive.WhoResolution, hint string) error {
	if _, err := fmt.Fprintf(w, "unknown_who: %q did not match a person.\n", query); err != nil {
		return err
	}
	if len(resolution.Candidates) > 0 {
		if _, err := io.WriteString(w, "Did you mean:\n"); err != nil {
			return err
		}
		if err := printWhoMatchCount(w, resolution); err != nil {
			return err
		}
		if err := renderWhoCandidatesTable(w, resolution.Candidates); err != nil {
			return err
		}
		return printWhoRetry(w, searchQuery, resolution.Candidates)
	}
	_, err := fmt.Fprintf(w, "Hint: %s\n", hint)
	return err
}

func printWhoMatchCount(w io.Writer, resolution archive.WhoResolution) error {
	if !resolution.Truncated {
		return nil
	}
	_, err := fmt.Fprintf(w, "Showing %d of %d matches.\n", len(resolution.Candidates), resolution.TotalMatches)
	return err
}

func printWhoRetry(w io.Writer, searchQuery string, candidates []archive.WhoCandidate) error {
	if len(candidates) == 0 || len(candidates[0].Identifiers) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "Retry: imsgcrawl search --who %s", strconv.Quote(candidates[0].Identifiers[0])); err != nil {
		return err
	}
	if strings.TrimSpace(searchQuery) != "" {
		if _, err := fmt.Fprintf(w, " %s", strconv.Quote(searchQuery)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}
