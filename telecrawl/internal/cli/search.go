package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runSearch(args []string) error {
	filter, err := r.messageFilter("telecrawl search", args, true, defaultSearchLimit)
	if err != nil {
		return err
	}
	if filter.Limit <= 0 {
		filter.Limit = defaultSearchLimit
	}
	if filter.Limit > maxSearchLimit {
		filter.Limit = maxSearchLimit
	}
	return r.withStore(func(st *store.Store) error {
		resolved, err := r.resolveSearchWhoFilter(st, &filter)
		if err != nil {
			return err
		}
		messages, err := st.Search(r.ctx, filter)
		if err != nil {
			return err
		}
		total, err := st.CountSearch(r.ctx, filter)
		if err != nil {
			return err
		}
		shortRefs, err := st.ShortRefsFor(r.ctx, messageRefs(messages))
		if err != nil {
			return err
		}
		return r.print(newSearchEnvelope(filter.Query, messages, total, filter.Who, resolved, shortRefs))
	})
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message.SourcePK))
	}
	return refs
}

func (r *runtime) resolveSearchWhoFilter(st *store.Store, filter *store.MessageFilter) (*store.WhoCandidate, error) {
	if strings.TrimSpace(filter.Who) == "" {
		return nil, nil
	}
	candidates, err := st.ResolveWho(r.ctx, filter.Who)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	if len(candidates) > 1 {
		return nil, r.ambiguousWhoError(filter.Query, filter.Who, candidates)
	}
	candidate := candidates[0]
	if candidate.MatchedOnlyByCloseSpelling() {
		return nil, r.unknownWhoError(filter.Who, candidates)
	}
	filter.WhoParticipants = candidate.Participants
	filter.WhoResolved = true
	return &candidate, nil
}

func (r *runtime) printSearch(value searchEnvelope) error {
	if value.WhoResolved != nil {
		query := normalizeCLIWords(value.WhoQuery)
		if query == "" {
			query = value.WhoResolved.Who
		}
		if _, err := fmt.Fprintf(r.stdout, "%s \u2192 %s\n", query, value.WhoResolved.Who); err != nil {
			return err
		}
		if len(value.Results) > 0 {
			if _, err := io.WriteString(r.stdout, "\n"); err != nil {
				return err
			}
		}
	}
	for _, item := range value.Results {
		line := item.Time
		if item.Who != "" {
			line += " " + item.Who
		}
		if item.Where != "" {
			line += " in " + item.Where
		}
		ref := item.Ref
		if item.ShortRef != "" {
			ref = item.ShortRef + " (" + item.Ref + ")"
		}
		if _, err := fmt.Fprintf(r.stdout, "%s\n%s\nref: %s\n\n", line, item.Snippet, ref); err != nil {
			return err
		}
	}
	if value.Truncated {
		_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches; narrow with --limit, --after, --before, --chat, or --who\n", len(value.Results), value.TotalMatches)
		return err
	}
	_, err := fmt.Fprintf(r.stdout, "showing %d of %d matches\n", len(value.Results), value.TotalMatches)
	return err
}
