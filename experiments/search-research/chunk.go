package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type searchablePassage struct {
	content                    string
	sectionStartUTF8ByteOffset int
	sectionEndUTF8ByteOffset   int
}

func splitSearchableRecordTextSectionIntoPassages(sectionContent string) []searchablePassage {
	sectionContent, sectionStartOffset := trimLeadingSpace(sectionContent, 0)
	sectionContent = strings.TrimRightFunc(sectionContent, unicode.IsSpace)
	if sectionContent == "" {
		return nil
	}
	passages := make([]searchablePassage, 0, len(sectionContent)/maximumSearchablePassageContentUTF8Bytes+1)
	remainingContent := sectionContent
	remainingStartOffset := sectionStartOffset
	for len(remainingContent) > maximumSearchablePassageContentUTF8Bytes {
		splitByteOffset := preferredPassageSplitByteOffset(remainingContent, maximumSearchablePassageContentUTF8Bytes)
		rawPassage := remainingContent[:splitByteOffset]
		passageContent := strings.TrimSpace(rawPassage)
		if passageContent != "" {
			leadingSpaceBytes := len(rawPassage) - len(strings.TrimLeftFunc(rawPassage, unicode.IsSpace))
			passages = append(passages, searchablePassage{
				content:                    passageContent,
				sectionStartUTF8ByteOffset: remainingStartOffset + leadingSpaceBytes,
				sectionEndUTF8ByteOffset:   remainingStartOffset + leadingSpaceBytes + len(passageContent),
			})
		}
		remainingStartOffset += splitByteOffset
		remainingContent, remainingStartOffset = trimLeadingSpace(remainingContent[splitByteOffset:], remainingStartOffset)
	}
	if remainingContent != "" {
		passages = append(passages, searchablePassage{
			content:                    remainingContent,
			sectionStartUTF8ByteOffset: remainingStartOffset,
			sectionEndUTF8ByteOffset:   remainingStartOffset + len(remainingContent),
		})
	}
	return passages
}

func trimLeadingSpace(text string, absoluteStartOffset int) (string, int) {
	trimmed := strings.TrimLeftFunc(text, unicode.IsSpace)
	return trimmed, absoluteStartOffset + len(text) - len(trimmed)
}

func preferredPassageSplitByteOffset(text string, maximumUTF8Bytes int) int {
	if len(text) <= maximumUTF8Bytes {
		return len(text)
	}
	maximumUTF8Bytes = lastCompleteRuneBoundaryAtOrBefore(text, maximumUTF8Bytes)
	window := text[:maximumUTF8Bytes]
	for _, separator := range []string{"\n\n", "\n"} {
		if splitOffset := strings.LastIndex(window, separator); splitOffset > 0 {
			return splitOffset + len(separator)
		}
	}
	lastSentenceBoundary := -1
	lastWordBoundary := -1
	for byteOffset, currentRune := range window {
		if unicode.IsSpace(currentRune) {
			lastWordBoundary = byteOffset + utf8.RuneLen(currentRune)
			previousText := strings.TrimSpace(window[:byteOffset])
			if previousText != "" {
				lastRune, _ := utf8.DecodeLastRuneInString(previousText)
				if lastRune == '.' || lastRune == '?' || lastRune == '!' {
					lastSentenceBoundary = byteOffset + utf8.RuneLen(currentRune)
				}
			}
		}
	}
	if lastSentenceBoundary > 0 {
		return lastSentenceBoundary
	}
	if lastWordBoundary > 0 {
		return lastWordBoundary
	}
	return maximumUTF8Bytes
}

func lastCompleteRuneBoundaryAtOrBefore(text string, byteOffset int) int {
	if byteOffset >= len(text) {
		return len(text)
	}
	for byteOffset > 0 && !utf8.RuneStart(text[byteOffset]) {
		byteOffset--
	}
	return byteOffset
}
