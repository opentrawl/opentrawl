package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"golang.org/x/text/unicode/norm"
)

func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int, error) {
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	exactPersonFilterIdentifierSet := map[string]struct{}{}
	for _, exactPersonFilterIdentifier := range options.ExactPersonFilterIdentifiers {
		exactPersonFilterIdentifierText := strings.ToLower(strings.TrimSpace(
			exactPersonFilterIdentifier.GetExactPersonFilterIdentifier(),
		))
		if exactPersonFilterIdentifierText != "" {
			exactPersonFilterIdentifierSet[exactPersonFilterIdentifierText] = struct{}{}
		}
	}
	if query == "" && len(exactPersonFilterIdentifierSet) == 0 {
		return []SearchResult{}, 0, nil
	}
	people, err := s.People(ctx)
	if err != nil {
		return nil, 0, err
	}
	byID := map[string]model.Person{}
	for _, person := range people {
		byID[person.ID] = person
	}
	hits := []scoredHit{}
	for _, person := range people {
		if len(exactPersonFilterIdentifierSet) > 0 {
			personIdentifierValues := append(
				[]string{person.ID},
				exactPersonFilterIdentifiersFromTrawlerArchives(personMatchFactsFromTrawlers(person))...,
			)
			personMatchesExactFilterIdentifier := false
			for _, personIdentifierValue := range personIdentifierValues {
				if _, included := exactPersonFilterIdentifierSet[strings.ToLower(strings.TrimSpace(personIdentifierValue))]; included {
					personMatchesExactFilterIdentifier = true
					break
				}
			}
			if !personMatchesExactFilterIdentifier {
				continue
			}
		}
		matches := personSearchMatches(person, query)
		if query == "" || len(matches) > 0 {
			hits = append(hits, scoredHit{AnchorID: personSearchAnchor(matches), PersonID: person.ID, Who: person.Name, AccountProviderName: personSearchMatchedAccountProviderName(matches), Score: 100, Snippet: firstSearchMatchText(matches, person.Name), Matches: matches})
		}
		if query == "" {
			continue
		}
		notes, err := s.Notes(ctx, person.ID)
		if err != nil {
			return nil, 0, err
		}
		for _, note := range notes {
			text := strings.ToLower(strings.Join(append([]string{note.Kind, note.Source, note.Body}, note.Topics...), " "))
			if score := scoreText(text, query); score > 0 {
				hits = append(hits, scoredHit{AnchorID: NoteAnchorID(note.ID), PersonID: person.ID, Who: person.Name, AccountProviderName: note.Source, Score: score, Snippet: noteSnippet(note, query), Time: note.OccurredAt, Matches: noteSearchMatches(note, query)})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].PersonID < hits[j].PersonID
		}
		return hits[i].Score > hits[j].Score
	})
	resultCapacity := len(hits)
	if options.Limit > 0 && options.Limit < resultCapacity {
		resultCapacity = options.Limit
	}
	results := make([]SearchResult, 0, resultCapacity)
	total := 0
	for _, hit := range hits {
		if !withinRange(hit.Time, options.After, options.Before) {
			continue
		}
		total++
		if options.Limit > 0 && len(results) >= options.Limit {
			continue
		}
		person := byID[hit.PersonID]
		accountProviderName := strings.TrimSpace(hit.AccountProviderName)
		if accountProviderName == "" {
			accountProviderName = personSearchAccountProviderName(person)
		}
		ref := PersonRef(hit.PersonID)
		results = append(results, SearchResult{
			AnchorID:                   hit.AnchorID,
			Ref:                        ref,
			Time:                       hit.Time,
			Who:                        hit.Who,
			AlternativePersonNames:     resolverIdentityAliases(person),
			PersonTechnicalIdentifiers: resolverIdentifiers(person),
			Snippet:                    hit.Snippet,
			PersonID:                   hit.PersonID,
			PhysicalPlaceName:          personSearchPhysicalPlaceName(person),
			AccountProviderName:        accountProviderName,
			Matches:                    hit.Matches,
		})
	}
	return results, total, nil
}

type scoredHit struct {
	AnchorID            string
	PersonID            string
	Who                 string
	AccountProviderName string
	Score               int
	Snippet             string
	Time                time.Time
	Matches             []SearchMatch
}

