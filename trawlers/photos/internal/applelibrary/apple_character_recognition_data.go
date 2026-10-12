package applelibrary

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Apple's on-device text recognition result is a keyed archive holding an
// LZFSE-compressed tree of output regions: blocks, lines, words and, for
// Chinese, Japanese and Korean, single characters. A word or character region
// carries its text as a length-prefixed UTF-8 string shortly after its class
// name. Apple stores up to four candidate readings of each line, best first,
// and repeats the lines of a table; all share the line's position, so a line
// at a position already seen is skipped.
var (
	appleRecognisedLineRegionClassName      = lengthPrefixedClassName("CRLineOutputRegion")
	appleRecognisedWordRegionClassName      = lengthPrefixedClassName("CRWordOutputRegion")
	appleRecognisedCharacterRegionClassName = lengthPrefixedClassName("CRCharacterOutputRegion")
)

const (
	appleRecognisedWordSearchWindowByteCount = 400
	appleRecognisedWordMaximumByteCount      = 400
	// The four corner points of a line, eight doubles in pixels, start this
	// many bytes after the line region's class name.
	appleRecognisedLinePositionByteOffset = 36
	appleRecognisedLinePositionByteCount  = 64
)

func lengthPrefixedClassName(className string) []byte {
	return append(binary.LittleEndian.AppendUint64(nil, uint64(len(className))), className...)
}

func decodeAppleCharacterRecognitionData(characterRecognitionData []byte) ([]string, error) {
	keyedArchive, err := decodeAppleKeyedArchive(characterRecognitionData)
	if err != nil {
		return nil, err
	}
	var compressedRegionData []byte
	var uncompressedRegionByteCount float64
	for _, archivedObject := range keyedArchive.archivedObjects {
		outputRegion, ok := archivedObject.(map[string]any)
		if !ok {
			continue
		}
		if regionData, ok := keyedArchive.resolve(outputRegion["kCROutputRegionData"]).([]byte); ok {
			compressedRegionData = regionData
			uncompressedRegionByteCount, _ = keyedArchive.number(outputRegion["kCROutputRegionUncompressedDataSize"])
			break
		}
	}
	if len(compressedRegionData) == 0 || uncompressedRegionByteCount <= 0 {
		return nil, fmt.Errorf("recognised text archive has no region data")
	}
	regionData, err := decompressAppleLZFSE(compressedRegionData, int(uncompressedRegionByteCount))
	if err != nil {
		return nil, err
	}
	return recognisedTextLinesFromRegionData(regionData), nil
}

type appleRecognisedRegionKind int

const (
	appleRecognisedLineRegion appleRecognisedRegionKind = iota
	appleRecognisedWordRegion
	appleRecognisedCharacterRegion
)

type appleRecognisedRegionMarker struct {
	byteOffset int
	regionKind appleRecognisedRegionKind
}

type appleRecognisedLine struct {
	linePositionKey string
	words           []string
}

func recognisedTextLinesFromRegionData(regionData []byte) []string {
	var regionMarkers []appleRecognisedRegionMarker
	for regionKind, className := range map[appleRecognisedRegionKind][]byte{
		appleRecognisedLineRegion:      appleRecognisedLineRegionClassName,
		appleRecognisedWordRegion:      appleRecognisedWordRegionClassName,
		appleRecognisedCharacterRegion: appleRecognisedCharacterRegionClassName,
	} {
		for _, byteOffset := range allIndexes(regionData, className) {
			regionMarkers = append(regionMarkers, appleRecognisedRegionMarker{byteOffset: byteOffset, regionKind: regionKind})
		}
	}
	sort.Slice(regionMarkers, func(left, right int) bool {
		return regionMarkers[left].byteOffset < regionMarkers[right].byteOffset
	})
	var recognisedLines []*appleRecognisedLine
	for _, regionMarker := range regionMarkers {
		if regionMarker.regionKind == appleRecognisedLineRegion {
			recognisedLines = append(recognisedLines, &appleRecognisedLine{
				linePositionKey: recognisedLinePositionKey(regionData, regionMarker.byteOffset+len(appleRecognisedLineRegionClassName)),
			})
			continue
		}
		if len(recognisedLines) == 0 {
			continue
		}
		classNameByteCount := len(appleRecognisedWordRegionClassName)
		if regionMarker.regionKind == appleRecognisedCharacterRegion {
			classNameByteCount = len(appleRecognisedCharacterRegionClassName)
		}
		text, ok := firstLengthPrefixedWordAfter(regionData, regionMarker.byteOffset+classNameByteCount)
		if !ok {
			continue
		}
		currentLine := recognisedLines[len(recognisedLines)-1]
		if regionMarker.regionKind == appleRecognisedCharacterRegion && len(currentLine.words) > 0 {
			currentLine.words[len(currentLine.words)-1] += text
			continue
		}
		currentLine.words = append(currentLine.words, text)
	}
	var recognisedTextLines []string
	seenLinePositionKeys := map[string]bool{}
	for _, recognisedLine := range recognisedLines {
		lineWasSeen := seenLinePositionKeys[recognisedLine.linePositionKey]
		seenLinePositionKeys[recognisedLine.linePositionKey] = true
		if lineWasSeen || len(recognisedLine.words) == 0 {
			continue
		}
		recognisedTextLines = append(recognisedTextLines, strings.Join(recognisedLine.words, " "))
	}
	return recognisedTextLines
}

// recognisedLinePositionKey is the line's corner points rounded to whole
// pixels. A line whose position cannot be read gets a key of its own.
func recognisedLinePositionKey(regionData []byte, linePositionStart int) string {
	byteOffset := linePositionStart + appleRecognisedLinePositionByteOffset
	if byteOffset+appleRecognisedLinePositionByteCount > len(regionData) {
		return fmt.Sprintf("unreadable position at byte %d", linePositionStart)
	}
	cornerCoordinates := make([]string, 0, appleRecognisedLinePositionByteCount/8)
	for coordinateIndex := range appleRecognisedLinePositionByteCount / 8 {
		coordinate := math.Float64frombits(binary.LittleEndian.Uint64(regionData[byteOffset+coordinateIndex*8:]))
		cornerCoordinates = append(cornerCoordinates, strconv.FormatInt(int64(math.Round(coordinate)), 10))
	}
	return strings.Join(cornerCoordinates, ",")
}

func allIndexes(data, pattern []byte) []int {
	var indexes []int
	for searchStart := 0; ; {
		index := bytes.Index(data[searchStart:], pattern)
		if index < 0 {
			return indexes
		}
		indexes = append(indexes, searchStart+index)
		searchStart += index + len(pattern)
	}
}

func firstLengthPrefixedWordAfter(regionData []byte, searchStart int) (string, bool) {
	searchEnd := min(len(regionData)-9, searchStart+appleRecognisedWordSearchWindowByteCount)
	for byteOffset := searchStart; byteOffset < searchEnd; byteOffset++ {
		wordByteCount := binary.LittleEndian.Uint64(regionData[byteOffset:])
		if wordByteCount < 1 || wordByteCount > appleRecognisedWordMaximumByteCount || byteOffset+8+int(wordByteCount) > len(regionData) {
			continue
		}
		wordBytes := regionData[byteOffset+8 : byteOffset+8+int(wordByteCount)]
		if utf8.Valid(wordBytes) && isPrintableText(string(wordBytes)) {
			return string(wordBytes), true
		}
	}
	return "", false
}

func isPrintableText(text string) bool {
	for _, character := range text {
		if !unicode.IsPrint(character) {
			return false
		}
	}
	return true
}
