package main

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/alecthomas/kong"
	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
)

const (
	internalCandidateRecordsPerLane = 100
)

var (
	earliestSearchFilterTimeRepresentableAsUnixNanoseconds = time.Unix(0, -1<<63)
	latestSearchFilterTimeRepresentableAsUnixNanoseconds   = time.Unix(0, 1<<63-1)
)

type searchRecordCandidate struct {
	canonicalArchiveRecordReference     canonicalArchiveRecordReference
	canonicalSearchResultGroupReference canonicalArchiveRecordReference
	registeredTrawler                   registeredTrawlerName
	localTrawlerShortReference          localTrawlerShortReference
	archiveRecordKindDisplayName        string
	archiveRecordAssociatedNanoseconds  sql.NullInt64
	searchMatchPresentationProtobuf     []byte
	searchableRecordTextSectionName     string
	openedRecordAnchorIdentifier        string
	sectionStartUTF8ByteOffset          int64
	sectionEndUTF8ByteOffset            int64
	searchablePassageContent            string
}

type denseRecordCandidate struct {
	searchRecordCandidate       searchRecordCandidate
	searchablePassageIdentifier int64
	cosineDistance              float64
}

type worstDenseRecordCandidateHeap []denseRecordCandidate

func (candidates worstDenseRecordCandidateHeap) Len() int { return len(candidates) }
func (candidates worstDenseRecordCandidateHeap) Less(left, right int) bool {
	if candidates[left].cosineDistance != candidates[right].cosineDistance {
		return candidates[left].cosineDistance > candidates[right].cosineDistance
	}
	return candidates[left].searchablePassageIdentifier > candidates[right].searchablePassageIdentifier
}
func (candidates worstDenseRecordCandidateHeap) Swap(left, right int) {
	candidates[left], candidates[right] = candidates[right], candidates[left]
}
func (candidates *worstDenseRecordCandidateHeap) Push(candidate any) {
	*candidates = append(*candidates, candidate.(denseRecordCandidate))
}
func (candidates *worstDenseRecordCandidateHeap) Pop() any {
	previous := *candidates
	lastIndex := len(previous) - 1
	candidate := previous[lastIndex]
	*candidates = previous[:lastIndex]
	return candidate
}

type searchRecordFilters struct {
	selectedTrawler registeredTrawlerName
	after           time.Time
	before          time.Time
}

type searchCorpusScope struct {
	selectedTrawler          registeredTrawlerName
	maximumRecordsPerTrawler uint64
}

type naturalLanguageSearchCommand struct {
	Query   []string `arg:"" optional:"" name:"words" help:"Natural-language evidence to find"`
	Trawler string   `name:"trawler" help:"Search one OpenTrawl source"`
	Limit   int      `name:"limit" default:"10" placeholder:"COUNT" help:"Maximum records to show"`
	After   string   `name:"after" help:"Results on or after this date"`
	Before  string   `name:"before" help:"Results on or before this date or time"`
}