// NoteAnchorID returns the stable presentation anchor for a contact note.
func NoteAnchorID(noteID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(noteID)))
	return "note-" + hex.EncodeToString(sum[:8])
}

func personSearchAnchor(matches []SearchMatch) string {
	if len(matches) == 0 {
		return openrecord.PersonDisplayNameAnchorID
	}
	return matches[0].Field
}

func personSearchMatchedAccountProviderName(matches []SearchMatch) string {
	for _, match := range matches {
		if match.AccountProviderName != "" {
			return match.AccountProviderName
		}
	}
	return ""
}

func personSearchMatches(person model.Person, query string) []SearchMatch {
	queryTokens := uniqueNormalizedHumanSearchTokens(query)
	if len(queryTokens) == 0 {
		return nil
	}

	queryTokenWasMatched := make([]bool, len(queryTokens))
	type personHumanSearchFieldMatch struct {
		humanSearchFieldValue humanSearchFieldValue
		matchingTextSpans     []humanSearchMatchingTextSpan
	}
	fieldMatches := make([]personHumanSearchFieldMatch, 0)
	for _, fieldValue := range personHumanSearchFieldValues(person) {
		fieldTokens := normalizedHumanSearchTokens(fieldValue.humanSearchFieldText)
		matchingTextSpans := make([]humanSearchMatchingTextSpan, 0)
		for queryTokenIndex, queryToken := range queryTokens {
			for _, fieldToken := range fieldTokens {
				if strings.HasPrefix(fieldToken.normalizedText, queryToken.normalizedText) {
					queryTokenWasMatched[queryTokenIndex] = true
					matchingTextSpans = append(matchingTextSpans, humanSearchMatchingTextSpan{
						startByte: fieldToken.originalTextStartByte,
						endByte:   fieldToken.originalTextEndByte,
					})
					break
				}
			}
		}
		if len(matchingTextSpans) > 0 {
			fieldMatches = append(fieldMatches, personHumanSearchFieldMatch{
				humanSearchFieldValue: fieldValue,
				matchingTextSpans:     matchingTextSpans,
			})
		}
	}
	for _, wasMatched := range queryTokenWasMatched {
		if !wasMatched {
			return nil
		}
	}

	matches := make([]SearchMatch, 0, len(fieldMatches))
	for _, fieldMatch := range fieldMatches {
		matches = append(matches, SearchMatch{
			Field:               string(fieldMatch.humanSearchFieldValue.humanSearchFieldKind),
			AccountProviderName: fieldMatch.humanSearchFieldValue.accountProviderName,
			Runs: humanSearchTextRuns(
				fieldMatch.humanSearchFieldValue.humanSearchFieldText,
				fieldMatch.matchingTextSpans,
			),
		})
	}
	return matches
}

type normalizedHumanSearchToken struct {
	normalizedText        string
	originalTextStartByte int
	originalTextEndByte   int
}

type humanSearchMatchingTextSpan struct {
	startByte int
	endByte   int
}

func uniqueNormalizedHumanSearchTokens(text string) []normalizedHumanSearchToken {
	tokens := normalizedHumanSearchTokens(text)
	uniqueTokens := make([]normalizedHumanSearchToken, 0, len(tokens))
	seenNormalizedText := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, exists := seenNormalizedText[token.normalizedText]; exists {
			continue
		}
		seenNormalizedText[token.normalizedText] = struct{}{}
		uniqueTokens = append(uniqueTokens, token)
	}
	return uniqueTokens
}

