package photosarchive

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/archivetime"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type PhotoSearchMatch struct {
	AppleLibraryPhotoFacts *photo.AppleLibraryPhotoFacts
	// Nil when the photo has no written card.
	WrittenPhotoCard                    *photo.PhotoCard
	SearchMatchTextFieldsInDisplayOrder []*search.SearchMatchTextField
}

const photoSearchIndexedColumnCount = 10

type indexedPhotoSearchMatch struct {
	applePhotoUUID string
	markedFields   [photoSearchIndexedColumnCount]string
}

// SearchPhotos matches words against each photo's one search row, which holds
// its Apple facts and its written card together.
func (photosArchive *PhotosArchive) SearchPhotos(ctx context.Context, query trawlkit.Query) ([]PhotoSearchMatch, uint64, error) {
	ftsQuery := photoSearchTokenQuery(query.Text)
	if strings.TrimSpace(query.Text) != "" && ftsQuery == "" {
		return nil, 0, nil
	}
	where := " where p.absent_from_apple_library_since_time_utc is null"
	var arguments []any
	photoSearchTables := ` from apple_library_photo p`
	selectedColumns := `select p.apple_photo_uuid`
	if ftsQuery != "" {
		where += " and photo_search match ?"
		arguments = append(arguments, ftsQuery)
		photoSearchTables += ` join photo_search on p.rowid=photo_search.rowid`
		for indexColumn := range photoSearchIndexedColumnCount {
			selectedColumns += fmt.Sprintf(",\nhighlight(photo_search,%d,char(57344),char(57345))", indexColumn+1)
		}
	}
	var timeClauses []string
	archivetime.NewInclusiveTimeRange(query.After, query.Before).AppendRFC3339UTCSQLClauses(
		"p.capture_time_utc",
		&timeClauses,
		&arguments,
	)
	for _, clause := range timeClauses {
		where += " and " + clause
	}
	personFilterIsSet := query.PersonFilter.UnresolvedPersonFilterText() != "" || query.PersonFilter.ResolvedPersonFilter() != nil
	var total uint64
	rowLimit := ""
	if !personFilterIsSet && query.Limit > 0 {
		if err := photosArchive.database.QueryRowContext(ctx, "select count(*)"+photoSearchTables+where, arguments...).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count photo search matches: %w", err)
		}
		rowLimit = " limit ?"
		arguments = append(arguments, query.Limit)
	}
	rows, err := photosArchive.database.QueryContext(ctx, selectedColumns+photoSearchTables+where+` order by p.capture_time_utc desc,p.apple_photo_uuid`+rowLimit, arguments...)
	if err != nil {
		return nil, 0, fmt.Errorf("search Apple photo facts: %w", err)
	}
	var indexedMatches []indexedPhotoSearchMatch
	for rows.Next() {
		var match indexedPhotoSearchMatch
		columns := []any{&match.applePhotoUUID}
		if ftsQuery != "" {
			for index := range match.markedFields {
				columns = append(columns, &match.markedFields[index])
			}
		}
		if err := rows.Scan(columns...); err != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("read photo search match: %w", err)
		}
		indexedMatches = append(indexedMatches, match)
	}
	rowError := rows.Err()
	_ = rows.Close()
	if rowError != nil {
		return nil, 0, fmt.Errorf("read photo search matches: %w", rowError)
	}
	var matches []PhotoSearchMatch
	if !personFilterIsSet && query.Limit <= 0 {
		total = uint64(len(indexedMatches))
	}
	for first := 0; first < len(indexedMatches); first += 500 {
		last := min(first+500, len(indexedMatches))
		uuidBatch := make([]string, 0, last-first)
		for _, match := range indexedMatches[first:last] {
			uuidBatch = append(uuidBatch, match.applePhotoUUID)
		}
		facts, err := photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, uuidBatch)
		if err != nil {
			return nil, 0, err
		}
		for index, fact := range facts {
			if !photoSearchPersonMatches(fact, query.PersonFilter) {
				continue
			}
			if personFilterIsSet {
				total++
			}
			if query.Limit > 0 && len(matches) >= query.Limit {
				continue
			}
			writtenPhotoCard, err := photosArchive.ReadStoredPhotoCard(ctx, fact.GetApplePhotoUuid())
			if err != nil {
				return nil, 0, err
			}
			if writtenPhotoCard.GetPhotoCardOutcome() != photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN {
				writtenPhotoCard = nil
			}
			textFields := photoSearchFieldsForMatchedText(fact, writtenPhotoCard, indexedMatches[first+index].markedFields)
			if ftsQuery == "" {
				textFields = photoSearchFieldsWithoutText(fact, query.PersonFilter)
			}
			matches = append(matches, PhotoSearchMatch{AppleLibraryPhotoFacts: fact, WrittenPhotoCard: writtenPhotoCard, SearchMatchTextFieldsInDisplayOrder: textFields})
		}
	}
	return matches, total, nil
}