func runNaturalLanguageSearch(arguments []string, output io.Writer) (returnedError error) {
	var command naturalLanguageSearchCommand
	helpWasShown := false
	parser, err := kong.New(
		&command,
		kong.Name("trawl search"),
		kong.Description("Search OpenTrawl archives with natural language"),
		kong.Writers(output, output),
		kong.Exit(func(int) { helpWasShown = true }),
	)
	if err != nil {
		return err
	}
	if _, err := parser.Parse(arguments); err != nil {
		return err
	}
	if helpWasShown {
		return nil
	}
	query := strings.TrimSpace(strings.Join(command.Query, " "))
	if query == "" || command.Limit <= 0 {
		return errors.New("usage: trawl search [--trawler SOURCE] [--after TIME] [--before TIME] [--limit COUNT] natural language query")
	}
	searchFilters, err := parseSearchRecordFilters(command.Trawler, command.After, command.Before)
	if err != nil {
		return err
	}
	corpusPath := strings.TrimSpace(os.Getenv("TRAWL_SEARCH_RESEARCH_CORPUS"))
	indexPath := strings.TrimSpace(os.Getenv("TRAWL_SEARCH_RESEARCH_INDEX"))
	runtimeProcessIdentifierText := strings.TrimSpace(os.Getenv("TRAWL_SEARCH_RESEARCH_RUNTIME_PROCESS_ID"))
	if corpusPath == "" || indexPath == "" || runtimeProcessIdentifierText == "" {
		return errors.New("TRAWL_SEARCH_RESEARCH_CORPUS, TRAWL_SEARCH_RESEARCH_INDEX and TRAWL_SEARCH_RESEARCH_RUNTIME_PROCESS_ID are required")
	}
	runtimeProcessIdentifier, err := strconv.Atoi(runtimeProcessIdentifierText)
	if err != nil || runtimeProcessIdentifier <= 0 {
		return errors.New("TRAWL_SEARCH_RESEARCH_RUNTIME_PROCESS_ID must identify the isolated embedding runtime")
	}
	corpusDatabase, err := sql.Open("sqlite3", "file:"+corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, corpusDatabase.Close()) }()
	corpusDatabase.SetMaxOpenConns(1)
	if _, err := readSearchResearchSourceSnapshot(corpusDatabase); err != nil {
		return err
	}
	indexDatabase, err := sql.Open("sqlite3", indexPath)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, indexDatabase.Close()) }()
	indexDatabase.SetMaxOpenConns(1)
	configuration, indexedCorpusSHA256, err := readEmbeddingDeploymentConfiguration(indexDatabase)
	if err != nil {
		return err
	}
	if err := verifyOllamaEmbeddingDeployment(context.Background(), configuration); err != nil {
		return err
	}
	if err := confirmCompleteMatchingEmbeddingIndex(corpusDatabase, indexDatabase, indexedCorpusSHA256); err != nil {
		return err
	}
	corpusScope, err := readSearchCorpusScope(corpusDatabase)
	if err != nil {
		return err
	}
	runIdentifier, measurement, err := startExperimentRun(indexDatabase, "natural_language_search", configuration.modelArtifactBytes, runtimeProcessIdentifier)
	if err != nil {
		return err
	}
	memorySampler := startProcessMemorySampler(runtimeProcessIdentifier)
	defer func() {
		memoryMeasurement := memorySampler.finish()
		measurement.baselineHarnessProcessMemoryBytes = memoryMeasurement.baselineHarnessProcessMemoryBytes
		measurement.baselineRuntimeProcessMemoryBytes = memoryMeasurement.baselineRuntimeProcessMemoryBytes
		measurement.peakProcessMemoryBytes = memoryMeasurement.peakHarnessAndRuntimeMemoryBytes
		measurement.steadyProcessMemoryBytes = memoryMeasurement.steadyHarnessAndRuntimeMemoryBytes
		measurement.indexBytes = sqliteDatabaseFileSetSize(indexPath)
		if returnedError != nil {
			measurement.failureMessage = returnedError.Error()
			if measurement.failureStage == "" {
				measurement.failureStage = candidateGenerationFailure
			}
		}
		returnedError = errors.Join(returnedError, finishExperimentRun(indexDatabase, runIdentifier, measurement))
	}()
	embeddingResponse, inferenceElapsed, err := requestOllamaEmbeddings(
		context.Background(), configuration, []string{configuration.queryInputPrefix + query},
	)
	measurement.inferenceElapsed = inferenceElapsed
	measurement.promptTokens = embeddingResponse.PromptTokenCount
	if err != nil {
		measurement.failureStage = embeddingRequestFailure
		return err
	}
	if len(embeddingResponse.Embeddings) != 1 {
		measurement.failureStage = embeddingRequestFailure
		return fmt.Errorf("runtime returned %d query embeddings", len(embeddingResponse.Embeddings))
	}
	if err := validateEmbedding(embeddingResponse.Embeddings[0], configuration.embeddingDimensions); err != nil {
		measurement.failureStage = embeddingRequestFailure
		return err
	}
	candidateGenerationStartedAt := time.Now()
	lexicalCandidates, lexicalLaneExhausted, err := generateLexicalSearchRecordCandidates(
		corpusDatabase, query, searchFilters, internalCandidateRecordsPerLane,
	)
	if err != nil {
		measurement.failureStage = candidateGenerationFailure
		return err
	}
	denseCandidates, denseLaneExhausted, err := generateDenseSearchRecordCandidates(
		corpusDatabase, indexPath, embeddingResponse.Embeddings[0], searchFilters,
		internalCandidateRecordsPerLane,
	)
	measurement.candidateGenerationElapsed = time.Since(candidateGenerationStartedAt)
	if err != nil {
		measurement.failureStage = candidateGenerationFailure
		return err
	}
	orderingStartedAt := time.Now()
	orderedResults := alternateCandidateLaneCoverage(lexicalCandidates, denseCandidates, command.Limit)
	measurement.orderingElapsed = time.Since(orderingStartedAt)
	return writeNaturalLanguageSearchResults(
		output, query, corpusScope, orderedResults,
		lexicalLaneExhausted && denseLaneExhausted,
		searchFilters.selectedTrawler != "",
	)
}

