package photosarchive

import (
	"strings"
	"unicode"
)

// SearchTextWithEastAsianCharacterPairs prepares text for SQLite's word
// tokenizer. Chinese, Japanese and Korean have no spaces between words, so
// each run of those characters becomes overlapping two-character pieces:
// "成都茶社" becomes "成都 都茶 茶社", which a search for "茶社" matches.
// Search words must pass through the same function.
func SearchTextWithEastAsianCharacterPairs(text string) string {
	var preparedText strings.Builder
	var eastAsianRun []rune
	finishEastAsianRun := func() {
		if len(eastAsianRun) == 0 {
			return
		}
		preparedText.WriteByte(' ')
		if len(eastAsianRun) == 1 {
			preparedText.WriteString(string(eastAsianRun))
		}
		for index := 0; index+1 < len(eastAsianRun); index++ {
			if index > 0 {
				preparedText.WriteByte(' ')
			}
			preparedText.WriteString(string(eastAsianRun[index : index+2]))
		}
		preparedText.WriteByte(' ')
		eastAsianRun = eastAsianRun[:0]
	}
	for _, character := range text {
		if isEastAsianWordCharacter(character) {
			eastAsianRun = append(eastAsianRun, character)
			continue
		}
		finishEastAsianRun()
		preparedText.WriteRune(character)
	}
	finishEastAsianRun()
	return strings.TrimSpace(preparedText.String())
}

func isEastAsianWordCharacter(character rune) bool {
	return unicode.In(character, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
