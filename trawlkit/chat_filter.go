package trawlkit

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

// filterChatsWith keeps only the chats that include the --with person. The kit
// owns this filter, not the sources: every source hands the kit the same Chat
// fields (a group's resolved member list, a dm's partner name), so one rule matches
// them all and no crawler re-implements name matching. The caller fetches every
// chat before filtering, so the survivors are the full answer to "chats with X",
// not a filtered page.
//
// A caller may also supply the aliases of one person already resolved by the
// People index. Those aliases are exact-match evidence only. This is the seam
// between identity reconciliation and source-native chat facts: People decides
// which names belong to one person, while this package remains the single owner
// of chat acquisition and filtering.
func filterChatsWith(chats []Chat, with string, aliases []string) []Chat {
	with = strings.TrimSpace(with)
	aliases = cleanChatAliases(aliases)
	if with == "" && len(aliases) == 0 {
		return chats
	}
	kept := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		if chatMatchesWith(chat, with, aliases) {
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
func chatMatchesWith(chat Chat, with string, aliases []string) bool {
	targets := chatWithTargets(chat)
	if len(aliases) > 0 {
		for _, alias := range aliases {
			if exactChatAliasMatch(alias, targets) {
				return true
			}
		}
		return false
	}
	return strings.TrimSpace(with) != "" && matchesName(with, targets)
}

func cleanChatAliases(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := whomatch.Compact(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func exactChatAliasMatch(alias string, targets []string) bool {
	alias = whomatch.Compact(alias)
	if alias == "" {
		return false
	}
	for _, target := range targets {
		if alias == whomatch.Compact(target) {
			return true
		}
	}
	return false
}

// matchesName is the shared --with predicate: a real name match at substring or
// better, never close spelling. RankCloseSpelling is the lowest rank, so
// BetterThan excludes it while keeping exact, prefix and substring.
func matchesName(query string, names []string) bool {
	rank, ok := whomatch.MatchRank(query, names)
	return ok && rank.BetterThan(whomatch.RankCloseSpelling)
}

// chatWithTargets is the set of names --with matches against. It is only the
// names a source could resolve: a group's member list and, for a dm, the partner name
// the source stores as the title. A raw handle never reaches here — WhatsApp
// masks an unresolved @lid to a privacy label before the kit ever sees it, so
// --with can only match a real name, never a private handle, and never prints
// one. A chat the source could not name for a person simply does not match.
func chatWithTargets(chat Chat) []string {
	targets := make([]string, 0, len(chat.ParticipantNames)+1)
	targets = append(targets, chat.ParticipantNames...)
	// A dm carries no member list on most sources; its one other person is the title
	// (Telegram, WhatsApp) or is already in the member list above (iMessage). A group
	// title is a subject, not a person, so it is not a --with target.
	if !chat.Group {
		if title := strings.TrimSpace(chat.Title); title != "" {
			targets = append(targets, title)
		}
	}
	return targets
}