func readSearchCorpusScope(database *sql.DB) (searchCorpusScope, error) {
	var scope searchCorpusScope
	err := database.QueryRow(`
		select selected_registered_trawler, maximum_records_per_trawler
		from corpus_projection_scope
		where singleton = 1`).Scan(&scope.selectedTrawler, &scope.maximumRecordsPerTrawler)
	return scope, err
}

func parseSearchRecordFilters(selectedTrawlerText, afterText, beforeText string) (searchRecordFilters, error) {
	filters := searchRecordFilters{selectedTrawler: registeredTrawlerName(strings.TrimSpace(selectedTrawlerText))}
	if filters.selectedTrawler != "" && !isSearchResearchTrawler(filters.selectedTrawler) {
		return searchRecordFilters{}, fmt.Errorf("unknown research trawler %q", filters.selectedTrawler)
	}
	var err error
	if strings.TrimSpace(afterText) != "" {
		filters.after, err = ckflags.Date(afterText)
		if err != nil {
			return searchRecordFilters{}, fmt.Errorf("--after %w", err)
		}
		if err := validateSearchFilterTimeRepresentableAsUnixNanoseconds("--after", filters.after); err != nil {
			return searchRecordFilters{}, err
		}
	}
	if strings.TrimSpace(beforeText) != "" {
		filters.before, err = ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(beforeText)
		if err != nil {
			return searchRecordFilters{}, fmt.Errorf("--before %w", err)
		}
		if err := validateSearchFilterTimeRepresentableAsUnixNanoseconds("--before", filters.before); err != nil {
			return searchRecordFilters{}, err
		}
	}
	if !filters.after.IsZero() && !filters.before.IsZero() && filters.after.After(filters.before) {
		return searchRecordFilters{}, errors.New("--after must not be later than --before")
	}
	return filters, nil
}

func validateSearchFilterTimeRepresentableAsUnixNanoseconds(flagName string, filterTime time.Time) error {
	if filterTime.Before(earliestSearchFilterTimeRepresentableAsUnixNanoseconds) ||
		filterTime.After(latestSearchFilterTimeRepresentableAsUnixNanoseconds) {
		return fmt.Errorf(
			"%s must be between 1677-09-21T00:12:43.145224192Z and 2262-04-11T23:47:16.854775807Z",
			flagName,
		)
	}
	return nil
}

func confirmCompleteMatchingEmbeddingIndex(
	corpusDatabase *sql.DB,
	indexDatabase *sql.DB,
	indexedCorpusSHA256 corpusContentSHA256,
) error {
	var corpusSHA256 string
	if err := corpusDatabase.QueryRow(`select passage_corpus_sha256 from corpus_metadata where singleton = 1`).Scan(&corpusSHA256); err != nil {
		return err
	}
	if corpusSHA256 == "" || corpusContentSHA256(corpusSHA256) != indexedCorpusSHA256 {
		return errors.New("search corpus and embedding index do not match")
	}
	var corpusPassageCount, indexedPassageCount int64
	if err := indexDatabase.QueryRow(`select corpus_passage_count, indexed_passage_count from embedding_index_checkpoint where singleton = 1`).Scan(&corpusPassageCount, &indexedPassageCount); err != nil {
		return err
	}
	if corpusPassageCount == 0 || indexedPassageCount != corpusPassageCount {
		return errors.New("embedding index is incomplete")
	}
	return nil
}

