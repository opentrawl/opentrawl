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
	// limit is the requested page size; it drives the More hint and is
	// never serialized.
	limit int
}

type searchResult struct {
	Ref     string `json:"ref"`
	Alias   string `json:"short_ref"`
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
	resolved, err := filter.resolve(whoProvided, flagWasProvided(fs, "limit"))
	if err != nil {
		return usageErr(err)
	}
	resolved.Query = query
	return a.withExistingStore(ctx, func(st *store.Store) error {
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
		aliases, err := searchAliases(ctx, st, msgs)
		if err != nil {
			return err
		}
		return a.print(newSearchEnvelope(query, whoQuery, total, msgs, whoResolved, aliases, resolved.Limit))
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
		return store.WhoResolution{}, unknownWhoError(value, nil)
	case 1:
		if resolution.OnlyCloseSpellingMatch() {
			return store.WhoResolution{}, unknownWhoError(value, resolution.Candidates)
		}
		return resolution, nil
	default:
		return store.WhoResolution{}, ambiguousWhoError(value, query, resolution.Candidates)
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

func (f searchFlags) resolve(whoProvided, limitSet bool) (store.MessageFilter, error) {
	out, err := f.messageFlags.resolve(limitSet)
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

func searchAliases(ctx context.Context, st *store.Store, messages []store.Message) (map[string]string, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	// The short-ref index is a display convenience. On a read-only handle it
	// cannot be (re)built — e.g. a pre-migration archive that has not been
	// re-synced yet — so degrade to full refs (still openable) rather than
	// fail the read. The next sync rebuilds the index. (Errors should never
	// pass silently, unless explicitly silenced — this is that silencing.)
	if err := st.EnsureShortRefs(ctx); err != nil {
		return nil, nil
	}
	return st.ShortRefAliases(ctx, messageRefs(messages))
}

func (a *app) printSearch(result searchEnvelope) error {
	if result.WhoResolved != nil {
		if _, err := fmt.Fprintf(a.stdout, "%s → %s\n\n", result.WhoQuery, result.WhoResolved.Who); err != nil {
			return err
		}
	}
	hints := []string{"Open: wacrawl open REF"}
	if result.Truncated {
		hints = append(hints, searchMoreHint(result))
	}
	return render.WriteList(a.stdout, render.List{
		Heading:   searchHeading(result),
		Hints:     hints,
		Items:     searchListItems(result.Results),
		ClampText: 2,
		Empty:     searchEmptyText(result.Query),
	})
}

func searchHeading(result searchEnvelope) string {
	if strings.TrimSpace(result.Query) == "" {
		return fmt.Sprintf("Search filters: showing %d of %d matches.", len(result.Results), result.TotalMatches)
	}
	return fmt.Sprintf("Search %q: showing %d of %d matches.", result.Query, len(result.Results), result.TotalMatches)
}

func searchEmptyText(query string) string {
	if strings.TrimSpace(query) == "" {
		return "No matches for these filters."
	}
	return fmt.Sprintf("No matches for %q.", query)
}

func searchMoreHint(result searchEnvelope) string {
	limit := result.limit
	if limit < 1 {
		limit = defaultMessageLimit
	}
	next := limit * 2
	if strings.TrimSpace(result.Query) == "" {
		return fmt.Sprintf("More: wacrawl search --limit %d", next)
	}
	return fmt.Sprintf("More: wacrawl search %q --limit %d", result.Query, next)
}

func searchListItems(results []searchResult) []render.ListItem {
	items := make([]render.ListItem, 0, len(results))
	for _, item := range results {
		items = append(items, render.ListItem{
			Time:  parseFormattedTime(item.Time),
			Who:   item.Who,
			Where: item.Where,
			Ref:   displayRef(item.Ref, item.Alias),
			Text:  item.Snippet,
		})
	}
	return items
}

func newSearchEnvelope(query, whoQuery string, total int, messages []store.Message, resolved *whoResolved, aliases map[string]string, limit int) searchEnvelope {
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
		limit:        limit,
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
