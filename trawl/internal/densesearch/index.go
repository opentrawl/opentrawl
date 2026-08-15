package densesearch

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	searchablerecord "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/searchable_record"
	"google.golang.org/protobuf/proto"
)

const (
	indexDirectoryName                  = "search"
	indexFileName                       = "dense.sqlite"
	searchableRecordExportPageSize      = 1000
	maximumEmbeddingRequestPassageCount = 32
	maximumDenseCandidateRecordCount    = 100
)

type Availability int

const (
	AvailabilityAvailable Availability = iota
	AvailabilityIndexNotBuilt
	AvailabilityIndexBuilding
	AvailabilityModelUnavailable
	AvailabilityIndexIncompatible
)

type SearchableRecordSource struct {
	RegisteredTrawlerIdentity    *identity.RegisteredTrawlerIdentity
	RegisteredTrawlerDisplayName string
	ExportPage                   func(
		context.Context,
		*searchablerecord.SearchableRecordExportRequest,
	) (*searchablerecord.TrawlerSearchableRecordExportPage, error)
}

type BuildIndexOptions struct {
	IndexPath               string
	Sources                 []SearchableRecordSource
	MaximumRecordsPerSource uint64
	ProgressOutput          io.Writer
}

type SearchFilters struct {
	EligibleRegisteredTrawlers []*identity.RegisteredTrawlerIdentity
	After                      time.Time
	Before                     time.Time
}

type indexedPassage struct {
	recordDatabaseIdentifier            int64
	registeredTrawler                   string
	localTrawlerShortReference          string
	canonicalSearchResultGroupReference string
	searchMatchPresentationProtobuf     []byte
	searchableTextSectionDisplayName    string
	recordAnchorIdentifier              string
	sectionStartUTF8ByteOffset          int64
	sectionEndUTF8ByteOffset            int64
	content                             string
}

type denseSearchCandidate struct {
	indexedPassage
	passageDatabaseIdentifier int64
	cosineDistance            float64
}

type worstDenseSearchCandidateHeap []denseSearchCandidate

func (candidates worstDenseSearchCandidateHeap) Len() int { return len(candidates) }
func (candidates worstDenseSearchCandidateHeap) Less(left, right int) bool {
	if candidates[left].cosineDistance != candidates[right].cosineDistance {
		return candidates[left].cosineDistance > candidates[right].cosineDistance
	}
	return candidates[left].passageDatabaseIdentifier > candidates[right].passageDatabaseIdentifier
}
func (candidates worstDenseSearchCandidateHeap) Swap(left, right int) {
	candidates[left], candidates[right] = candidates[right], candidates[left]
}
func (candidates *worstDenseSearchCandidateHeap) Push(candidate any) {
	*candidates = append(*candidates, candidate.(denseSearchCandidate))
}
func (candidates *worstDenseSearchCandidateHeap) Pop() any {
	previous := *candidates
	lastIndex := len(previous) - 1
	candidate := previous[lastIndex]
	*candidates = previous[:lastIndex]
	return candidate
}

func IndexPath(stateRoot string) string {
	return filepath.Join(stateRoot, indexDirectoryName, indexFileName)
}

