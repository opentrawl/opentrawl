package render

import (
	"strings"
	"unicode/utf8"
)

// A typed word is a near spelling of a known name when at most this many
// single-letter edits turn one into the other, and the edits change less
// than half of the word.
const maximumSingleLetterEditsForNearSpelling = 2

// didYouMeanSentence is " Did you mean "search"?" for the known name nearest
// to the first typed word, or empty when no known name is near.
func didYouMeanSentence(typedCommandWords string, knownNames []string) string {
	typedWords := strings.Fields(typedCommandWords)
	if len(typedWords) == 0 {
		return ""
	}
	typedWord := strings.ToLower(typedWords[0])
	nearestName := ""
	nearestEditCount := maximumSingleLetterEditsForNearSpelling + 1
	for _, knownName := range knownNames {
		editCount := singleLetterEditCount(typedWord, strings.ToLower(knownName))
		if editCount < nearestEditCount && editCount*2 < utf8.RuneCountInString(typedWord) {
			nearestName = knownName
			nearestEditCount = editCount
		}
	}
	if nearestName == "" || strings.EqualFold(nearestName, typedWord) {
		return ""
	}
	return " Did you mean " + quotedForSentence(nearestName) + "?"
}

// singleLetterEditCount is the Levenshtein distance: the fewest letters
// inserted, deleted or replaced to turn one word into the other.
func singleLetterEditCount(firstWord string, secondWord string) int {
	firstLetters := []rune(firstWord)
	secondLetters := []rune(secondWord)
	previousRow := make([]int, len(secondLetters)+1)
	for index := range previousRow {
		previousRow[index] = index
	}
	for firstIndex, firstLetter := range firstLetters {
		currentRow := make([]int, len(secondLetters)+1)
		currentRow[0] = firstIndex + 1
		for secondIndex, secondLetter := range secondLetters {
			replaceCost := previousRow[secondIndex]
			if firstLetter != secondLetter {
				replaceCost++
			}
			currentRow[secondIndex+1] = min(replaceCost, previousRow[secondIndex+1]+1, currentRow[secondIndex]+1)
		}
		previousRow = currentRow
	}
	return previousRow[len(secondLetters)]
}