func normalizedHumanSearchTokens(text string) []normalizedHumanSearchToken {
	tokens := make([]normalizedHumanSearchToken, 0)
	var normalizedText strings.Builder
	currentTokenStartByte := -1
	currentTokenEndByte := -1
	appendCurrentToken := func() {
		if normalizedText.Len() == 0 {
			return
		}
		tokens = append(tokens, normalizedHumanSearchToken{
			normalizedText:        normalizedText.String(),
			originalTextStartByte: currentTokenStartByte,
			originalTextEndByte:   currentTokenEndByte,
		})
		normalizedText.Reset()
		currentTokenStartByte = -1
		currentTokenEndByte = -1
	}

	for originalTextByte, originalRune := range text {
		originalRuneEndByte := originalTextByte + utf8.RuneLen(originalRune)
		if unicode.IsMark(originalRune) {
			if currentTokenStartByte >= 0 {
				currentTokenEndByte = originalRuneEndByte
			}
			continue
		}
		normalizedRuneContainsLetterOrDigit := false
		for _, decomposedRune := range norm.NFD.String(string(originalRune)) {
			if unicode.IsMark(decomposedRune) {
				continue
			}
			if unicode.IsLetter(decomposedRune) || unicode.IsDigit(decomposedRune) {
				normalizedRuneContainsLetterOrDigit = true
				normalizedText.WriteRune(unicode.ToLower(decomposedRune))
			}
		}
		if normalizedRuneContainsLetterOrDigit {
			if currentTokenStartByte < 0 {
				currentTokenStartByte = originalTextByte
			}
			currentTokenEndByte = originalRuneEndByte
			continue
		}
		appendCurrentToken()
	}
	appendCurrentToken()
	return tokens
}

func humanSearchTextRuns(text string, matchingTextSpans []humanSearchMatchingTextSpan) []SearchTextRun {
	if len(matchingTextSpans) == 0 {
		return nil
	}
	sort.Slice(matchingTextSpans, func(i, j int) bool {
		return matchingTextSpans[i].startByte < matchingTextSpans[j].startByte
	})
	mergedSpans := matchingTextSpans[:1]
	for _, currentSpan := range matchingTextSpans[1:] {
		lastSpan := &mergedSpans[len(mergedSpans)-1]
		if currentSpan.startByte <= lastSpan.endByte {
			if currentSpan.endByte > lastSpan.endByte {
				lastSpan.endByte = currentSpan.endByte
			}
			continue
		}
		mergedSpans = append(mergedSpans, currentSpan)
	}

	runs := make([]SearchTextRun, 0, len(mergedSpans)*2+1)
	currentByte := 0
	for _, matchingSpan := range mergedSpans {
		if currentByte < matchingSpan.startByte {
			runs = append(runs, SearchTextRun{Text: text[currentByte:matchingSpan.startByte]})
		}
		runs = append(runs, SearchTextRun{Text: text[matchingSpan.startByte:matchingSpan.endByte], Matched: true})
		currentByte = matchingSpan.endByte
	}
	if currentByte < len(text) {
		runs = append(runs, SearchTextRun{Text: text[currentByte:]})
	}
	return runs
}

type humanSearchFieldValue struct {
	humanSearchFieldKind humanSearchFieldKind
	humanSearchFieldText string
	accountProviderName  string
}

type humanSearchFieldKind string

const (
	personDisplayNameHumanSearchFieldKind                      = humanSearchFieldKind(openrecord.PersonDisplayNameAnchorID)
	personSortNameHumanSearchFieldKind                         = humanSearchFieldKind("sort_name")
	personAlternativeNameHumanSearchFieldKind                  = humanSearchFieldKind(openrecord.PersonAlternativeDisplayNameAnchorID)
	personTagHumanSearchFieldKind                              = humanSearchFieldKind("tag")
	personEmailAddressHumanSearchFieldKind                     = humanSearchFieldKind(openrecord.PersonEmailAddressAnchorID)
	personPhoneNumberHumanSearchFieldKind                      = humanSearchFieldKind(openrecord.PersonPhoneNumberAnchorID)
	personPostalAddressHumanSearchFieldKind                    = humanSearchFieldKind(openrecord.PersonPostalAddressAnchorID)
	personAccountIdentifierHumanSearchFieldKind                = humanSearchFieldKind(openrecord.PersonAccountIdentifierAnchorID)
	personRelationshipOrContextDescriptionHumanSearchFieldKind = humanSearchFieldKind(openrecord.PersonRelationshipOrContextDescriptionAnchorID)
	personBodyHumanSearchFieldKind                             = humanSearchFieldKind("body")
	contactNoteKindHumanSearchFieldKind                        = humanSearchFieldKind("note_kind")
	contactNoteSourceHumanSearchFieldKind                      = humanSearchFieldKind("note_source")
	contactNoteBodyHumanSearchFieldKind                        = humanSearchFieldKind("note_body")
	contactNoteTopicHumanSearchFieldKind                       = humanSearchFieldKind("note_topic")
)