func photoSearchTokenQuery(queryText string) string {
	return store.FTS5QueryFromWordTokens(queryText, SearchTextWithEastAsianCharactersSeparated)
}

func photoSearchPersonMatches(fact *photo.AppleLibraryPhotoFacts, filter trawlkit.SearchPersonFilter) bool {
	if filter.ResolvedPersonFilter() == nil && filter.UnresolvedPersonFilterText() == "" {
		return true
	}
	for _, namedPerson := range fact.GetAppleNamedPeople() {
		if photoNamedPersonMatchesSearchFilter(namedPerson, filter) {
			return true
		}
	}
	return false
}

func photoNamedPersonMatchesSearchFilter(namedPerson *photo.AppleLibraryPhotoNamedPerson, filter trawlkit.SearchPersonFilter) bool {
	if resolved := filter.ResolvedPersonFilter(); resolved != nil {
		if strings.EqualFold(namedPerson.GetPersonFullName(), resolved.PersonFilterText) {
			return true
		}
		for _, identifier := range resolved.ExactPersonFilterIdentifiers {
			if strings.EqualFold(namedPerson.GetApplePersonUuid(), identifier.GetExactPersonFilterIdentifier()) {
				return true
			}
		}
		return false
	}
	return containsPhotoFieldIgnoringCase(namedPerson.GetPersonFullName(), filter.UnresolvedPersonFilterText())
}

type photoSearchSourceText struct {
	sourceField photo.PhotoSearchSourceField
	indexColumn int
	sourceText  string
}

// photoSearchSourceTexts lists each searchable text in display order, with
// the search column it is indexed in. A card adds its text only when it was
// written.
func photoSearchSourceTexts(fact *photo.AppleLibraryPhotoFacts, writtenPhotoCard *photo.PhotoCard) []photoSearchSourceText {
	var fields []photoSearchSourceText
	if writtenPhotoCard.GetPhotoCardOutcome() == photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN {
		fields = append(fields,
			photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CARD_ONE_LINE_SUMMARY, 6, writtenPhotoCard.GetOneLineSummary()},
			photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CARD_PLACE_SHOWN, 9, writtenPhotoCard.GetPlaceShownName()},
			photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CARD_DESCRIPTION, 7, writtenPhotoCard.GetDescription()},
		)
		for _, visibleTextGroup := range writtenPhotoCard.GetVisibleTextGroups() {
			fields = append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CARD_VISIBLE_TEXT, 8,
				joinNonEmptyLines(visibleTextGroup.GetTextAsWritten(), visibleTextGroup.GetEnglishRendering())})
		}
	}
	place := fact.GetAppleStoredPlace()
	fields = append(fields,
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CAPTION, 0, fact.GetAppleCaption()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TITLE, 0, fact.GetAppleTitle()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_POINT_OF_INTEREST, 1, place.GetAppleStoredPointOfInterestName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STREET, 1, place.GetAppleStoredStreet()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_NEIGHBOURHOOD, 1, place.GetAppleStoredNeighbourhoodName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TOWN, 1, place.GetAppleStoredTownName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTY, 1, place.GetAppleStoredCountyName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STATE, 1, place.GetAppleStoredStateName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTRY, 1, place.GetAppleStoredCountryName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_WATER, 1, place.GetAppleStoredWaterName()},
		photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ISLAND, 1, place.GetAppleStoredIslandName()},
	)
	for _, area := range place.GetAppleStoredPlaceAreasInSourceOrder() {
		fields = append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PLACE_AREA, 1, area.GetAreaName()})
	}
	fields = append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_FULL_ADDRESS, 1, place.GetAppleStoredFullAddress()}, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ISO_COUNTRY_CODE, 1, place.GetAppleStoredIsoCountryCode()})
	for _, person := range fact.GetAppleNamedPeople() {
		fields = append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PERSON, 2, person.GetPersonFullName()})
	}
	for _, album := range fact.GetAppleAlbums() {
		fields = append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ALBUM, 3, album.GetAlbumTitle()})
	}
	return append(fields, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_MOMENT, 4, fact.GetAppleMomentTitle()}, photoSearchSourceText{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_RECOGNISED_TEXT, 5, joinNonEmptyLines(fact.GetAppleRecognisedTextLines()...)})
}

func photoSearchFieldsForMatchedText(fact *photo.AppleLibraryPhotoFacts, writtenPhotoCard *photo.PhotoCard, markedFields [photoSearchIndexedColumnCount]string) []*search.SearchMatchTextField {
	var coveredTerms [photoSearchIndexedColumnCount]map[string]bool
	for index := range coveredTerms {
		coveredTerms[index] = map[string]bool{}
	}
	var fields []*search.SearchMatchTextField
	for _, source := range photoSearchSourceTexts(fact, writtenPhotoCard) {
		indexedRuns := store.ParseFTS5MarkedText(markedFields[source.indexColumn])
		contributesMatch := false
		for _, run := range indexedRuns {
			if !run.Matched {
				continue
			}
			for _, term := range indexedMatchTerms(run.Text) {
				key := strings.ToLower(term)
				if coveredTerms[source.indexColumn][key] {
					continue
				}
				for _, sourceRun := range originalPhotoTextRuns(source.sourceText, []store.FTS5TextRun{{Text: term, Matched: true}}) {
					if sourceRun.Matched {
						contributesMatch = true
						coveredTerms[source.indexColumn][key] = true
						break
					}
				}
			}
		}
		if !contributesMatch {
			continue
		}
		runs := originalPhotoTextRuns(source.sourceText, indexedRuns)
		if field := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(render.PhotoSearchFieldDisplayName(source.sourceField), runs); field != nil {
			fields = append(fields, field)
		}
	}
	return fields
}