func BuildIndex(ctx context.Context, options BuildIndexOptions) (returnedError error) {
	if strings.TrimSpace(options.IndexPath) == "" || len(options.Sources) == 0 {
		return errors.New("dense search index path and searchable record sources are required")
	}
	if options.ProgressOutput == nil {
		options.ProgressOutput = io.Discard
	}
	if err := verifyEmbeddingGemmaModel(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(options.IndexPath), 0o700); err != nil {
		return err
	}
	buildingMarkerPath := options.IndexPath + ".building"
	buildingMarker, err := os.OpenFile(buildingMarkerPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := buildingMarker.Close(); err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, os.Remove(buildingMarkerPath)) }()
	temporaryIndex, err := os.CreateTemp(filepath.Dir(options.IndexPath), ".dense-*.sqlite")
	if err != nil {
		return err
	}
	temporaryIndexPath := temporaryIndex.Name()
	if err := temporaryIndex.Close(); err != nil {
		return err
	}
	defer func() {
		if returnedError != nil {
			returnedError = errors.Join(returnedError, os.Remove(temporaryIndexPath))
		}
	}()
	database, err := sql.Open("sqlite3", temporaryIndexPath)
	if err != nil {
		return err
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if database != nil {
			returnedError = errors.Join(returnedError, database.Close())
		}
	}()
	if err := initializeIndex(database); err != nil {
		return err
	}
	indexedRecordCount := uint64(0)
	indexedPassageCount := uint64(0)
	for _, source := range options.Sources {
		sourceRecordCount, sourcePassageCount, err := indexSearchableRecordSource(ctx, database, source, options.MaximumRecordsPerSource)
		if err != nil {
			return err
		}
		indexedRecordCount += sourceRecordCount
		indexedPassageCount += sourcePassageCount
		_, _ = fmt.Fprintf(
			options.ProgressOutput,
			"indexed_source=%s records=%d passages=%d\n",
			trawlkit.RegisteredTrawlerIdentityText(source.RegisteredTrawlerIdentity),
			sourceRecordCount,
			sourcePassageCount,
		)
	}
	if _, err := database.Exec(`update dense_search_index set complete = 1 where singleton = 1`); err != nil {
		return err
	}
	if err := database.Close(); err != nil {
		return err
	}
	database = nil
	if err := os.Chmod(temporaryIndexPath, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryIndexPath, options.IndexPath); err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		options.ProgressOutput,
		"dense_index_records=%d dense_index_passages=%d\n",
		indexedRecordCount,
		indexedPassageCount,
	)
	return err
}

func initializeIndex(database *sql.DB) error {
	_, err := database.Exec(`
		pragma journal_mode = delete;
		pragma synchronous = full;
		pragma foreign_keys = on;
		create table dense_search_index (
			singleton integer primary key check (singleton = 1),
			complete integer not null,
			model_name text not null,
			model_digest text not null,
			model_bytes integer not null,
			document_input_prefix text not null,
			query_input_prefix text not null,
			native_embedding_dimensions integer not null,
			stored_embedding_dimensions integer not null,
			maximum_passage_content_utf8_bytes integer not null
		);
		create table searchable_records (
			searchable_record_identifier integer primary key,
			registered_trawler text not null,
			canonical_record_reference text not null unique,
			canonical_search_result_group_reference text not null,
			local_trawler_short_reference text not null,
			archive_record_associated_unix_nanoseconds integer,
			search_match_presentation_protobuf blob not null
		);
		create table searchable_passages (
			searchable_passage_identifier integer primary key,
			searchable_record_identifier integer not null references searchable_records(searchable_record_identifier),
			searchable_text_section_display_name text not null,
			record_anchor_identifier text not null,
			section_start_utf8_byte_offset integer not null,
			section_end_utf8_byte_offset integer not null,
			searchable_passage_content text not null,
			embedding_float32_blob blob not null
		);
		create index searchable_records_by_trawler_and_time
			on searchable_records(registered_trawler, archive_record_associated_unix_nanoseconds);
		create index searchable_passages_by_record
			on searchable_passages(searchable_record_identifier);
		insert into dense_search_index values (1, 0, ?, ?, ?, ?, ?, ?, ?, ?);`,
		embeddingGemmaModelName,
		embeddingGemmaModelDigest,
		embeddingGemmaModelBytes,
		embeddingGemmaDocumentPrefix,
		embeddingGemmaQueryPrefix,
		embeddingGemmaNativeDimensions,
		embeddingGemmaStoredDimensions,
		maximumSearchablePassageContentUTF8Bytes,
	)
	return err
}