func personHumanSearchFieldValues(person model.Person) []humanSearchFieldValue {
	values := []humanSearchFieldValue{}
	identifierValuesNotSuitableAsPersonDisplayNames := model.PersonIdentifierValuesNotSuitableAsPersonDisplayNames(person)
	appendPersonNameIfSuitableForHumanPresentation := func(fieldKind humanSearchFieldKind, name string) {
		if model.PersonDisplayNameIsSuitableForHumanPresentation(name, identifierValuesNotSuitableAsPersonDisplayNames) {
			values = append(values, humanSearchFieldValue{humanSearchFieldKind: fieldKind, humanSearchFieldText: name})
		}
	}
	appendPersonNameIfSuitableForHumanPresentation(personDisplayNameHumanSearchFieldKind, person.Name)
	appendPersonNameIfSuitableForHumanPresentation(personSortNameHumanSearchFieldKind, person.SortName)
	values = append(values,
		humanSearchFieldValue{
			humanSearchFieldKind: personRelationshipOrContextDescriptionHumanSearchFieldKind,
			humanSearchFieldText: string(person.PersonRelationshipOrContextDescription),
		},
		humanSearchFieldValue{humanSearchFieldKind: personBodyHumanSearchFieldKind, humanSearchFieldText: person.Body},
	)
	for _, value := range person.AKA {
		appendPersonNameIfSuitableForHumanPresentation(personAlternativeNameHumanSearchFieldKind, value)
	}
	for _, value := range person.Tags {
		values = append(values, humanSearchFieldValue{humanSearchFieldKind: personTagHumanSearchFieldKind, humanSearchFieldText: value})
	}
	for _, value := range person.Emails {
		values = append(values, humanSearchFieldValue{humanSearchFieldKind: personEmailAddressHumanSearchFieldKind, humanSearchFieldText: value.Value})
	}
	for _, value := range person.Phones {
		values = append(values, humanSearchFieldValue{humanSearchFieldKind: personPhoneNumberHumanSearchFieldKind, humanSearchFieldText: value.Value})
	}
	for _, value := range person.Addresses {
		values = append(values, humanSearchFieldValue{humanSearchFieldKind: personPostalAddressHumanSearchFieldKind, humanSearchFieldText: value.Value})
	}
	accountProviderNames := make([]string, 0, len(person.Accounts))
	for service := range person.Accounts {
		accountProviderNames = append(accountProviderNames, service)
	}
	sort.Strings(accountProviderNames)
	for _, service := range accountProviderNames {
		identifiers := person.Accounts[service]
		for _, identifier := range identifiers {
			humanAccountIdentifier := model.AccountIdentifierForHumanPresentation(
				service,
				identifier,
			)
			if humanAccountIdentifier == "" {
				continue
			}
			values = append(values, humanSearchFieldValue{
				humanSearchFieldKind: personAccountIdentifierHumanSearchFieldKind,
				humanSearchFieldText: humanAccountIdentifier,
				accountProviderName:  service,
			})
		}
	}
	sourceNames := make([]string, 0, len(person.Sources))
	for sourceName := range person.Sources {
		sourceNames = append(sourceNames, sourceName)
	}
	sort.Strings(sourceNames)
	for _, sourceName := range sourceNames {
		source := person.Sources[sourceName]
		for _, name := range source.Names {
			appendPersonNameIfSuitableForHumanPresentation(personAlternativeNameHumanSearchFieldKind, name)
		}
	}
	return values
}

func noteSearchMatches(note model.Note, query string) []SearchMatch {
	values := []humanSearchFieldValue{
		{humanSearchFieldKind: contactNoteKindHumanSearchFieldKind, humanSearchFieldText: note.Kind},
		{humanSearchFieldKind: contactNoteSourceHumanSearchFieldKind, humanSearchFieldText: note.Source},
		{humanSearchFieldKind: contactNoteBodyHumanSearchFieldKind, humanSearchFieldText: note.Body},
	}
	for _, topic := range note.Topics {
		values = append(values, humanSearchFieldValue{humanSearchFieldKind: contactNoteTopicHumanSearchFieldKind, humanSearchFieldText: topic})
	}
	return collectSearchMatches(values, query)
}

