package trawlkit

import (
	"strings"
	"time"
	"unicode"

	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const maximumSearchMatchTextRunesBeforeFirstSearchQueryMatch = 5

type Query struct {
	Text                                            string
	Limit                                           int
	SearchTotalIsLowerBoundWhenResultLimitIsReached bool
	After, Before                                   time.Time
	Who                                             string
	WhoResolved                                     *WhoResolved
}

type WhoResolved struct {
	Who         string
	Identifiers []string
}

const MatchAnchorID = "match"

func NewPersonRelatedToSearchMatchingRecord(
	personDisplayName string,
	personRoleInMatchingRecord personv1.PersonRoleInArchiveRecord,
) *personv1.PersonRelatedToArchiveRecord {
	personDisplayName = strings.Join(strings.Fields(personDisplayName), " ")
	if personDisplayName == "" {
		return nil
	}
	return &personv1.PersonRelatedToArchiveRecord{
		PersonDisplayName:         personDisplayName,
		PersonRoleInArchiveRecord: personRoleInMatchingRecord,
	}
}

func newSearchMatchTextFieldFromMatcherFragments(
	searchMatchTextFieldName string,
	matcherOwnedTextFragments []*searchv1.SearchMatchTextFragment,
) *searchv1.SearchMatchTextField {
	searchMatchTextFieldName = strings.Join(strings.Fields(searchMatchTextFieldName), " ")
	searchMatchTextFragmentsInDisplayOrder := make(
		[]*searchv1.SearchMatchTextFragment,
		0,
		len(matcherOwnedTextFragments),
	)
	for _, matcherOwnedTextFragment := range matcherOwnedTextFragments {
		if matcherOwnedTextFragment == nil || matcherOwnedTextFragment.GetSearchMatchTextFragmentContent() == "" {
			continue
		}
		searchMatchTextFragmentsInDisplayOrder = append(
			searchMatchTextFragmentsInDisplayOrder,
			matcherOwnedTextFragment,
		)
	}
	if searchMatchTextFieldName == "" || len(searchMatchTextFragmentsInDisplayOrder) == 0 {
		return nil
	}
	return &searchv1.SearchMatchTextField{
		SearchMatchTextFieldName:               searchMatchTextFieldName,
		SearchMatchTextFragmentsInDisplayOrder: searchMatchTextFragmentsInDisplayOrder,
	}
}

func NewSearchMatchTextFieldFromFTS5TextRuns(
	searchMatchTextFieldName string,
	fts5TextRuns []ckstore.FTS5TextRun,
) *searchv1.SearchMatchTextField {
	firstSearchQueryMatchingTextRunIndex := -1
	for textRunIndex, fts5TextRun := range fts5TextRuns {
		if fts5TextRun.Matched && fts5TextRun.Text != "" {
			firstSearchQueryMatchingTextRunIndex = textRunIndex
			break
		}
	}
	if firstSearchQueryMatchingTextRunIndex < 0 {
		return nil
	}
	matcherOwnedTextFragments := make(
		[]*searchv1.SearchMatchTextFragment,
		0,
		len(fts5TextRuns)-firstSearchQueryMatchingTextRunIndex+1,
	)
	if firstSearchQueryMatchingTextRunIndex > 0 {
		var textBeforeFirstSearchQueryMatch strings.Builder
		for _, fts5TextRun := range fts5TextRuns[:firstSearchQueryMatchingTextRunIndex] {
			textBeforeFirstSearchQueryMatch.WriteString(fts5TextRun.Text)
		}
		searchResultTextImmediatelyBeforeFirstQueryMatch := boundedTextBeforeFirstSearchQueryMatch(
			textBeforeFirstSearchQueryMatch.String(),
		)
		if searchResultTextImmediatelyBeforeFirstQueryMatch != "" {
			matcherOwnedTextFragments = append(
				matcherOwnedTextFragments,
				&searchv1.SearchMatchTextFragment{
					SearchMatchTextFragmentContent: searchResultTextImmediatelyBeforeFirstQueryMatch,
				},
			)
		}
	}
	for _, fts5TextRun := range fts5TextRuns[firstSearchQueryMatchingTextRunIndex:] {
		if fts5TextRun.Text == "" {
			continue
		}
		matcherOwnedTextFragments = append(
			matcherOwnedTextFragments,
			&searchv1.SearchMatchTextFragment{
				SearchMatchTextFragmentContent:            fts5TextRun.Text,
				SearchMatchTextFragmentMatchesSearchQuery: fts5TextRun.Matched,
			},
		)
	}
	return newSearchMatchTextFieldFromMatcherFragments(
		searchMatchTextFieldName,
		matcherOwnedTextFragments,
	)
}

func boundedTextBeforeFirstSearchQueryMatch(textBeforeFirstSearchQueryMatch string) string {
	textRunes := []rune(textBeforeFirstSearchQueryMatch)
	firstIncludedRuneIndex := 0
	textWasShortened := false
	if len(textRunes) > maximumSearchMatchTextRunesBeforeFirstSearchQueryMatch {
		firstIncludedRuneIndex = len(textRunes) - maximumSearchMatchTextRunesBeforeFirstSearchQueryMatch
		textWasShortened = true
		if !unicode.IsSpace(textRunes[firstIncludedRuneIndex-1]) &&
			!unicode.IsSpace(textRunes[firstIncludedRuneIndex]) {
			for firstIncludedRuneIndex < len(textRunes) && !unicode.IsSpace(textRunes[firstIncludedRuneIndex]) {
				firstIncludedRuneIndex++
			}
		}
	}
	boundedText := strings.Join(strings.Fields(string(textRunes[firstIncludedRuneIndex:])), " ")
	if boundedText == "" {
		if textWasShortened {
			return "…"
		}
		return ""
	}
	if textWasShortened {
		boundedText = "…" + boundedText
	}
	if unicode.IsSpace(textRunes[len(textRunes)-1]) {
		boundedText += " "
	}
	return boundedText
}

func NewSearchMatchTextFieldWithoutSearchQueryMatch(
	searchMatchTextFieldName string,
	searchMatchTextFieldContent string,
) *searchv1.SearchMatchTextField {
	searchMatchTextFieldContent = strings.TrimSpace(searchMatchTextFieldContent)
	if searchMatchTextFieldContent == "" {
		return nil
	}
	return newSearchMatchTextFieldFromMatcherFragments(
		searchMatchTextFieldName,
		[]*searchv1.SearchMatchTextFragment{{
			SearchMatchTextFragmentContent: searchMatchTextFieldContent,
		}},
	)
}