func generateLexicalSearchRecordCandidates(
	database *sql.DB,
	query string,
	filters searchRecordFilters,
	maximumRecords int,
) ([]searchRecordCandidate, bool, error) {
	matchExpression := tokenOrFTS5MatchExpression(query)
	if matchExpression == "" {
		return nil, true, nil
	}
	statement := `
		select record.canonical_archive_record_reference, record.canonical_search_result_group_reference,
		       record.registered_trawler,
		       record.local_trawler_short_reference, record.archive_record_kind_display_name,
		       record.archive_record_associated_unix_nanoseconds,
		       record.search_match_presentation_protobuf,
		       section.searchable_record_text_section_name, section.opened_record_anchor_identifier,
		       passage.section_start_utf8_byte_offset, passage.section_end_utf8_byte_offset,
		       snippet(searchable_passages_fts, 0, '', '', ' … ', 48)
		from searchable_passages_fts
		join searchable_passages passage on passage.searchable_passage_identifier = searchable_passages_fts.rowid
		join searchable_record_text_sections section using (searchable_record_text_section_identifier)
		join archive_records record using (archive_record_identifier)
		where searchable_passages_fts match ?`
	arguments := []any{matchExpression}
	statement, arguments = appendSearchRecordSQLFilters(statement, arguments, filters)
	statement += ` order by bm25(searchable_passages_fts), passage.searchable_passage_identifier`
	rows, err := database.Query(statement, arguments...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	uniqueRecords := make([]searchRecordCandidate, 0, maximumRecords)
	seenReferences := make(map[canonicalArchiveRecordReference]struct{}, maximumRecords)
	for rows.Next() {
		candidate, err := scanSearchRecordCandidate(rows)
		if err != nil {
			return nil, false, err
		}
		if _, seen := seenReferences[candidate.canonicalSearchResultGroupReference]; seen {
			continue
		}
		seenReferences[candidate.canonicalSearchResultGroupReference] = struct{}{}
		uniqueRecords = append(uniqueRecords, candidate)
		if len(uniqueRecords) == maximumRecords {
			return uniqueRecords, false, nil
		}
	}
	return uniqueRecords, true, rows.Err()
}

func appendSearchRecordSQLFilters(statement string, arguments []any, filters searchRecordFilters) (string, []any) {
	if filters.selectedTrawler != "" {
		statement += ` and record.registered_trawler = ?`
		arguments = append(arguments, filters.selectedTrawler)
	}
	if !filters.after.IsZero() {
		statement += ` and record.archive_record_associated_unix_nanoseconds >= ?`
		arguments = append(arguments, filters.after.UnixNano())
	}
	if !filters.before.IsZero() {
		statement += ` and record.archive_record_associated_unix_nanoseconds <= ?`
		arguments = append(arguments, filters.before.UnixNano())
	}
	return statement, arguments
}

func tokenOrFTS5MatchExpression(query string) string {
	tokens := strings.FieldsFunc(query, func(value rune) bool {
		return !unicode.IsLetter(value) && !unicode.IsNumber(value)
	})
	quotedTokens := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if token = strings.TrimSpace(token); token != "" {
			quotedTokens = append(quotedTokens, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
		}
	}
	return strings.Join(quotedTokens, " OR ")
}

func generateDenseSearchRecordCandidates(
	corpusDatabase *sql.DB,
	indexPath string,
	queryEmbedding []float32,
	filters searchRecordFilters,
	maximumRecords int,
) ([]searchRecordCandidate, bool, error) {
	indexAttachmentURI := (&url.URL{
		Scheme:   "file",
		Path:     indexPath,
		RawQuery: "mode=ro&immutable=1",
	}).String()
	if _, err := corpusDatabase.Exec(`attach database ? as embedding_index`, indexAttachmentURI); err != nil {
		return nil, false, err
	}
	defer func() { _, _ = corpusDatabase.Exec(`detach database embedding_index`) }()
	statement := `
		select record.canonical_archive_record_reference, record.canonical_search_result_group_reference,
		       record.registered_trawler,
		       record.local_trawler_short_reference, record.archive_record_kind_display_name,
		       record.archive_record_associated_unix_nanoseconds,
		       record.search_match_presentation_protobuf,
		       section.searchable_record_text_section_name, section.opened_record_anchor_identifier,
		       passage.section_start_utf8_byte_offset, passage.section_end_utf8_byte_offset,
		       passage.searchable_passage_content, passage.searchable_passage_identifier,
		       embedding.embedding_float32_blob
		from searchable_passages passage
		join searchable_record_text_sections section using (searchable_record_text_section_identifier)
		join archive_records record using (archive_record_identifier)
		join embedding_index.searchable_passage_embeddings embedding using (searchable_passage_identifier)
		where 1 = 1`
	statement, arguments := appendSearchRecordSQLFilters(statement, nil, filters)
	statement += ` order by record.canonical_search_result_group_reference, passage.searchable_passage_identifier`
	rows, err := corpusDatabase.Query(statement, arguments...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	topRecordCandidates := make(worstDenseRecordCandidateHeap, 0, maximumRecords)
	var currentGroupReference canonicalArchiveRecordReference
	var bestCandidateForCurrentGroup denseRecordCandidate
	currentGroupHasCandidate := false
	eligibleResultGroupCount := 0
	retainBestCandidate := func() {
		if !currentGroupHasCandidate {
			return
		}
		eligibleResultGroupCount++
		if len(topRecordCandidates) < maximumRecords {
			heap.Push(&topRecordCandidates, bestCandidateForCurrentGroup)
			return
		}
		if denseRecordCandidateIsBetter(bestCandidateForCurrentGroup, topRecordCandidates[0]) {
			topRecordCandidates[0] = bestCandidateForCurrentGroup
			heap.Fix(&topRecordCandidates, 0)
		}
	}
	for rows.Next() {
		var candidate searchRecordCandidate
		var passageIdentifier int64
		var embeddingBlob []byte
		if err := rows.Scan(
			&candidate.canonicalArchiveRecordReference, &candidate.canonicalSearchResultGroupReference,
			&candidate.registeredTrawler, &candidate.localTrawlerShortReference,
			&candidate.archiveRecordKindDisplayName, &candidate.archiveRecordAssociatedNanoseconds,
			&candidate.searchMatchPresentationProtobuf,
			&candidate.searchableRecordTextSectionName, &candidate.openedRecordAnchorIdentifier,
			&candidate.sectionStartUTF8ByteOffset, &candidate.sectionEndUTF8ByteOffset,
			&candidate.searchablePassageContent, &passageIdentifier, &embeddingBlob,
		); err != nil {
			return nil, false, err
		}
		passageEmbedding, err := decodeFloat32Embedding(embeddingBlob, len(queryEmbedding))
		if err != nil {
			return nil, false, err
		}
		passageCandidate := denseRecordCandidate{
			searchRecordCandidate:       candidate,
			searchablePassageIdentifier: passageIdentifier,
			cosineDistance:              cosineDistance(queryEmbedding, passageEmbedding),
		}
		if currentGroupHasCandidate && candidate.canonicalSearchResultGroupReference != currentGroupReference {
			retainBestCandidate()
			currentGroupHasCandidate = false
		}
		currentGroupReference = candidate.canonicalSearchResultGroupReference
		if !currentGroupHasCandidate || denseRecordCandidateIsBetter(passageCandidate, bestCandidateForCurrentGroup) {
			bestCandidateForCurrentGroup = passageCandidate
			currentGroupHasCandidate = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	retainBestCandidate()
	sort.Slice(topRecordCandidates, func(left, right int) bool {
		return denseRecordCandidateIsBetter(topRecordCandidates[left], topRecordCandidates[right])
	})
	orderedCandidates := make([]searchRecordCandidate, len(topRecordCandidates))
	for candidateIndex, candidate := range topRecordCandidates {
		orderedCandidates[candidateIndex] = candidate.searchRecordCandidate
	}
	return orderedCandidates, eligibleResultGroupCount < maximumRecords, nil
}

func denseRecordCandidateIsBetter(left, right denseRecordCandidate) bool {
	if left.cosineDistance != right.cosineDistance {
		return left.cosineDistance < right.cosineDistance
	}
	return left.searchablePassageIdentifier < right.searchablePassageIdentifier
}

func decodeFloat32Embedding(encoded []byte, expectedDimensions int) ([]float32, error) {
	if len(encoded) != expectedDimensions*4 {
		return nil, fmt.Errorf("stored embedding has %d bytes; expected %d", len(encoded), expectedDimensions*4)
	}
	embedding := make([]float32, expectedDimensions)
	for dimensionIndex := range embedding {
		encodedComponent := binary.LittleEndian.Uint32(encoded[dimensionIndex*4:])
		embedding[dimensionIndex] = math.Float32frombits(encodedComponent)
	}
	return embedding, nil
}

func cosineDistance(left, right []float32) float64 {
	var dotProduct, leftSquaredNorm, rightSquaredNorm float64
	for dimensionIndex, leftComponent := range left {
		rightComponent := right[dimensionIndex]
		dotProduct += float64(leftComponent) * float64(rightComponent)
		leftSquaredNorm += float64(leftComponent) * float64(leftComponent)
		rightSquaredNorm += float64(rightComponent) * float64(rightComponent)
	}
	return 1 - dotProduct/(math.Sqrt(leftSquaredNorm)*math.Sqrt(rightSquaredNorm))
}

type searchRecordCandidateScanner interface {
	Scan(destinations ...any) error
}

func scanSearchRecordCandidate(scanner searchRecordCandidateScanner) (searchRecordCandidate, error) {
	var candidate searchRecordCandidate
	err := scanner.Scan(
		&candidate.canonicalArchiveRecordReference, &candidate.canonicalSearchResultGroupReference,
		&candidate.registeredTrawler,
		&candidate.localTrawlerShortReference, &candidate.archiveRecordKindDisplayName,
		&candidate.archiveRecordAssociatedNanoseconds, &candidate.searchMatchPresentationProtobuf,
		&candidate.searchableRecordTextSectionName,
		&candidate.openedRecordAnchorIdentifier, &candidate.sectionStartUTF8ByteOffset,
		&candidate.sectionEndUTF8ByteOffset, &candidate.searchablePassageContent,
	)
	return candidate, err
}

func alternateCandidateLaneCoverage(
	lexicalCandidates []searchRecordCandidate,
	denseCandidates []searchRecordCandidate,
	maximumResults int,
) []searchRecordCandidate {
	orderedResults := make([]searchRecordCandidate, 0, maximumResults)
	seenReferences := make(map[canonicalArchiveRecordReference]struct{}, maximumResults)
	for candidateIndex := 0; candidateIndex < max(len(lexicalCandidates), len(denseCandidates)); candidateIndex++ {
		for _, lane := range [][]searchRecordCandidate{lexicalCandidates, denseCandidates} {
			if candidateIndex >= len(lane) {
				continue
			}
			candidate := lane[candidateIndex]
			if _, seen := seenReferences[candidate.canonicalSearchResultGroupReference]; seen {
				continue
			}
			seenReferences[candidate.canonicalSearchResultGroupReference] = struct{}{}
			orderedResults = append(orderedResults, candidate)
			if len(orderedResults) == maximumResults {
				return orderedResults
			}
		}
	}
	return orderedResults
}

func writeNaturalLanguageSearchResults(
	output io.Writer,
	query string,
	corpusScope searchCorpusScope,
	results []searchRecordCandidate,
	allCandidateLanesExhausted bool,
	searchWasExplicitlyScopedToOneTrawler bool,
) error {
	if _, err := fmt.Fprintln(output, corpusScope.description()); err != nil {
		return err
	}
	presentations := make([]render.SearchResultPresentationForRootTrawlHumanOutput, 0, len(results))
	for _, result := range results {
		searchMatchPresentation := &search.SearchMatchPresentation{}
		if err := proto.Unmarshal(result.searchMatchPresentationProtobuf, searchMatchPresentation); err != nil {
			return err
		}
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = searchMatchTextFieldFromCandidate(
			result.searchableRecordTextSectionName,
			result.searchablePassageContent,
		)
		link, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity(string(result.registeredTrawler)),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(string(result.localTrawlerShortReference)),
		})
		if err != nil {
			return err
		}
		var textPassage *identity.ArchiveRecordTextPassage
		if result.openedRecordAnchorIdentifier != "" {
			textPassage = &identity.ArchiveRecordTextPassage{
				RecordAnchor:                      trawlkit.NewRecordAnchorIdentifier(result.openedRecordAnchorIdentifier),
				SectionStartUtf8ByteOffset:        uint64(result.sectionStartUTF8ByteOffset),
				SectionEndUtf8ByteOffsetExclusive: uint64(result.sectionEndUTF8ByteOffset),
			}
		}
		presentations = append(presentations, render.SearchResultPresentationForRootTrawlHumanOutput{
			SearchMatchPresentation:   searchMatchPresentation,
			GloballyRoutableTrawlLink: link,
			ArchiveRecordTextPassage:  textPassage,
		})
	}
	hints := []string(nil)
	if allCandidateLanesExhausted {
		hints = append(hints, "Search exhausted the available candidates after filters.")
	}
	return render.WriteSearchResults(output, render.SearchResults{
		Heading:                               render.SearchResultsHeading(query, "", len(presentations), len(presentations)),
		Hints:                                 hints,
		Presentations:                         presentations,
		Empty:                                 render.SearchResultsEmptySentence(query),
		SearchWasExplicitlyScopedToOneTrawler: searchWasExplicitlyScopedToOneTrawler,
	})
}

func (scope searchCorpusScope) description() string {
	sourceDescription := "all enabled research sources"
	if scope.selectedTrawler != "" {
		sourceDescription = string(scope.selectedTrawler) + " only"
	}
	if scope.maximumRecordsPerTrawler == 0 {
		return "Corpus scope: " + sourceDescription + ", complete export."
	}
	return fmt.Sprintf(
		"Corpus scope: %s, at most %d records per source.",
		sourceDescription,
		scope.maximumRecordsPerTrawler,
	)
}