func indexSearchableRecordSource(
	ctx context.Context,
	database *sql.DB,
	source SearchableRecordSource,
	maximumRecords uint64,
) (recordCount uint64, passageCount uint64, returnedError error) {
	registeredTrawler := strings.TrimSpace(trawlkit.RegisteredTrawlerIdentityText(source.RegisteredTrawlerIdentity))
	if registeredTrawler == "" || source.ExportPage == nil {
		return 0, 0, errors.New("searchable record source identity and exporter are required")
	}
	var recordsAfterCanonicalReference *identity.CanonicalArchiveRecordReference
	for {
		pageMaximum := uint32(searchableRecordExportPageSize)
		if maximumRecords > 0 {
			remaining := maximumRecords - recordCount
			if remaining == 0 {
				return recordCount, passageCount, nil
			}
			pageMaximum = uint32(min(uint64(pageMaximum), remaining))
		}
		page, err := source.ExportPage(ctx, &searchablerecord.SearchableRecordExportRequest{
			RecordsAfterCanonicalRecordReference: recordsAfterCanonicalReference,
			MaximumRecordCount:                   pageMaximum,
		})
		if err != nil {
			return recordCount, passageCount, err
		}
		if page == nil {
			return recordCount, passageCount, errors.New("searchable record exporter returned no page")
		}
		pageRecordCount, pagePassageCount, err := indexSearchableRecordPage(ctx, database, registeredTrawler, source.RegisteredTrawlerDisplayName, page)
		if err != nil {
			return recordCount, passageCount, err
		}
		recordCount += pageRecordCount
		passageCount += pagePassageCount
		if page.GetAllSearchableRecordsExported() || len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()) == 0 {
			return recordCount, passageCount, nil
		}
		recordsAfterCanonicalReference = page.GetNextPageStartsAfterCanonicalRecordReference()
		if trawlkit.CanonicalArchiveRecordReferenceText(recordsAfterCanonicalReference) == "" {
			return recordCount, passageCount, errors.New("searchable record export page has no next-page reference")
		}
	}
}