func photoSearchFieldsWithoutText(fact *photo.AppleLibraryPhotoFacts, filter trawlkit.SearchPersonFilter) []*search.SearchMatchTextField {
	if filter.ResolvedPersonFilter() != nil || filter.UnresolvedPersonFilterText() != "" {
		var fields []*search.SearchMatchTextField
		for _, namedPerson := range fact.GetAppleNamedPeople() {
			if photoNamedPersonMatchesSearchFilter(namedPerson, filter) {
				fields = append(fields, trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(render.PhotoSearchFieldDisplayName(photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PERSON), namedPerson.GetPersonFullName()))
			}
		}
		return fields
	}
	for _, source := range photoSearchSourceTexts(fact, nil) {
		if source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TITLE && source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CAPTION && source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_MOMENT {
			continue
		}
		if field := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(render.PhotoSearchFieldDisplayName(source.sourceField), source.sourceText); field != nil {
			return []*search.SearchMatchTextField{field}
		}
	}
	return nil
}

// The FTS index inserts spaces around each East Asian character. Marks are
// transferred back to the unchanged source text so the index never becomes
// the human-readable evidence.
func originalPhotoTextRuns(originalText string, indexedRuns []store.FTS5TextRun) []store.FTS5TextRun {
	originalRunes := []rune(originalText)
	matchedRunes := make([]bool, len(originalRunes))
	for _, indexedRun := range indexedRuns {
		if !indexedRun.Matched {
			continue
		}
		for _, indexedTerm := range indexedMatchTerms(indexedRun.Text) {
			if markOriginalPhotoTerm(originalRunes, matchedRunes, indexedTerm) {
				continue
			}
			termRunes := []rune(indexedTerm)
			if len(termRunes) > 1 && isEastAsianWordCharacter(termRunes[0]) {
				for _, singleCharacter := range termRunes {
					markOriginalPhotoTerm(originalRunes, matchedRunes, string(singleCharacter))
				}
			}
		}
	}
	var runs []store.FTS5TextRun
	for first := 0; first < len(originalRunes); {
		last := first + 1
		for last < len(originalRunes) && matchedRunes[last] == matchedRunes[first] {
			last++
		}
		runs = append(runs, store.FTS5TextRun{Text: string(originalRunes[first:last]), Matched: matchedRunes[first]})
		first = last
	}
	return runs
}

func markOriginalPhotoTerm(originalRunes []rune, matchedRunes []bool, indexedTerm string) bool {
	term := []rune(indexedTerm)
	if len(term) == 0 {
		return false
	}
	marked := false
	for first := 0; first+len(term) <= len(originalRunes); first++ {
		if !strings.EqualFold(string(originalRunes[first:first+len(term)]), indexedTerm) {
			continue
		}
		last := first + len(term)
		if !isEastAsianWordCharacter(term[0]) &&
			((first > 0 && isNonEastAsianPhotoSearchWordCharacter(originalRunes[first-1])) ||
				(last < len(originalRunes) && isNonEastAsianPhotoSearchWordCharacter(originalRunes[last]))) {
			continue
		}
		for index := first; index < last; index++ {
			matchedRunes[index] = true
		}
		marked = true
	}
	return marked
}

func isNonEastAsianPhotoSearchWordCharacter(character rune) bool {
	return isPhotoSearchWordCharacter(character) && !isEastAsianWordCharacter(character)
}

// indexedMatchTerms splits one highlighted run of the search index into the
// terms to find in the source text. The index separates each East Asian
// character with spaces; a phrase of them is joined again so that only the
// whole phrase is marked in the source, not each of its characters wherever
// it appears.
func indexedMatchTerms(indexedRunText string) []string {
	var terms []string
	for _, field := range strings.Fields(indexedRunText) {
		last := len(terms) - 1
		fieldRunes, lastRunes := []rune(field), []rune("")
		if last >= 0 {
			lastRunes = []rune(terms[last])
		}
		if last >= 0 && len(fieldRunes) == 1 && isEastAsianWordCharacter(fieldRunes[0]) && isEastAsianWordCharacter(lastRunes[len(lastRunes)-1]) {
			terms[last] += field
			continue
		}
		terms = append(terms, field)
	}
	return terms
}

func isPhotoSearchWordCharacter(character rune) bool {
	return store.IsFTS5WordRune(character)
}
