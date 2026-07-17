package trawlkit

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// ChatsRunOptions configures the shared lifecycle for listing one source's chats.
type ChatsRunOptions struct {
	StateRoot string
	Timeout   time.Duration
	Verbosity int
	Stderr    io.Writer
}

// ChatsResult is the complete per-source result. Federation may combine and
// further cap these rows, but does not repeat source acquisition, filtering,
// paging or short-ref policy.
type ChatsResult struct {
	Chats     []Chat
	ShortRefs map[string]string
	Truncated bool
}

type typedChats struct {
	query  ChatQuery
	result ChatsResult
}

// RunChats lists one source through the same configuration, logging, timeout,
// archive preparation, store and request lifecycle as the namespaced runner.
func RunChats(ctx context.Context, source Crawler, query ChatQuery, opts ChatsRunOptions) (ChatsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	r := runner{opts: defaultRunOptions()}
	r.opts.stderr = opts.Stderr
	r.opts.readTimeout = opts.Timeout
	r.opts = r.opts.withDefaults()
	verb, err := resolveVerb(source, []string{"chats"})
	if err != nil {
		return ChatsResult{}, err
	}
	chats := &typedChats{query: query}
	verb.chats = chats
	result := r.runInProcess(ctx, source, verb, globalOptions{stateRoot: opts.StateRoot, verbosity: opts.Verbosity}, output.JSON, false)
	if result.err != nil {
		return ChatsResult{}, result.err
	}
	return chats.result, nil
}

func executeChats(ctx context.Context, lister ChatLister, req *Request, query ChatQuery) (ChatsResult, error) {
	query.With = strings.TrimSpace(query.With)
	fetch := query
	fetch.With = ""
	fetch.WithAliases = nil
	switch {
	case query.With != "" || len(query.WithAliases) > 0:
		// Participant filtering needs the whole source result; filtering one page
		// would silently miss matching conversations beyond that page.
		fetch.All = true
		fetch.Limit = 0
	case !query.All && query.Limit > 0:
		// One extra row proves truncation instead of guessing from a full page.
		fetch.Limit = query.Limit + 1
	}

	chats, err := lister.Chats(ctx, req, fetch)
	if err != nil {
		return ChatsResult{}, err
	}
	if query.Unread {
		chats = filterUnreadChats(chats)
	}
	if query.With != "" || len(query.WithAliases) > 0 {
		chats = filterChatsWith(chats, query.With, query.WithAliases)
	}
	truncated := !query.All && query.Limit > 0 && len(chats) > query.Limit
	if truncated {
		chats = chats[:query.Limit]
	}
	aliases, err := chatShortRefs(ctx, req, chats)
	if err != nil {
		return ChatsResult{}, err
	}
	return ChatsResult{Chats: chats, ShortRefs: aliases, Truncated: truncated}, nil
}

func filterUnreadChats(chats []Chat) []Chat {
	kept := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if chat.Unread != nil && *chat.Unread > 0 {
			kept = append(kept, chat)
		}
	}
	return kept
}
