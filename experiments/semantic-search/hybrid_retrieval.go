package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

const maximumSQLiteVectorSearchChunkCandidates = 4096

type productionShapedRetrievalResult struct {
	bm25Candidates []rankedCanonicalArchiveRecord
	fusedRecords   []hybridRankedCanonicalArchiveRecord
}

func retrieveProductionShapedCanonicalRecords(corpus *sql.DB, informationNeed string, constraints retrievalQueryConstraints, denseCandidates []rankedCanonicalArchiveRecord, resultLimit int) (productionShapedRetrievalResult, error) {
	bm25Candidates, err := retrieveBM25CanonicalRecordCandidates(corpus, informationNeed, constraints)
	if err != nil {
		return productionShapedRetrievalResult{}, err
	}
	return productionShapedRetrievalResult{
		bm25Candidates: bm25Candidates,
		fusedRecords:   fuseCanonicalRecordRanks(denseCandidates, bm25Candidates, resultLimit),
	}, nil
}

func retrieveBM25CanonicalRecordCandidates(corpus *sql.DB, informationNeed string, constraints retrievalQueryConstraints) ([]rankedCanonicalArchiveRecord, error) {
	matchExpression := fts5AnyTokenQuery(informationNeed)
	if matchExpression == "" {
		return nil, nil
	}
	maximumChunks, err := maximumChunksPerCanonicalRecord(corpus)
	if err != nil {
		return nil, err
	}
	queryText := `
		select document.document_identifier, bm25(archive_documents_fts),
		       document.registered_trawler, document.canonical_archive_record_reference,
		       document.local_short_reference, document.associated_time
		from archive_documents_fts
		join archive_documents document on document.document_identifier = archive_documents_fts.rowid
		join frozen_manifest.selected_documents selected on selected.document_identifier = document.document_identifier
		where archive_documents_fts match ?`
	arguments := []any{matchExpression}
	queryText, arguments = addRetrievalConstraints(queryText, arguments, constraints)
	queryText += ` order by bm25(archive_documents_fts), document.document_identifier limit ?`
	arguments = append(arguments, maximumChunks*hybridCandidateCanonicalRecordsPerBranch)
	rows, err := corpus.Query(queryText, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seenCanonicalRecords := make(map[canonicalArchiveRecordIdentity]struct{}, hybridCandidateCanonicalRecordsPerBranch)
	records := make([]rankedCanonicalArchiveRecord, 0, hybridCandidateCanonicalRecordsPerBranch)
	for rows.Next() {
		var record rankedCanonicalArchiveRecord
		if err := rows.Scan(
			&record.documentIdentifier,
			&record.cosineSimilarity,
			&record.identity.registeredTrawler,
			&record.identity.canonicalArchiveRecordReference,
			&record.localShortReference,
			&record.associatedTime,
		); err != nil {
			return nil, err
		}
		if _, duplicate := seenCanonicalRecords[record.identity]; duplicate {
			continue
		}
		seenCanonicalRecords[record.identity] = struct{}{}
		records = append(records, record)
		if len(records) == hybridCandidateCanonicalRecordsPerBranch {
			break
		}
	}
	return records, rows.Err()
}

func fts5AnyTokenQuery(value string) string {
	terms := make([]string, 0)
	seenTerms := make(map[string]struct{})
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		term := token.String()
		token.Reset()
		if _, duplicate := seenTerms[term]; duplicate {
			return
		}
		seenTerms[term] = struct{}{}
		terms = append(terms, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || unicode.IsMark(character) || character == '_' {
			token.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return strings.Join(terms, " OR ")
}

func fuseCanonicalRecordRanks(denseRecords []rankedCanonicalArchiveRecord, bm25Records []rankedCanonicalArchiveRecord, resultLimit int) []hybridRankedCanonicalArchiveRecord {
	fusedByIdentity := make(map[canonicalArchiveRecordIdentity]*hybridRankedCanonicalArchiveRecord, len(denseRecords)+len(bm25Records))
	for recordNumber, record := range denseRecords {
		rank := recordNumber + 1
		fusedByIdentity[record.identity] = &hybridRankedCanonicalArchiveRecord{
			record:              record,
			denseRank:           rank,
			reciprocalRankScore: 1 / float64(reciprocalRankFusionRankConstant+rank),
		}
	}
	for recordNumber, record := range bm25Records {
		rank := recordNumber + 1
		fusedRecord, found := fusedByIdentity[record.identity]
		if !found {
			fusedRecord = &hybridRankedCanonicalArchiveRecord{record: record}
			fusedByIdentity[record.identity] = fusedRecord
		}
		fusedRecord.lexicalRank = rank
		fusedRecord.reciprocalRankScore += 1 / float64(reciprocalRankFusionRankConstant+rank)
		if fusedRecord.denseRank == 0 || rank < fusedRecord.denseRank {
			fusedRecord.record = record
		}
	}
	fusedRecords := make([]hybridRankedCanonicalArchiveRecord, 0, len(fusedByIdentity))
	for _, record := range fusedByIdentity {
		fusedRecords = append(fusedRecords, *record)
	}
	sort.Slice(fusedRecords, func(left, right int) bool {
		if fusedRecords[left].reciprocalRankScore != fusedRecords[right].reciprocalRankScore {
			return fusedRecords[left].reciprocalRankScore > fusedRecords[right].reciprocalRankScore
		}
		leftBestRank := bestRetrievalBranchRank(fusedRecords[left])
		rightBestRank := bestRetrievalBranchRank(fusedRecords[right])
		if leftBestRank != rightBestRank {
			return leftBestRank < rightBestRank
		}
		if fusedRecords[left].record.identity.registeredTrawler != fusedRecords[right].record.identity.registeredTrawler {
			return fusedRecords[left].record.identity.registeredTrawler < fusedRecords[right].record.identity.registeredTrawler
		}
		return fusedRecords[left].record.identity.canonicalArchiveRecordReference < fusedRecords[right].record.identity.canonicalArchiveRecordReference
	})
	return fusedRecords[:min(len(fusedRecords), resultLimit)]
}

func bestRetrievalBranchRank(record hybridRankedCanonicalArchiveRecord) int {
	if record.denseRank == 0 {
		return record.lexicalRank
	}
	if record.lexicalRank == 0 {
		return record.denseRank
	}
	return min(record.denseRank, record.lexicalRank)
}

func searchBM25Corpus(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-bm25", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private frozen corpus database")
	manifestPath := flags.String("manifest", "", "frozen document manifest")
	limit := flags.Int("limit", 20, "maximum unique canonical records")
	source := flags.String("trawler", "", "optional registered trawler filter")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *corpusPath == "" || *manifestPath == "" || query == "" || *limit <= 0 {
		return errors.New("--corpus, --manifest, a positive --limit and QUERY are required")
	}
	corpus, err := openFrozenSearchCorpus(*corpusPath, *manifestPath)
	if err != nil {
		return err
	}
	defer func() { _ = corpus.Close() }()
	constraints := retrievalQueryConstraints{}
	if *source != "" {
		constraints.Sources = []string{*source}
	}
	records, err := retrieveBM25CanonicalRecordCandidates(corpus, query, constraints)
	if err != nil {
		return err
	}
	matches, err := semanticSearchMatchesForRankedRecords(corpus, records[:min(len(records), *limit)])
	if err != nil {
		return err
	}
	return printSemanticSearchMatches(output, matches)
}

func searchHybridCorpus(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-hybrid", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private frozen corpus database")
	manifestPath := flags.String("manifest", "", "frozen document manifest")
	vectorPath := flags.String("vectors", "", "private vector database")
	limit := flags.Int("limit", 20, "maximum unique canonical records")
	source := flags.String("trawler", "", "optional registered trawler filter")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *corpusPath == "" || *manifestPath == "" || *vectorPath == "" || query == "" || *limit <= 0 {
		return errors.New("--corpus, --manifest, --vectors, a positive --limit and QUERY are required")
	}
	corpus, err := openFrozenSearchCorpus(*corpusPath, *manifestPath)
	if err != nil {
		return err
	}
	defer func() { _ = corpus.Close() }()
	constraints := retrievalQueryConstraints{}
	if *source != "" {
		constraints.Sources = []string{*source}
	}
	denseCandidates, err := retrieveInteractiveDenseCanonicalRecordCandidates(corpus, *corpusPath, *vectorPath, query, *source)
	if err != nil {
		return err
	}
	retrieval, err := retrieveProductionShapedCanonicalRecords(corpus, query, constraints, denseCandidates, *limit)
	if err != nil {
		return err
	}
	rankedRecords := make([]rankedCanonicalArchiveRecord, len(retrieval.fusedRecords))
	for recordNumber, record := range retrieval.fusedRecords {
		rankedRecords[recordNumber] = record.record
	}
	matches, err := semanticSearchMatchesForRankedRecords(corpus, rankedRecords)
	if err != nil {
		return err
	}
	return printSemanticSearchMatches(output, matches)
}

func openFrozenSearchCorpus(corpusPath string, manifestPath string) (*sql.DB, error) {
	corpus, err := sql.Open("sqlite3", "file:"+corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	corpus.SetMaxOpenConns(1)
	if _, err := corpus.Exec("attach database ? as frozen_manifest", "file:"+manifestPath+"?mode=ro&immutable=1"); err != nil {
		_ = corpus.Close()
		return nil, err
	}
	return corpus, nil
}

func retrieveInteractiveDenseCanonicalRecordCandidates(corpus *sql.DB, corpusPath string, vectorPath string, query string, source string) ([]rankedCanonicalArchiveRecord, error) {
	vectorDatabase, err := sql.Open("sqlite3", "file:"+vectorPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer func() { _ = vectorDatabase.Close() }()
	var indexedModel, indexedManifestSHA256, indexedDocumentPrefix, indexedQueryPrefix, indexedCorpusSHA256 string
	var indexedMaximumInputTokens, indexedDimensions int
	var corpusDocumentCount, indexedDocumentCount int64
	if err := vectorDatabase.QueryRow(`
		select model, model_manifest_sha256, document_prefix, query_prefix,
		       maximum_input_tokens, dimensions, corpus_sha256,
		       corpus_document_count, indexed_document_count
		from embedding_index_metadata where singleton = 1`).Scan(
		&indexedModel, &indexedManifestSHA256, &indexedDocumentPrefix, &indexedQueryPrefix,
		&indexedMaximumInputTokens, &indexedDimensions, &indexedCorpusSHA256,
		&corpusDocumentCount, &indexedDocumentCount,
	); err != nil {
		return nil, err
	}
	modelDefinition, allowed := experimentalLocalEmbeddingModels[indexedModel]
	if !allowed {
		return nil, fmt.Errorf("semantic index uses unsupported model %q", indexedModel)
	}
	currentCorpusSHA256, err := sha256File(corpusPath)
	if err != nil {
		return nil, err
	}
	if indexedModel != modelDefinition.name || indexedManifestSHA256 != modelDefinition.manifestSHA256 || indexedDocumentPrefix != modelDefinition.documentPrefix || indexedQueryPrefix != modelDefinition.queryPrefix || indexedMaximumInputTokens != embeddingInputTokenLimit || indexedCorpusSHA256 != currentCorpusSHA256 {
		return nil, errors.New("semantic index does not match the requested corpus and embedding model contract")
	}
	if indexedDocumentCount != corpusDocumentCount {
		return nil, fmt.Errorf("semantic index is incomplete: indexed %d of %d documents", indexedDocumentCount, corpusDocumentCount)
	}
	if err := verifyPinnedLocalOllamaModel(context.Background(), indexedModel); err != nil {
		return nil, err
	}
	response, err := requestOllamaEmbeddings(context.Background(), indexedModel, indexedDimensions, []string{indexedQueryPrefix + query})
	if err != nil {
		return nil, err
	}
	if len(response.Embeddings) != 1 || len(response.Embeddings[0]) != indexedDimensions {
		return nil, errors.New("ollama returned an invalid query embedding shape")
	}
	maximumChunks, err := maximumChunksPerCanonicalRecord(corpus)
	if err != nil {
		return nil, err
	}
	requestedVectorCandidates := maximumChunks * hybridCandidateCanonicalRecordsPerBranch
	requestedVectorCandidates = min(requestedVectorCandidates, maximumSQLiteVectorSearchChunkCandidates)
	var rows *sql.Rows
	if source == "" {
		rows, err = vectorDatabase.Query(`select rowid, distance from document_embeddings where embedding match ? and k = ? order by distance`, encodeFloat32Vector(response.Embeddings[0]), requestedVectorCandidates)
	} else {
		rows, err = vectorDatabase.Query(`select rowid, distance from document_embeddings where embedding match ? and k = ? and registered_trawler = ? order by distance`, encodeFloat32Vector(response.Embeddings[0]), requestedVectorCandidates, source)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seenCanonicalRecords := make(map[canonicalArchiveRecordIdentity]struct{}, hybridCandidateCanonicalRecordsPerBranch)
	records := make([]rankedCanonicalArchiveRecord, 0, hybridCandidateCanonicalRecordsPerBranch)
	for rows.Next() {
		var record rankedCanonicalArchiveRecord
		if err := rows.Scan(&record.documentIdentifier, &record.cosineSimilarity); err != nil {
			return nil, err
		}
		if err := corpus.QueryRow(`
			select registered_trawler, canonical_archive_record_reference, local_short_reference, associated_time
			from archive_documents where document_identifier = ?`, record.documentIdentifier).Scan(
			&record.identity.registeredTrawler,
			&record.identity.canonicalArchiveRecordReference,
			&record.localShortReference,
			&record.associatedTime,
		); err != nil {
			return nil, err
		}
		if _, duplicate := seenCanonicalRecords[record.identity]; duplicate {
			continue
		}
		seenCanonicalRecords[record.identity] = struct{}{}
		records = append(records, record)
		if len(records) == hybridCandidateCanonicalRecordsPerBranch {
			break
		}
	}
	return records, rows.Err()
}

func semanticSearchMatchesForRankedRecords(corpus *sql.DB, records []rankedCanonicalArchiveRecord) ([]semanticSearchMatch, error) {
	matches := make([]semanticSearchMatch, 0, len(records))
	for _, record := range records {
		match := semanticSearchMatch{documentIdentifier: record.documentIdentifier}
		if err := corpus.QueryRow(`
			select canonical_archive_record_reference, local_short_reference, registered_trawler,
			       archive_record_kind, associated_time, searchable_text
			from archive_documents where document_identifier = ?`, record.documentIdentifier).Scan(
			&match.reference,
			&match.localShortReference,
			&match.registeredTrawler,
			&match.recordKind,
			&match.associatedTime,
			&match.searchableText,
		); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, nil
}
