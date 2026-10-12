package photosarchive

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlkit"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type PhotoFactsSearchMatch struct {
	AppleLibraryPhotoFacts              *photo.AppleLibraryPhotoFacts
	SearchMatchTextFieldsInDisplayOrder []*search.SearchMatchTextField
}

type indexedPhotoSearchMatch struct {
	applePhotoUUID string
	markedFields   [6]string
}

func (photosArchive *PhotosArchive) SearchPhotoFacts(ctx context.Context, query trawlkit.Query) ([]PhotoFactsSearchMatch, uint64, error) {
	ftsQuery := photoSearchTokenQuery(query.Text)
	where := " where p.absent_from_apple_library_since_time_utc is null"
	var arguments []any
	photoSearchTables := ` from apple_library_photo p`
	selectedColumns := `select p.apple_photo_uuid`
	if ftsQuery != "" {
		where += " and apple_library_photo_search match ?"
		arguments = append(arguments, ftsQuery)
		photoSearchTables += ` join apple_library_photo_search on p.apple_photo_uuid=apple_library_photo_search.apple_photo_uuid`
		selectedColumns += `,
highlight(apple_library_photo_search,1,char(57344),char(57345)),
highlight(apple_library_photo_search,2,char(57344),char(57345)),
highlight(apple_library_photo_search,3,char(57344),char(57345)),
highlight(apple_library_photo_search,4,char(57344),char(57345)),
highlight(apple_library_photo_search,5,char(57344),char(57345)),
highlight(apple_library_photo_search,6,char(57344),char(57345))`
	}
	if !query.After.IsZero() {
		where += " and p.capture_time_utc >= ?"
		arguments = append(arguments, formatArchiveTime(query.After))
	}
	if !query.Before.IsZero() {
		where += " and p.capture_time_utc < ?"
		arguments = append(arguments, formatArchiveTime(query.Before))
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
	var matches []PhotoFactsSearchMatch
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
			textFields := photoSearchFieldsForFacts(fact, indexedMatches[first+index].markedFields)
			if ftsQuery == "" {
				textFields = photoSearchFieldsWithoutText(fact, query.PersonFilter)
			}
			matches = append(matches, PhotoFactsSearchMatch{AppleLibraryPhotoFacts: fact, SearchMatchTextFieldsInDisplayOrder: textFields})
		}
	}
	return matches, total, nil
}

func photoSearchTokenQuery(queryText string) string {
	var tokens []string
	var token strings.Builder
	finishToken := func() {
		if token.Len() == 0 {
			return
		}
		tokens = append(tokens, store.FTS5Phrase(SearchTextWithEastAsianCharacterPairs(token.String())))
		token.Reset()
	}
	for _, character := range queryText {
		if isPhotoSearchWordCharacter(character) {
			token.WriteRune(character)
		} else {
			finishToken()
		}
	}
	finishToken()
	return strings.Join(tokens, " ")
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

func originalPhotoSearchSourceFields(fact *photo.AppleLibraryPhotoFacts) []photoSearchSourceText {
	place := fact.GetAppleStoredPlace()
	fields := []photoSearchSourceText{
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CAPTION, 0, fact.GetAppleCaption()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TITLE, 0, fact.GetAppleTitle()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_POINT_OF_INTEREST, 1, place.GetAppleStoredPointOfInterestName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STREET, 1, place.GetAppleStoredStreet()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_NEIGHBOURHOOD, 1, place.GetAppleStoredNeighbourhoodName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TOWN, 1, place.GetAppleStoredTownName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTY, 1, place.GetAppleStoredCountyName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_STATE, 1, place.GetAppleStoredStateName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_COUNTRY, 1, place.GetAppleStoredCountryName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_WATER, 1, place.GetAppleStoredWaterName()},
		{photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_ISLAND, 1, place.GetAppleStoredIslandName()},
	}
	for _, area := range place.GetAppleStoredPlaceAreasSmallToLarge() {
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

func photoSearchFieldsForFacts(fact *photo.AppleLibraryPhotoFacts, markedFields [6]string) []*search.SearchMatchTextField {
	var coveredTerms [6]map[string]bool
	for index := range coveredTerms {
		coveredTerms[index] = map[string]bool{}
	}
	var fields []*search.SearchMatchTextField
	for _, source := range originalPhotoSearchSourceFields(fact) {
		indexedRuns := store.ParseFTS5MarkedText(markedFields[source.indexColumn])
		contributesMatch := false
		for _, run := range indexedRuns {
			if !run.Matched {
				continue
			}
			for _, term := range strings.Fields(run.Text) {
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
		if field := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(render.PhotoFactsSearchFieldDisplayName(source.sourceField), runs); field != nil {
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
				fields = append(fields, trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(render.PhotoFactsSearchFieldDisplayName(photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_PERSON), namedPerson.GetPersonFullName()))
			}
		}
		return fields
	}
	for _, source := range originalPhotoSearchSourceFields(fact) {
		if source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_TITLE && source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_CAPTION && source.sourceField != photo.PhotoSearchSourceField_PHOTO_SEARCH_SOURCE_FIELD_MOMENT {
			continue
		}
		if field := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(render.PhotoFactsSearchFieldDisplayName(source.sourceField), source.sourceText); field != nil {
			return []*search.SearchMatchTextField{field}
		}
	}
	return nil
}

// The FTS index inserts spaces between East Asian character pairs. Marks are
// transferred back to the unchanged source text so the index never becomes
// the human-readable evidence.
func originalPhotoTextRuns(originalText string, indexedRuns []store.FTS5TextRun) []store.FTS5TextRun {
	originalRunes := []rune(originalText)
	matchedRunes := make([]bool, len(originalRunes))
	for _, indexedRun := range indexedRuns {
		if !indexedRun.Matched {
			continue
		}
		for _, indexedTerm := range strings.Fields(indexedRun.Text) {
			term := []rune(indexedTerm)
			for first := 0; first+len(term) <= len(originalRunes); first++ {
				if !strings.EqualFold(string(originalRunes[first:first+len(term)]), indexedTerm) {
					continue
				}
				last := first + len(term)
				if len(term) > 0 && !isEastAsianWordCharacter(term[0]) &&
					((first > 0 && isPhotoSearchWordCharacter(originalRunes[first-1])) || (last < len(originalRunes) && isPhotoSearchWordCharacter(originalRunes[last]))) {
					continue
				}
				for index := first; index < first+len(term); index++ {
					matchedRunes[index] = true
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

func isPhotoSearchWordCharacter(character rune) bool {
	return unicode.IsLetter(character) || unicode.IsDigit(character) || unicode.IsMark(character) || character == '_'
}