func indexSearchableRecordPage(
	ctx context.Context,
	database *sql.DB,
	registeredTrawler string,
	registeredTrawlerDisplayName string,
	page *searchablerecord.TrawlerSearchableRecordExportPage,
) (recordCount uint64, passageCount uint64, returnedError error) {
	type recordAndPassages struct {
		record     *searchablerecord.SearchableArchiveRecord
		projection searchableRecordProjection
		passages   []indexedPassage
	}
	pageRecords := make([]recordAndPassages, 0, len(page.GetSearchableArchiveRecordsInCanonicalReferenceOrder()))
	allEmbeddingInputs := make([]string, 0)
	for _, record := range page.GetSearchableArchiveRecordsInCanonicalReferenceOrder() {
		projection, err := projectTypedSourceRecord(registeredTrawlerDisplayName, record)
		if err != nil {
			return 0, 0, err
		}
		projected := recordAndPassages{record: record, projection: projection}
		for _, section := range projection.textSections {
			for _, passage := range splitSearchableTextSectionIntoPassages(section.content) {
				formattedPassage := section.displayName + ":\n" + passage.content
				projected.passages = append(projected.passages, indexedPassage{
					searchableTextSectionDisplayName: section.displayName,
					recordAnchorIdentifier:           trawlkit.RecordAnchorIdentifierText(section.recordAnchor),
					sectionStartUTF8ByteOffset:       int64(passage.sectionStartUTF8ByteOffset),
					sectionEndUTF8ByteOffset:         int64(passage.sectionEndUTF8ByteOffset),
					content:                          passage.content,
				})
				allEmbeddingInputs = append(allEmbeddingInputs, embeddingGemmaDocumentPrefix+formattedPassage)
			}
		}
		if len(projected.passages) > 0 {
			pageRecords = append(pageRecords, projected)
		}
	}
	allStoredEmbeddings := make([][]float32, 0, len(allEmbeddingInputs))
	for batchStart := 0; batchStart < len(allEmbeddingInputs); batchStart += maximumEmbeddingRequestPassageCount {
		batchEnd := min(batchStart+maximumEmbeddingRequestPassageCount, len(allEmbeddingInputs))
		storedEmbeddings, err := requestStoredEmbeddings(ctx, allEmbeddingInputs[batchStart:batchEnd])
		if err != nil {
			return 0, 0, err
		}
		allStoredEmbeddings = append(allStoredEmbeddings, storedEmbeddings...)
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if returnedError != nil {
			_ = transaction.Rollback()
		}
	}()
	embeddingIndex := 0
	for _, projected := range pageRecords {
		presentationProtobuf, err := proto.Marshal(projected.projection.searchPresentation)
		if err != nil {
			return 0, 0, err
		}
		var associatedUnixNanoseconds any
		if exactTime := projected.projection.associatedTime.GetExactTime(); exactTime != nil && exactTime.IsValid() {
			associatedUnixNanoseconds = exactTime.AsTime().UnixNano()
		}
		recordInsertion, err := transaction.ExecContext(ctx, `
			insert into searchable_records(
				registered_trawler, canonical_record_reference,
				canonical_search_result_group_reference, local_trawler_short_reference,
				archive_record_associated_unix_nanoseconds, search_match_presentation_protobuf
			) values (?, ?, ?, ?, ?, ?)`,
			registeredTrawler,
			trawlkit.CanonicalArchiveRecordReferenceText(projected.record.GetCanonicalRecordReference()),
			trawlkit.CanonicalArchiveRecordReferenceText(projected.record.GetCanonicalSearchResultGroupReference()),
			trawlkit.LocalTrawlerShortReferenceText(projected.record.GetLocalShortReference()),
			associatedUnixNanoseconds,
			presentationProtobuf,
		)
		if err != nil {
			return 0, 0, err
		}
		recordIdentifier, err := recordInsertion.LastInsertId()
		if err != nil {
			return 0, 0, err
		}
		for _, passage := range projected.passages {
			if _, err := transaction.ExecContext(ctx, `
				insert into searchable_passages(
					searchable_record_identifier, searchable_text_section_display_name,
					record_anchor_identifier, section_start_utf8_byte_offset,
					section_end_utf8_byte_offset, searchable_passage_content,
					embedding_float32_blob
				) values (?, ?, ?, ?, ?, ?, ?)`,
				recordIdentifier,
				passage.searchableTextSectionDisplayName,
				passage.recordAnchorIdentifier,
				passage.sectionStartUTF8ByteOffset,
				passage.sectionEndUTF8ByteOffset,
				passage.content,
				encodeFloat32Embedding(allStoredEmbeddings[embeddingIndex]),
			); err != nil {
				return 0, 0, err
			}
			embeddingIndex++
			passageCount++
		}
		recordCount++
	}
	if embeddingIndex != len(allStoredEmbeddings) {
		return 0, 0, errors.New("embedding count does not match searchable passages")
	}
	return recordCount, passageCount, transaction.Commit()
}

