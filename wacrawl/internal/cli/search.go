package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

type searchEnvelope struct {
	Query        string         `json:"query"`
	WhoQuery     string         `json:"-"`
	WhoResolved  *whoResolved   `json:"who_resolved,omitempty"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type searchResult struct {
	Ref     string `json:"ref"`
	Alias   string `json:"-"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

func (a *app) runSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	filter := bindSearchFlags(fs)
	if commandWantsHelp(args) {
		printCommandUsage(a.stdout, "search")
		return nil
	}
	flagArgs, query, err := splitSearchArgs(args)
	if err != nil {
		return usageErr(err)
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "search")
			return nil
		}
		return usageErr(err)
	}
	whoProvided := flagWasProvided(fs, "who")
	afterProvided := flagWasProvided(fs, "after")
	beforeProvided := flagWasProvided(fs, "before")
	if strings.TrimSpace(query) == "" && !whoProvided && !afterProvided && !beforeProvided {
		return usageErr(errors.New("search requires a query or --who, --after, or --before"))
	}
	resolved, err := filter.resolve(whoProvided)
	if err != nil {
		return usageErr(err)
	}
	resolved.Query = query
	withStore := a.withReadStore
	if !a.json {
		withStore = a.withExistingStore
	}
	return withStore(ctx, func(st *store.Store) error {
		var whoResolved *whoResolved
		whoQuery := ""
		if resolved.Who != "" {
			resolution, err := a.resolveSearchWho(ctx, st, resolved.Who, query)
			if err != nil {
				return err
			}
			resolved.WhoKeys = resolution.ParticipantKeys
			whoQuery = resolved.Who
			whoResolved = newWhoResolved(resolution.Candidates[0])
		}
		total, err := st.SearchCount(ctx, resolved)
		if err != nil {
			return err
		}
		msgs, err := st.Search(ctx, resolved)
		if err != nil {
			return err
		}
		aliases := map[string]string{}
		if !a.json {
			if err := st.EnsureShortRefs(ctx); err != nil {
				return err
			}
			aliases, err = st.ShortRefAliases(ctx, messageRefs(msgs))
			if err != nil {
				return err
			}
		}
		return a.print(newSearchEnvelope(query, whoQuery, total, msgs, whoResolved, aliases))
	})
}

func (a *app) resolveSearchWho(ctx context.Context, st *store.Store, value, query string) (store.WhoResolution, error) {
	resolution, err := st.ResolveWhoIdentifier(ctx, value)
	if err != nil {
		return store.WhoResolution{}, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = st.ResolveWho(ctx, value)
		if err != nil {
			return store.WhoResolution{}, err
		}
	}
	switch len(resolution.Candidates) {
	case 0:
		return store.WhoResolution{}, a.failContractWithExit(unknownWhoError(value, nil), 5)
	case 1:
		if resolution.OnlyCloseSpellingMatch() {
			return store.WhoResolution{}, a.failContractWithExit(unknownWhoError(value, resolution.Candidates), 5)
		}
		return resolution, nil
	default:
		return store.WhoResolution{}, a.failContractWithExit(ambiguousWhoError(value, query, resolution.Candidates), 4)
	}
}

func splitSearchArgs(args []string) ([]string, string, error) {
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if searchFlagNeedsValue(arg) && !strings.Contains(arg, "=") {
				next := i + 1
				if next >= len(args) {
					return nil, "", fmt.Errorf("flag needs an argument: %s", arg)
				}
				flags = append(flags, args[next]) // #nosec G602 -- next is checked against len(args) above.
				i = next
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) > 1 {
		return nil, "", errors.New("search accepts at most one query")
	}
	if len(positionals) == 0 {
		return flags, "", nil
	}
	return flags, positionals[0], nil
}

func searchFlagNeedsValue(arg string) bool {
	name := strings.TrimPrefix(arg, "-")
	name = strings.TrimPrefix(name, "-")
	if before, _, ok := strings.Cut(name, "="); ok {
		name = before
	}
	switch name {
	case "chat", "sender", "who", "limit", "after", "before":
		return true
	default:
		return false
	}
}

type searchFlags struct {
	messageFlags
	who *string
}

func bindSearchFlags(fs *flag.FlagSet) searchFlags {
	return searchFlags{
		messageFlags: bindMessageFlags(fs),
		who:          fs.String("who", "", ""),
	}
}

func (f searchFlags) resolve(whoProvided bool) (store.MessageFilter, error) {
	out, err := f.messageFlags.resolve()
	if err != nil {
		return store.MessageFilter{}, err
	}
	if !whoProvided {
		return out, nil
	}
	out.Who = normalizeWhoValue(*f.who)
	if out.Who == "" {
		return store.MessageFilter{}, errors.New("--who requires an identity")
	}
	return out, nil
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	provided := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			provided = true
		}
	})
	return provided
}

func (a *app) printSearch(result searchEnvelope) error {
	if result.WhoResolved != nil {
		if _, err := fmt.Fprintf(a.stdout, "%s → %s\n\n", result.WhoQuery, result.WhoResolved.Who); err != nil {
			return err
		}
	}
	width := render.OutputWidth(a.stdout)
	for _, item := range result.Results {
		if err := a.printSearchResult(width, item); err != nil {
			return err
		}
	}
	if result.Truncated {
		_, err := fmt.Fprintf(a.stdout, "showing %d of %d matches; narrow with --limit, --after, --before, or --chat\n", len(result.Results), result.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "showing %d of %d matches\n", len(result.Results), result.TotalMatches)
	return err
}

func (a *app) printSearchResult(width int, item searchResult) error {
	meta := strings.TrimSpace(fmt.Sprintf("%s  %s in %s", item.Time, item.Who, item.Where))
	for _, line := range render.WrapWithIndent("", meta, width, "  ") {
		if _, err := fmt.Fprintln(a.stdout, line); err != nil {
			return err
		}
	}
	for _, line := range render.WrapWithIndent("  ", item.Snippet, width, "  ") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if _, err := fmt.Fprintln(a.stdout, line); err != nil {
			return err
		}
	}
	if item.Alias != "" {
		if _, err := fmt.Fprintf(a.stdout, "ref: %s\nfull ref: %s\n\n", item.Alias, item.Ref); err != nil {
			return err
		}
		return nil
	}
	_, err := fmt.Fprintf(a.stdout, "ref: %s\n\n", item.Ref)
	return err
}

func newSearchEnvelope(query, whoQuery string, total int, messages []store.Message, resolved *whoResolved, aliases map[string]string) searchEnvelope {
	if messages == nil {
		messages = []store.Message{}
	}
	results := make([]searchResult, 0, len(messages))
	for _, message := range messages {
		fullRef := messageRef(message)
		results = append(results, newSearchResult(message, aliases[fullRef]))
	}
	return searchEnvelope{
		Query:        query,
		WhoQuery:     whoQuery,
		WhoResolved:  resolved,
		Results:      results,
		TotalMatches: total,
		Truncated:    total > len(results),
	}
}

func newSearchResult(message store.Message, alias string) searchResult {
	return searchResult{
		Ref:     messageRef(message),
		Alias:   alias,
		Time:    formatTime(message.Timestamp),
		Who:     outputField(messageWho(message)),
		Where:   outputField(messageWhere(message)),
		Snippet: outputField(messageSnippet(message)),
	}
}
