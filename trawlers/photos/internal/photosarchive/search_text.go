package photosarchive

import (
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// SearchTextWithEastAsianCharactersSeparated prepares text for SQLite's word
// tokenizer. Chinese, Japanese and Korean have no spaces between words, so
// each of those characters becomes its own token: "成都茶社" becomes
// "成 都 茶 社". A search for one character matches it alone, and a search for
// several matches them only when they stand together, as an FTS5 phrase.
// Search words must pass through the same function.
func SearchTextWithEastAsianCharactersSeparated(text string) string {
	var preparedText strings.Builder
	for _, character := range text {
		if isEastAsianWordCharacter(character) {
			preparedText.WriteByte(' ')
			preparedText.WriteRune(character)
			preparedText.WriteByte(' ')
			continue
		}
		preparedText.WriteRune(character)
	}
	return strings.Join(strings.Fields(preparedText.String()), " ")
}

func isEastAsianWordCharacter(character rune) bool {
	return store.IsEastAsianScriptRune(character)
}