func collectSearchMatches(values []humanSearchFieldValue, query string) []SearchMatch {
	matches := make([]SearchMatch, 0, len(values))
	for _, value := range values {
		if runs := searchTextRuns(value.humanSearchFieldText, query); len(runs) > 0 {
			matches = append(matches, SearchMatch{Field: string(value.humanSearchFieldKind), Runs: runs})
		}
	}
	return matches
}

func searchTextRuns(value, query string) []SearchTextRun {
	patterns := []string{strings.TrimSpace(query)}
	if !strings.Contains(strings.ToLower(value), strings.ToLower(patterns[0])) {
		patterns = strings.Fields(query)
	}
	type span struct{ start, end int }
	var spans []span
	for _, pattern := range patterns {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(pattern))
		for _, match := range re.FindAllStringIndex(value, -1) {
			spans = append(spans, span{start: match[0], end: match[1]})
		}
	}
	if len(spans) == 0 {
		return nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := spans[:1]
	for _, current := range spans[1:] {
		last := &merged[len(merged)-1]
		if current.start <= last.end {
			if current.end > last.end {
				last.end = current.end
			}
			continue
		}
		merged = append(merged, current)
	}
	runs := make([]SearchTextRun, 0, len(merged)*2+1)
	position := 0
	for _, match := range merged {
		if position < match.start {
			runs = append(runs, SearchTextRun{Text: value[position:match.start]})
		}
		runs = append(runs, SearchTextRun{Text: value[match.start:match.end], Matched: true})
		position = match.end
	}
	if position < len(value) {
		runs = append(runs, SearchTextRun{Text: value[position:]})
	}
	return runs
}

func noteSnippet(note model.Note, query string) string {
	return firstSearchMatchText(noteSearchMatches(note, query), note.Body)
}

func withinRange(t, after, before time.Time) bool {
	if t.IsZero() {
		return after.IsZero() && before.IsZero()
	}
	if !after.IsZero() && t.Before(after) {
		return false
	}
	return before.IsZero() || t.Before(before)
}

func firstSearchMatchText(matches []SearchMatch, fallbackText string) string {
	for _, match := range matches {
		var text strings.Builder
		for _, run := range match.Runs {
			text.WriteString(run.Text)
		}
		if matchedFieldText := strings.TrimSpace(text.String()); matchedFieldText != "" {
			return matchedFieldText
		}
	}
	return fallbackText
}

func personSearchPhysicalPlaceName(person model.Person) string {
	for _, address := range person.Addresses {
		physicalPlaceName := strings.Join(strings.Fields(strings.ReplaceAll(address.Value, "\n", ", ")), " ")
		if physicalPlaceName != "" {
			return physicalPlaceName
		}
	}
	return ""
}

func personSearchAccountProviderName(person model.Person) string {
	accountProviderNames := make(map[string]struct{}, len(person.Accounts)+len(person.Sources)+2)
	for accountProviderName := range person.Accounts {
		accountProviderNames[strings.TrimSpace(accountProviderName)] = struct{}{}
	}
	for accountProviderName := range person.Sources {
		accountProviderNames[strings.TrimSpace(accountProviderName)] = struct{}{}
	}
	if strings.TrimSpace(person.Apple.ID) != "" {
		accountProviderNames["apple"] = struct{}{}
	}
	if strings.TrimSpace(person.Google.ID) != "" {
		accountProviderNames["google"] = struct{}{}
	}
	delete(accountProviderNames, "")
	namesInAlphabeticalOrder := make([]string, 0, len(accountProviderNames))
	for accountProviderName := range accountProviderNames {
		namesInAlphabeticalOrder = append(namesInAlphabeticalOrder, accountProviderName)
	}
	sort.Strings(namesInAlphabeticalOrder)
	if len(namesInAlphabeticalOrder) == 0 {
		return ""
	}
	return namesInAlphabeticalOrder[0]
}

func scoreText(text, query string) int {
	if text == query {
		return 100
	}
	return strings.Count(text, query)
}
