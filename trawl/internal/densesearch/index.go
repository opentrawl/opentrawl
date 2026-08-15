package densesearch

import (
	"bytes"
	"container/heap"
	"context"
	"crypto/sha256"
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
	"sync"
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
	AvailabilityAvailableButStale
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
	StateRoot                string
	IndexPath                string
	Sources                  []SearchableRecordSource
	WithSourceArchivesLocked func(func() error) error
	ProgressOutput           io.Writer
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

var (
	executableSHA256Once  sync.Once
	executableSHA256Value []byte
	executableSHA256Error error
)

func currentExecutableSHA256() ([]byte, error) {
	executableSHA256Once.Do(func() {
		executablePath, err := os.Executable()
		if err != nil {
			executableSHA256Error = err
			return
		}
		executable, err := os.Open(executablePath)
		if err != nil {
			executableSHA256Error = err
			return
		}
		defer func() { _ = executable.Close() }()
		hash := sha256.New()
		if _, err := io.Copy(hash, executable); err != nil {
			executableSHA256Error = err
			return
		}
		executableSHA256Value = hash.Sum(nil)
	})
	return append([]byte(nil), executableSHA256Value...), executableSHA256Error
}

func initializeIndex(database *sql.DB) error {
	executableSHA256, err := currentExecutableSHA256()
	if err != nil {
		return err
	}
	_, err = database.Exec(`
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
			maximum_passage_content_utf8_bytes integer not null,
			trawl_executable_sha256 blob not null
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
			embedding_input_fingerprint blob not null,
			embedding_float32_blob blob
		);
		create table searchable_source_generations (
			registered_trawler text primary key,
			required_generation integer not null check(required_generation > 0),
			all_source_records_indexed integer not null,
			next_page_starts_after_canonical_record_reference text not null
		);
		create table reusable_passage_embeddings (
			embedding_input_fingerprint blob primary key,
			embedding_float32_blob blob not null
		);
		create index searchable_records_by_trawler_and_time
			on searchable_records(registered_trawler, archive_record_associated_unix_nanoseconds);
		create index searchable_passages_by_record
			on searchable_passages(searchable_record_identifier);
		insert into dense_search_index values (1, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		embeddingGemmaModelName,
		embeddingGemmaModelDigest,
		embeddingGemmaModelBytes,
		embeddingGemmaDocumentPrefix,
		embeddingGemmaQueryPrefix,
		embeddingGemmaNativeDimensions,
		embeddingGemmaStoredDimensions,
		maximumSearchablePassageContentUTF8Bytes,
		executableSHA256,
	)
	return err
}

func Search(
	ctx context.Context,
	indexPath string,
	query string,
	filters SearchFilters,
) ([]*federation.FederatedSearchMatch, Availability, error) {
	if _, err := os.Stat(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if building, buildingErr := IndexBuildIsActive(filepath.Dir(filepath.Dir(indexPath))); buildingErr == nil && building {
				return nil, AvailabilityIndexBuilding, nil
			}
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
	if err := verifyReadableIndex(database); err != nil {
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
	availability := AvailabilityAvailable
	stateRoot := filepath.Dir(filepath.Dir(indexPath))
	projectionCurrent, projectionErr := indexMatchesCurrentExecutable(database)
	if stale, staleErr := indexHasStaleSources(ctx, stateRoot, database, filters); staleErr != nil || stale || projectionErr != nil || !projectionCurrent {
		availability = AvailabilityAvailableButStale
	}
	return matches, availability, nil
}

func indexHasStaleSources(
	ctx context.Context,
	stateRoot string,
	index *sql.DB,
	filters SearchFilters,
) (bool, error) {
	rows, err := index.QueryContext(ctx, `
		select registered_trawler, required_generation
		from searchable_source_generations
		where all_source_records_indexed = 1
		order by registered_trawler`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	indexedGenerations := make(map[string]SourceGeneration)
	for rows.Next() {
		var registeredTrawler string
		var requiredGeneration uint64
		if err := rows.Scan(&registeredTrawler, &requiredGeneration); err != nil {
			return false, err
		}
		indexedGenerations[registeredTrawler] = SourceGeneration(requiredGeneration)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	registeredTrawlers := make([]string, 0, len(filters.EligibleRegisteredTrawlers))
	for _, registeredTrawler := range filters.EligibleRegisteredTrawlers {
		registeredTrawlers = append(
			registeredTrawlers,
			trawlkit.RegisteredTrawlerIdentityText(registeredTrawler),
		)
	}
	if len(registeredTrawlers) == 0 {
		for registeredTrawler := range indexedGenerations {
			registeredTrawlers = append(registeredTrawlers, registeredTrawler)
		}
	}
	required, err := RequiredSourceGenerations(ctx, stateRoot, registeredTrawlers)
	if err != nil {
		return false, err
	}
	for _, requiredGeneration := range required {
		if indexedGenerations[requiredGeneration.RegisteredTrawler] != requiredGeneration.RequiredGeneration {
			return true, nil
		}
	}
	return false, nil
}

func verifyReadableIndex(database *sql.DB) error {
	return verifyIndexContract(database, true, false)
}

func indexMatchesCurrentExecutable(database *sql.DB) (bool, error) {
	var indexedExecutableSHA256 []byte
	if err := database.QueryRow(`select trawl_executable_sha256 from dense_search_index where singleton = 1`).Scan(&indexedExecutableSHA256); err != nil {
		return false, err
	}
	currentExecutableSHA256, err := currentExecutableSHA256()
	if err != nil {
		return false, err
	}
	return bytes.Equal(indexedExecutableSHA256, currentExecutableSHA256), nil
}

func verifyIndexContract(database *sql.DB, requireComplete, requireCurrentExecutable bool) error {
	var complete int
	var modelName, modelDigest, documentPrefix, queryPrefix string
	var modelBytes int64
	var nativeDimensions, storedDimensions, maximumPassageBytes int
	var indexedExecutableSHA256 []byte
	err := database.QueryRow(`
		select complete, model_name, model_digest, model_bytes, document_input_prefix,
		       query_input_prefix, native_embedding_dimensions, stored_embedding_dimensions,
		       maximum_passage_content_utf8_bytes, trawl_executable_sha256
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
		&indexedExecutableSHA256,
	)
	if err != nil {
		return err
	}
	if requireComplete && complete != 1 {
		return errors.New("dense search index is incomplete")
	}
	if complete != 0 && complete != 1 || modelName != embeddingGemmaModelName || modelDigest != embeddingGemmaModelDigest ||
		modelBytes != embeddingGemmaModelBytes || documentPrefix != embeddingGemmaDocumentPrefix ||
		queryPrefix != embeddingGemmaQueryPrefix || nativeDimensions != embeddingGemmaNativeDimensions ||
		storedDimensions != embeddingGemmaStoredDimensions || maximumPassageBytes != maximumSearchablePassageContentUTF8Bytes {
		return errors.New("dense search index does not match the released EmbeddingGemma contract")
	}
	if requireCurrentExecutable {
		currentExecutableSHA256, err := currentExecutableSHA256()
		if err != nil {
			return err
		}
		if !bytes.Equal(indexedExecutableSHA256, currentExecutableSHA256) {
			return errors.New("dense search index does not match the current trawl projection contract")
		}
	}
	if _, err := database.Exec(`select embedding_input_fingerprint from searchable_passages limit 0`); err != nil {
		return err
	}
	if _, err := database.Exec(`select required_generation from searchable_source_generations limit 0`); err != nil {
		return err
	}
	if _, err := database.Exec(`select embedding_input_fingerprint from reusable_passage_embeddings limit 0`); err != nil {
		return err
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
	if len(filters.EligibleRegisteredTrawlers) == 0 {
		statement += ` and 1 = 0`
	} else {
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