func Search(
	ctx context.Context,
	indexPath string,
	query string,
	filters SearchFilters,
) ([]*federation.FederatedSearchMatch, Availability, error) {
	if _, err := os.Stat(indexPath + ".building"); err == nil {
		return nil, AvailabilityIndexBuilding, nil
	}
	if _, err := os.Stat(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, AvailabilityIndexNotBuilt, nil
		}
		return nil, AvailabilityIndexIncompatible, err
	}
	database, err := sql.Open("sqlite3", "file:"+indexPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil, AvailabilityIndexIncompatible, err
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	if err := verifyIndex(database); err != nil {
		return nil, AvailabilityIndexIncompatible, err
	}
	if err := verifyEmbeddingGemmaModel(ctx); err != nil {
		return nil, AvailabilityModelUnavailable, err
	}
	queryEmbeddings, err := requestStoredEmbeddings(ctx, []string{embeddingGemmaQueryPrefix + strings.TrimSpace(query)})
	if err != nil {
		return nil, AvailabilityModelUnavailable, err
	}
	candidates, err := generateDenseSearchCandidates(database, queryEmbeddings[0], filters)
	if err != nil {
		return nil, AvailabilityIndexIncompatible, err
	}
	matches := make([]*federation.FederatedSearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		searchMatchPresentation := &search.SearchMatchPresentation{}
		if err := proto.Unmarshal(candidate.searchMatchPresentationProtobuf, searchMatchPresentation); err != nil {
			return nil, AvailabilityIndexIncompatible, err
		}
		searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = searchMatchTextField(
			candidate.searchableTextSectionDisplayName,
			candidate.content,
		)
		link, err := trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
			RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity(candidate.registeredTrawler),
			LocalShortReference: trawlkit.NewLocalTrawlerShortReference(candidate.localTrawlerShortReference),
		})
		if err != nil {
			return nil, AvailabilityIndexIncompatible, err
		}
		var textPassage *identity.ArchiveRecordTextPassage
		if candidate.recordAnchorIdentifier != "" {
			textPassage = &identity.ArchiveRecordTextPassage{
				RecordAnchor:                      trawlkit.NewRecordAnchorIdentifier(candidate.recordAnchorIdentifier),
				SectionStartUtf8ByteOffset:        uint64(candidate.sectionStartUTF8ByteOffset),
				SectionEndUtf8ByteOffsetExclusive: uint64(candidate.sectionEndUTF8ByteOffset),
			}
		}
		matches = append(matches, &federation.FederatedSearchMatch{
			RecordAnchor:                        trawlkit.NewRecordAnchorIdentifier(candidate.recordAnchorIdentifier),
			SearchMatchPresentation:             searchMatchPresentation,
			TrawlLink:                           link,
			CanonicalSearchResultGroupReference: trawlkit.NewCanonicalArchiveRecordReference(candidate.canonicalSearchResultGroupReference),
			ArchiveRecordTextPassage:            textPassage,
		})
	}
	return matches, AvailabilityAvailable, nil
}

func verifyIndex(database *sql.DB) error {
	var complete int
	var modelName, modelDigest, documentPrefix, queryPrefix string
	var modelBytes int64
	var nativeDimensions, storedDimensions, maximumPassageBytes int
	err := database.QueryRow(`
		select complete, model_name, model_digest, model_bytes, document_input_prefix,
		       query_input_prefix, native_embedding_dimensions, stored_embedding_dimensions,
		       maximum_passage_content_utf8_bytes
		from dense_search_index where singleton = 1`).Scan(
		&complete,
		&modelName,
		&modelDigest,
		&modelBytes,
		&documentPrefix,
		&queryPrefix,
		&nativeDimensions,
		&storedDimensions,
		&maximumPassageBytes,
	)
	if err != nil {
		return err
	}
	if complete != 1 || modelName != embeddingGemmaModelName || modelDigest != embeddingGemmaModelDigest ||
		modelBytes != embeddingGemmaModelBytes || documentPrefix != embeddingGemmaDocumentPrefix ||
		queryPrefix != embeddingGemmaQueryPrefix || nativeDimensions != embeddingGemmaNativeDimensions ||
		storedDimensions != embeddingGemmaStoredDimensions || maximumPassageBytes != maximumSearchablePassageContentUTF8Bytes {
		return errors.New("dense search index does not match the released EmbeddingGemma contract")
	}
	return nil
}

