package photosarchive

import "github.com/opentrawl/opentrawl/trawlkit/store"

// SearchTextWithEastAsianCharacterPairs prepares text for SQLite's word
// tokenizer using the shared East Asian character-pair expander in trawlkit/store.
func SearchTextWithEastAsianCharacterPairs(text string) string {
	return store.ReplaceEastAsianRunsWithCharacterPairsForFTS5(text)
}

func isEastAsianWordCharacter(character rune) bool {
	return store.IsEastAsianScriptRune(character)
}
