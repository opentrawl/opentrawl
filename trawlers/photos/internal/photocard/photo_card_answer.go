package photocard

import (
	"fmt"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// photoCardAnswer is the card model's answer read from its four labelled
// sections.
type photoCardAnswer struct {
	oneLineSummary      string
	description         string
	visibleTextGroups   []*photo.PhotoCardVisibleTextGroup
	placeShownName      string
	placeShownReasoning string
}

const (
	answerSectionSummary      = "summary"
	answerSectionDescription  = "description"
	answerSectionVisibleText  = "visible text"
	answerSectionPlaceShown   = "place shown"
	answerSectionHeadingStart = "## "
	visibleTextGroupStart     = "### "
	englishRenderingStart     = "English:"
)

var answerSectionsInOrder = []string{answerSectionSummary, answerSectionDescription, answerSectionVisibleText, answerSectionPlaceShown}

// parsePhotoCardAnswer reads the four sections. An answer that lacks a
// section, adds one, or gives no place reasoning is invalid, and the error
// says what was wrong.
func parsePhotoCardAnswer(rawModelAnswer string) (photoCardAnswer, error) {
	sectionTexts, err := answerSectionTexts(rawModelAnswer)
	if err != nil {
		return photoCardAnswer{}, err
	}
	answer := photoCardAnswer{
		oneLineSummary: strings.Join(strings.Fields(sectionTexts[answerSectionSummary]), " "),
		description:    sectionTexts[answerSectionDescription],
	}
	if answer.oneLineSummary == "" || answer.description == "" {
		return photoCardAnswer{}, fmt.Errorf("the summary or description section is empty")
	}
	if answer.visibleTextGroups, err = visibleTextGroupsFromAnswer(sectionTexts[answerSectionVisibleText]); err != nil {
		return photoCardAnswer{}, err
	}
	if err := answer.readPlaceShown(sectionTexts[answerSectionPlaceShown]); err != nil {
		return photoCardAnswer{}, err
	}
	return answer, nil
}

func answerSectionTexts(rawModelAnswer string) (map[string]string, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(rawModelAnswer), "\r\n", "\n"), "\n")
	sectionTexts := map[string]string{}
	var sectionNamesFound []string
	var currentSectionLines []string
	finishSection := func() {
		if len(sectionNamesFound) > 0 {
			sectionTexts[sectionNamesFound[len(sectionNamesFound)-1]] = strings.TrimSpace(strings.Join(currentSectionLines, "\n"))
		}
		currentSectionLines = nil
	}
	for lineIndex, line := range lines {
		if !strings.HasPrefix(line, answerSectionHeadingStart) {
			if len(sectionNamesFound) == 0 && strings.TrimSpace(line) != "" {
				return nil, fmt.Errorf("the answer has text before its first section heading, on line %d", lineIndex+1)
			}
			currentSectionLines = append(currentSectionLines, line)
			continue
		}
		finishSection()
		sectionNamesFound = append(sectionNamesFound, strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, answerSectionHeadingStart))))
	}
	finishSection()
	if strings.Join(sectionNamesFound, ", ") != strings.Join(answerSectionsInOrder, ", ") {
		return nil, fmt.Errorf("the answer's section headings are %q, not %q", sectionNamesFound, answerSectionsInOrder)
	}
	return sectionTexts, nil
}

func visibleTextGroupsFromAnswer(visibleTextSection string) ([]*photo.PhotoCardVisibleTextGroup, error) {
	if visibleTextSectionSaysNone(visibleTextSection) {
		return nil, nil
	}
	var groups []*photo.PhotoCardVisibleTextGroup
	var currentGroup *photo.PhotoCardVisibleTextGroup
	var writtenLines, englishLines []string
	inEnglishRendering := false
	finishGroup := func() {
		if currentGroup == nil {
			return
		}
		currentGroup.TextAsWritten = strings.TrimSpace(strings.Join(writtenLines, "\n"))
		currentGroup.EnglishRendering = strings.TrimSpace(strings.Join(englishLines, "\n"))
		groups = append(groups, currentGroup)
		writtenLines, englishLines, inEnglishRendering = nil, nil, false
	}
	for _, line := range strings.Split(visibleTextSection, "\n") {
		switch {
		case strings.HasPrefix(line, visibleTextGroupStart):
			finishGroup()
			currentGroup = &photo.PhotoCardVisibleTextGroup{WhereTextAppears: strings.TrimSpace(strings.TrimPrefix(line, visibleTextGroupStart))}
		case currentGroup == nil:
			if strings.TrimSpace(line) != "" {
				return nil, fmt.Errorf("the visible text section has text before its first %q line", strings.TrimSpace(visibleTextGroupStart))
			}
		case strings.HasPrefix(strings.TrimSpace(line), englishRenderingStart):
			if inEnglishRendering {
				return nil, fmt.Errorf("a visible text group has more than one %q line", englishRenderingStart)
			}
			inEnglishRendering = true
			englishLines = append(englishLines, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), englishRenderingStart)))
		case inEnglishRendering:
			englishLines = append(englishLines, line)
		default:
			writtenLines = append(writtenLines, line)
		}
	}
	finishGroup()
	for _, group := range groups {
		if group.GetWhereTextAppears() == "" || group.GetTextAsWritten() == "" {
			return nil, fmt.Errorf("a visible text group has no place or no text")
		}
	}
	return groups, nil
}

func (answer *photoCardAnswer) readPlaceShown(placeShownSection string) error {
	sawNameLine := false
	for _, line := range strings.Split(placeShownSection, "\n") {
		label, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "name":
			sawNameLine = true
			answer.placeShownName = value
		case "reasoning":
			answer.placeShownReasoning = value
		}
	}
	if !sawNameLine {
		return fmt.Errorf("the place shown section has no Name line")
	}
	if placeShownNameIsAbsentOrUnknown(answer.placeShownName) {
		answer.placeShownName = ""
	}
	if answer.placeShownReasoning == "" {
		return fmt.Errorf("the place shown section gives no reasoning")
	}
	return nil
}

func visibleTextSectionSaysNone(visibleTextSection string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(visibleTextSection))
	if trimmed == "none" || trimmed == "none." {
		return true
	}
	if strings.HasPrefix(visibleTextSection, visibleTextGroupStart) || strings.Contains(visibleTextSection, "\n"+visibleTextGroupStart) {
		return false
	}
	for _, prefix := range []string{"none.", "none ", "none—", "none -", "none:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func placeShownNameIsAbsentOrUnknown(name string) bool {
	trimmed := strings.ToLower(strings.Trim(strings.TrimSpace(name), `"'.`))
	if trimmed == "" || trimmed == "none" || trimmed == "unknown" {
		return true
	}
	for _, prefix := range []string{"none ", "none—", "none -", "none:", "unknown ", "unknown—", "unknown -", "unknown:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