func generateDenseSearchCandidates(
	database *sql.DB,
	queryEmbedding []float32,
	filters SearchFilters,
) ([]denseSearchCandidate, error) {
	statement := `
		select record.searchable_record_identifier, record.registered_trawler,
		       record.local_trawler_short_reference,
		       record.canonical_search_result_group_reference,
		       record.search_match_presentation_protobuf,
		       passage.searchable_text_section_display_name,
		       passage.record_anchor_identifier,
		       passage.section_start_utf8_byte_offset,
		       passage.section_end_utf8_byte_offset,
		       passage.searchable_passage_content,
		       passage.searchable_passage_identifier,
		       passage.embedding_float32_blob
		from searchable_passages passage
		join searchable_records record using (searchable_record_identifier)
		where 1 = 1`
	arguments := make([]any, 0)
	if len(filters.EligibleRegisteredTrawlers) > 0 {
		placeholders := make([]string, 0, len(filters.EligibleRegisteredTrawlers))
		for _, registeredTrawler := range filters.EligibleRegisteredTrawlers {
			placeholders = append(placeholders, "?")
			arguments = append(arguments, trawlkit.RegisteredTrawlerIdentityText(registeredTrawler))
		}
		statement += ` and record.registered_trawler in (` + strings.Join(placeholders, ",") + `)`
	}
	if !filters.After.IsZero() {
		statement += ` and record.archive_record_associated_unix_nanoseconds >= ?`
		arguments = append(arguments, filters.After.UnixNano())
	}
	if !filters.Before.IsZero() {
		statement += ` and record.archive_record_associated_unix_nanoseconds <= ?`
		arguments = append(arguments, filters.Before.UnixNano())
	}
	statement += ` order by record.canonical_search_result_group_reference, passage.searchable_passage_identifier`
	rows, err := database.Query(statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	topCandidates := make(worstDenseSearchCandidateHeap, 0, maximumDenseCandidateRecordCount)
	currentGroupReference := ""
	var bestCandidateForCurrentGroup denseSearchCandidate
	currentGroupHasCandidate := false
	retainBestCandidate := func() {
		if !currentGroupHasCandidate {
			return
		}
		if len(topCandidates) < maximumDenseCandidateRecordCount {
			heap.Push(&topCandidates, bestCandidateForCurrentGroup)
			return
		}
		if denseSearchCandidateIsBetter(bestCandidateForCurrentGroup, topCandidates[0]) {
			topCandidates[0] = bestCandidateForCurrentGroup
			heap.Fix(&topCandidates, 0)
		}
	}
	for rows.Next() {
		var candidate denseSearchCandidate
		var encodedEmbedding []byte
		if err := rows.Scan(
			&candidate.recordDatabaseIdentifier,
			&candidate.registeredTrawler,
			&candidate.localTrawlerShortReference,
			&candidate.canonicalSearchResultGroupReference,
			&candidate.searchMatchPresentationProtobuf,
			&candidate.searchableTextSectionDisplayName,
			&candidate.recordAnchorIdentifier,
			&candidate.sectionStartUTF8ByteOffset,
			&candidate.sectionEndUTF8ByteOffset,
			&candidate.content,
			&candidate.passageDatabaseIdentifier,
			&encodedEmbedding,
		); err != nil {
			return nil, err
		}
		passageEmbedding, err := decodeFloat32Embedding(encodedEmbedding)
		if err != nil {
			return nil, err
		}
		candidate.cosineDistance = cosineDistance(queryEmbedding, passageEmbedding)
		if currentGroupHasCandidate && candidate.canonicalSearchResultGroupReference != currentGroupReference {
			retainBestCandidate()
			currentGroupHasCandidate = false
		}
		currentGroupReference = candidate.canonicalSearchResultGroupReference
		if !currentGroupHasCandidate || denseSearchCandidateIsBetter(candidate, bestCandidateForCurrentGroup) {
			bestCandidateForCurrentGroup = candidate
			currentGroupHasCandidate = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	retainBestCandidate()
	sort.Slice(topCandidates, func(left, right int) bool {
		return denseSearchCandidateIsBetter(topCandidates[left], topCandidates[right])
	})
	return topCandidates, nil
}

func denseSearchCandidateIsBetter(left, right denseSearchCandidate) bool {
	if left.cosineDistance != right.cosineDistance {
		return left.cosineDistance < right.cosineDistance
	}
	return left.passageDatabaseIdentifier < right.passageDatabaseIdentifier
}

func encodeFloat32Embedding(embedding []float32) []byte {
	encoded := make([]byte, len(embedding)*4)
	for index, component := range embedding {
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(component))
	}
	return encoded
}

func decodeFloat32Embedding(encoded []byte) ([]float32, error) {
	if len(encoded) != embeddingGemmaStoredDimensions*4 {
		return nil, fmt.Errorf("stored embedding has %d bytes; expected %d", len(encoded), embeddingGemmaStoredDimensions*4)
	}
	embedding := make([]float32, embeddingGemmaStoredDimensions)
	for dimensionIndex := range embedding {
		embedding[dimensionIndex] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[dimensionIndex*4:]))
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
