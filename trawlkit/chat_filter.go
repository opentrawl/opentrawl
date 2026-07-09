package trawlkit

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// filterChatsWith keeps only the chats that include the --with person. The kit
// owns this filter, not the sources: every source hands the kit the same Chat
// fields (a group's resolved roster, a dm's partner name), so one rule matches
// them all and no crawler re-implements name matching. The caller fetches every
// chat before filtering, so the survivors are the full answer to "chats with X",
// not a filtered page.
func filterChatsWith(chats []Chat, with string) []Chat {
	with = strings.TrimSpace(with)
	if with == "" {
		return chats
	}
	kept := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if chatMatchesWith(chat, with) {
			kept = append(kept, chat)
		}
	}
	return kept
}

// chatMatchesWith reports whether a chat includes the named person. It matches
// case-insensitively on exact, prefix and substring name matches, reusing the
// shared resolver so folding and word handling match trawl's who and --who. It
// deliberately stops short of close spelling: that rank is for a resolver
// forgiving a typo on one intended person, but a filter that pulled in every
// close-spelled name would answer "chats with John" with Joan's chats, so a
// discovery filter keeps its matches literal.
func chatMatchesWith(chat Chat, with string) bool {
	return matchesName(with, chatWithTargets(chat))
}

// matchesName is the shared --with predicate: a real name match at substring or
// better, never close spelling. RankCloseSpelling is the lowest rank, so
// BetterThan excludes it while keeping exact, prefix and substring.
func matchesName(query string, names []string) bool {
	rank, ok := whomatch.MatchRank(query, names)
	return ok && rank.BetterThan(whomatch.RankCloseSpelling)
}

// chatWithTargets is the set of names --with matches against. It is only the
// names a source could resolve: a group's roster and, for a dm, the partner name
// the source stores as the title. A raw handle never reaches here — WhatsApp
// masks an unresolved @lid to a privacy label before the kit ever sees it, so
// --with can only match a real name, never a private handle, and never prints
// one. A chat the source could not name for a person simply does not match.
func chatWithTargets(chat Chat) []string {
	targets := make([]string, 0, len(chat.ParticipantNames)+1)
	targets = append(targets, chat.ParticipantNames...)
	// A dm carries no roster on most sources; its one other person is the title
	// (Telegram, WhatsApp) or is already in the roster above (iMessage). A group
	// title is a subject, not a person, so it is not a --with target.
	if !chat.Group {
		if title := strings.TrimSpace(chat.Title); title != "" {
			targets = append(targets, title)
		}
	}
	return targets
}
