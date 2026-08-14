package main

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"
)

const (
	hybridCandidateCanonicalRecordsPerBranch = 100
	reciprocalRankFusionRankConstant         = 60
)

type hybridRankedCanonicalArchiveRecord struct {
	record              rankedCanonicalArchiveRecord
	denseRank           int
	lexicalRank         int
	reciprocalRankScore float64
}

type hybridCandidateGenerationMetrics struct {
	acceptableRecordsInDenseCandidates   int
	acceptableRecordsInLexicalCandidates int
	acceptableRecordsInCandidateUnion    int
}

func evaluateHybridRetrieval(arguments []string) error {
	flags := flag.NewFlagSet("evaluate-hybrid", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private frozen corpus database")
	manifestPath := flags.String("manifest", "", "full-corpus frozen manifest")
	vectorPath := flags.String("vectors", "", "complete semantic vector index")
	queryPath := flags.String("queries", "", "private frozen retrieval questions")
	resultPath := flags.String("results", "", "new private hybrid evaluation result database")
	embeddingRuntimeVersion := flags.String("embedding-runtime-version", "", "exact embedding runtime version used for query vectors")
	ollamaURL := flags.String("ollama-endpoint", "http://127.0.0.1:11435", "isolated Ollama endpoint")
	teiURL := flags.String("tei-endpoint", "http://127.0.0.1:8081", "isolated Text Embeddings Inference endpoint")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *corpusPath == "" || *manifestPath == "" || *vectorPath == "" || *queryPath == "" || *resultPath == "" || *embeddingRuntimeVersion == "" {
		return errors.New("--corpus, --manifest, --vectors, --queries, --results and --embedding-runtime-version are required")
	}
	if _, err := os.Stat(*resultPath); err == nil {
		return errors.New("result database already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	corpusSHA256, err := sha256File(*corpusPath)
	if err != nil {
		return err
	}
	manifestSHA256, err := sha256File(*manifestPath)
	if err != nil {
		return err
	}
	querySHA256, err := sha256File(*queryPath)
	if err != nil {
		return err
	}
	vectorSHA256, err := sha256File(*vectorPath)
	if err != nil {
		return err
	}
	manifestDocumentCount, manifestOrderedDocumentIdentifiersSHA256, err := readFullCorpusManifestContract(*manifestPath, corpusSHA256)
	if err != nil {
		return err
	}
	vectorDatabase, err := sql.Open("sqlite3", "file:"+*vectorPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer vectorDatabase.Close()
	vectorDatabase.SetMaxOpenConns(1)
	indexContract, err := readSemanticVectorIndexContract(vectorDatabase)
	if err != nil {
		return err
	}
	if indexContract.corpusSHA256 != corpusSHA256 || indexContract.indexedDocumentCount != manifestDocumentCount {
		return errors.New("semantic index does not match the frozen full corpus")
	}
	if indexContract.orderedDocumentIdentifiersSHA256 != "" && indexContract.orderedDocumentIdentifiersSHA256 != manifestOrderedDocumentIdentifiersSHA256 {
		return errors.New("semantic index document identifiers do not match the frozen manifest")
	}

	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer corpus.Close()
	corpus.SetMaxOpenConns(1)
	if _, err := corpus.Exec("attach database ? as frozen_manifest", "file:"+*manifestPath+"?mode=ro&immutable=1"); err != nil {
		return fmt.Errorf("attach frozen manifest: %w", err)
	}
	queries, err := readAndResolveRetrievalEvaluationQueries(*queryPath, corpus)
	if err != nil {
		return err
	}
	if err := validateConstrainedSupportingEvidenceEligibility(queries, corpus); err != nil {
		return err
	}
	queryEmbeddings, runtimeRevision, err := embedEvaluationQueries(context.Background(), queries, indexContract, *ollamaURL, *teiURL)
	if err != nil {
		return err
	}
	if runtimeRevision != "" {
		indexContract.modelRevision = runtimeRevision
	}

	resultDatabase, err := createHybridRetrievalEvaluationResultDatabase(
		*resultPath,
		indexContract,
		corpusSHA256,
		manifestSHA256,
		querySHA256,
		vectorSHA256,
		manifestDocumentCount,
		*embeddingRuntimeVersion,
	)
	if err != nil {
		return err
	}
	defer resultDatabase.Close()

	startedAt := time.Now()
	denseRecordsByLaneAndQuery, eligibleRecordCountsByLaneAndQuery, err := retrieveHybridDenseCandidates(
		vectorDatabase,
		corpus,
		indexContract,
		queryEmbeddings,
		queries,
	)
	if err != nil {
		return err
	}
	aggregates := map[retrievalEvaluationLane]*retrievalAggregateMetrics{
		unfilteredRetrievalEvaluationLane:  {},
		constrainedRetrievalEvaluationLane: {},
	}
	for queryNumber, query := range queries {
		for _, lane := range []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane} {
			retrieval, err := retrieveProductionShapedCanonicalRecords(
				corpus,
				query.InformationNeed,
				constraintsForEvaluationLane(lane, query),
				denseRecordsByLaneAndQuery[lane][queryNumber],
				maximumRetrievedCanonicalRecordsPerQuery,
			)
			if err != nil {
				return err
			}
			candidateMetrics := calculateHybridCandidateGenerationMetrics(
				query,
				denseRecordsByLaneAndQuery[lane][queryNumber],
				retrieval.bm25Candidates,
			)
			hybridRecords := retrieval.fusedRecords
			rankedRecords := make([]rankedCanonicalArchiveRecord, len(hybridRecords))
			for recordNumber, record := range hybridRecords {
				rankedRecords[recordNumber] = record.record
				rankedRecords[recordNumber].cosineSimilarity = record.reciprocalRankScore
			}
			metrics := calculateQueryEvaluationMetrics(query, rankedRecords)
			if err := storeHybridRetrievalEvaluationResult(
				resultDatabase,
				lane,
				query,
				hybridRecords,
				eligibleRecordCountsByLaneAndQuery[lane][queryNumber],
				metrics,
				candidateMetrics,
			); err != nil {
				return err
			}
			addQueryEvaluationMetrics(aggregates[lane], query, metrics)
		}
	}
	elapsed := time.Since(startedAt)
	if _, err := resultDatabase.Exec("update evaluation_metadata set elapsed_nanoseconds = ?", elapsed.Nanoseconds()); err != nil {
		return err
	}
	if err := resultDatabase.Close(); err != nil {
		return err
	}
	if err := os.Chmod(*resultPath, 0o600); err != nil {
		return err
	}
	for _, lane := range []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane} {
		metrics := aggregates[lane]
		fmt.Printf("lane=%s queries=%d hit_at_5=%d hit_at_20=%d mean_reciprocal_rank=%.6f labelled_record_recall_at_20=%.6f labelled_source_recall_at_20=%.6f weakening_record_recall_at_20=%.6f\n",
			lane,
			metrics.queryCount,
			metrics.queriesWithAcceptableRecordAtFive,
			metrics.queriesWithAcceptableRecordAtTwenty,
			metrics.reciprocalRankTotal/float64(metrics.queryCount),
			ratio(metrics.acceptableRecordLabelsRetrieved, metrics.acceptableRecordLabels),
			ratio(metrics.acceptableSourceLabelsRetrieved, metrics.acceptableSourceLabels),
			ratio(metrics.weakeningRecordLabelsRetrieved, metrics.weakeningRecordLabels),
		)
	}
	fmt.Printf("results=%s elapsed=%s\n", *resultPath, elapsed.Round(time.Millisecond))
	return nil
}

func constraintsForEvaluationLane(lane retrievalEvaluationLane, query resolvedRetrievalEvaluationQuery) retrievalQueryConstraints {
	if lane == constrainedRetrievalEvaluationLane {
		return query.SuppliedConstraints
	}
	return retrievalQueryConstraints{}
}

func retrieveHybridDenseCandidates(vectorDatabase *sql.DB, corpus *sql.DB, contract semanticVectorIndexContract, queryEmbeddings [][]float32, queries []resolvedRetrievalEvaluationQuery) (map[retrievalEvaluationLane][][]rankedCanonicalArchiveRecord, map[retrievalEvaluationLane][]int64, error) {
	lanes := []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane}
	heapsByLaneAndQuery := make(map[retrievalEvaluationLane][]*scoredArchiveDocumentHeap, len(lanes))
	eligibleRecordCountsByLaneAndQuery := make(map[retrievalEvaluationLane][]int64, len(lanes))
	for _, lane := range lanes {
		heapsByLaneAndQuery[lane] = make([]*scoredArchiveDocumentHeap, len(queries))
		eligibleRecordCountsByLaneAndQuery[lane] = make([]int64, len(queries))
		for queryNumber := range queries {
			documents := make(scoredArchiveDocumentHeap, 0, hybridCandidateCanonicalRecordsPerBranch)
			heap.Init(&documents)
			heapsByLaneAndQuery[lane][queryNumber] = &documents
		}
	}
	flattenedQueryVectors := make([]float32, 0, len(queryEmbeddings)*contract.dimensions)
	for _, queryEmbedding := range queryEmbeddings {
		flattenedQueryVectors = append(flattenedQueryVectors, queryEmbedding...)
	}
	vectorRows, err := vectorDatabase.Query(fmt.Sprintf(
		"select %s, %s from %s order by %s",
		contract.vectorIdentifierColumn,
		contract.vectorEmbeddingColumn,
		contract.vectorTable,
		contract.vectorIdentifierColumn,
	))
	if err != nil {
		return nil, nil, err
	}
	defer vectorRows.Close()
	const documentMatrixBatchSize = 8192
	documentIdentifiers := make([]int64, 0, documentMatrixBatchSize)
	documentVectors := make([]float32, 0, documentMatrixBatchSize*contract.dimensions)
	completedCanonicalRecords := make(map[canonicalArchiveRecordIdentity]struct{}, contract.indexedDocumentCount)
	var currentCanonicalRecord canonicalArchiveRecordIdentity
	currentCanonicalRecordExists := false
	bestCurrentRecordByLaneAndQuery := make(map[retrievalEvaluationLane][]rankedCanonicalArchiveRecord, len(lanes))
	for _, lane := range lanes {
		bestCurrentRecordByLaneAndQuery[lane] = make([]rankedCanonicalArchiveRecord, len(queries))
	}
	flushCurrentCanonicalRecord := func() {
		if !currentCanonicalRecordExists {
			return
		}
		for _, lane := range lanes {
			for queryNumber, record := range bestCurrentRecordByLaneAndQuery[lane] {
				if math.IsInf(record.cosineSimilarity, -1) {
					continue
				}
				retainScoredArchiveDocument(
					heapsByLaneAndQuery[lane][queryNumber],
					record,
					hybridCandidateCanonicalRecordsPerBranch,
				)
			}
		}
		completedCanonicalRecords[currentCanonicalRecord] = struct{}{}
	}
	startCanonicalRecord := func(record rankedCanonicalArchiveRecord) error {
		if _, reappeared := completedCanonicalRecords[record.identity]; reappeared {
			return errors.New("semantic index does not keep canonical record chunks consecutive")
		}
		currentCanonicalRecord = record.identity
		currentCanonicalRecordExists = true
		for queryNumber, query := range queries {
			unfilteredRecord := record
			unfilteredRecord.cosineSimilarity = math.Inf(-1)
			bestCurrentRecordByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber] = unfilteredRecord
			eligibleRecordCountsByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber]++

			constrainedRecord := record
			constrainedRecord.cosineSimilarity = math.Inf(-1)
			bestCurrentRecordByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber] = constrainedRecord
			if evaluationQueryAcceptsRecord(query.SuppliedConstraints, record) {
				eligibleRecordCountsByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber]++
			}
		}
		return nil
	}
	processDocumentMatrix := func() error {
		if len(documentIdentifiers) == 0 {
			return nil
		}
		metadataByDocumentIdentifier, err := readEvaluationDocumentMetadata(corpus, documentIdentifiers)
		if err != nil {
			return err
		}
		scores := multiplyNormalizedDocumentAndQueryVectors(
			documentVectors,
			len(documentIdentifiers),
			flattenedQueryVectors,
			len(queries),
			contract.dimensions,
		)
		for documentNumber, documentIdentifier := range documentIdentifiers {
			record := metadataByDocumentIdentifier[documentIdentifier]
			if !currentCanonicalRecordExists || record.identity != currentCanonicalRecord {
				flushCurrentCanonicalRecord()
				if err := startCanonicalRecord(record); err != nil {
					return err
				}
			}
			for queryNumber, query := range queries {
				record.cosineSimilarity = float64(scores[documentNumber*len(queries)+queryNumber])
				if record.cosineSimilarity > bestCurrentRecordByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber].cosineSimilarity {
					bestCurrentRecordByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber] = record
				}
				if evaluationQueryAcceptsRecord(query.SuppliedConstraints, record) {
					if record.cosineSimilarity > bestCurrentRecordByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber].cosineSimilarity {
						bestCurrentRecordByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber] = record
					}
				}
			}
		}
		documentIdentifiers = documentIdentifiers[:0]
		documentVectors = documentVectors[:0]
		return nil
	}
	for vectorRows.Next() {
		var documentIdentifier int64
		var encodedVector []byte
		if err := vectorRows.Scan(&documentIdentifier, &encodedVector); err != nil {
			return nil, nil, err
		}
		if len(encodedVector) != contract.dimensions*4 {
			return nil, nil, errors.New("semantic index contains a vector with the wrong dimensions")
		}
		documentIdentifiers = append(documentIdentifiers, documentIdentifier)
		for offset := 0; offset < len(encodedVector); offset += 4 {
			documentVectors = append(documentVectors, math.Float32frombits(binary.LittleEndian.Uint32(encodedVector[offset:])))
		}
		if len(documentIdentifiers) == documentMatrixBatchSize {
			if err := processDocumentMatrix(); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := vectorRows.Err(); err != nil {
		return nil, nil, err
	}
	if err := processDocumentMatrix(); err != nil {
		return nil, nil, err
	}
	flushCurrentCanonicalRecord()

	retrievedRecordsByLaneAndQuery := make(map[retrievalEvaluationLane][][]rankedCanonicalArchiveRecord, len(lanes))
	for _, lane := range lanes {
		retrievedRecordsByLaneAndQuery[lane] = make([][]rankedCanonicalArchiveRecord, len(queries))
		for queryNumber := range queries {
			rankedRecords := append([]rankedCanonicalArchiveRecord(nil), *heapsByLaneAndQuery[lane][queryNumber]...)
			sort.Slice(rankedRecords, func(left, right int) bool {
				if rankedRecords[left].cosineSimilarity != rankedRecords[right].cosineSimilarity {
					return rankedRecords[left].cosineSimilarity > rankedRecords[right].cosineSimilarity
				}
				return rankedRecords[left].documentIdentifier < rankedRecords[right].documentIdentifier
			})
			retrievedRecordsByLaneAndQuery[lane][queryNumber] = rankedRecords
		}
	}
	return retrievedRecordsByLaneAndQuery, eligibleRecordCountsByLaneAndQuery, nil
}

func maximumChunksPerCanonicalRecord(corpus *sql.DB) (int, error) {
	var maximumChunks int
	err := corpus.QueryRow(`
		select max(chunk_count) from (
			select count(*) chunk_count
			from archive_documents
			group by registered_trawler, canonical_archive_record_reference
		)`).Scan(&maximumChunks)
	return maximumChunks, err
}

func calculateHybridCandidateGenerationMetrics(query resolvedRetrievalEvaluationQuery, denseRecords []rankedCanonicalArchiveRecord, lexicalRecords []rankedCanonicalArchiveRecord) hybridCandidateGenerationMetrics {
	denseCandidateIdentities := make(map[canonicalArchiveRecordIdentity]struct{}, len(denseRecords))
	lexicalCandidateIdentities := make(map[canonicalArchiveRecordIdentity]struct{}, len(lexicalRecords))
	for _, record := range denseRecords {
		denseCandidateIdentities[record.identity] = struct{}{}
	}
	for _, record := range lexicalRecords {
		lexicalCandidateIdentities[record.identity] = struct{}{}
	}
	var metrics hybridCandidateGenerationMetrics
	for acceptableRecord := range query.acceptableSupportingRecords {
		_, inDenseCandidates := denseCandidateIdentities[acceptableRecord]
		_, inLexicalCandidates := lexicalCandidateIdentities[acceptableRecord]
		if inDenseCandidates {
			metrics.acceptableRecordsInDenseCandidates++
		}
		if inLexicalCandidates {
			metrics.acceptableRecordsInLexicalCandidates++
		}
		if inDenseCandidates || inLexicalCandidates {
			metrics.acceptableRecordsInCandidateUnion++
		}
	}
	return metrics
}

func createHybridRetrievalEvaluationResultDatabase(resultPath string, contract semanticVectorIndexContract, corpusSHA256 string, manifestSHA256 string, querySHA256 string, vectorSHA256 string, documentCount int64, embeddingRuntimeVersion string) (*sql.DB, error) {
	if err := os.MkdirAll(filepathDirectory(resultPath), 0o700); err != nil {
		return nil, err
	}
	resultDatabase, err := sql.Open("sqlite3", resultPath)
	if err != nil {
		return nil, err
	}
	resultDatabase.SetMaxOpenConns(1)
	if err := resultDatabase.Ping(); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if err := os.Chmod(resultPath, 0o600); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if _, err := resultDatabase.Exec(`
		create table evaluation_metadata(
			model_identifier text not null,
			model_artifact_revision text not null,
			adapter text not null,
			pooling text not null,
			embedding_runtime text not null,
			embedding_runtime_version text not null,
			query_prefix text not null,
			document_prefix text not null,
			maximum_input_tokens integer not null,
			dimensions integer not null,
			corpus_sha256 text not null,
			manifest_sha256 text not null,
			query_sha256 text not null,
			vector_index_sha256 text not null,
			indexed_document_count integer not null,
			retrieval_method text not null,
			lexical_query_method text not null,
			candidate_canonical_records_per_branch integer not null,
			reciprocal_rank_fusion_rank_constant integer not null,
			maximum_retrieved_canonical_records_per_query integer not null,
			date_predicate text not null,
			elapsed_nanoseconds integer not null
		);
		create table retrieved_canonical_records(
			lane text not null,
			query_identifier text not null,
			rank integer not null,
			document_identifier integer not null,
			registered_trawler text not null,
			canonical_archive_record_reference text not null,
			local_short_reference text not null,
			dense_rank integer not null,
			lexical_rank integer not null,
			reciprocal_rank_fusion_score real not null,
			primary key(lane, query_identifier, rank),
			unique(lane, query_identifier, registered_trawler, canonical_archive_record_reference)
		);
		create table query_metrics(
			lane text not null,
			query_identifier text not null,
			category text not null,
			required_evidence_scope text not null,
			eligible_canonical_record_count integer not null,
			returned_canonical_record_count integer not null,
			acceptable_record_count integer not null,
			first_acceptable_rank integer not null,
			acceptable_records_retrieved_at_five integer not null,
			acceptable_records_retrieved_at_twenty integer not null,
			weakening_record_count integer not null,
			weakening_records_retrieved_at_twenty integer not null,
			acceptable_source_count integer not null,
			acceptable_sources_retrieved_at_twenty integer not null,
			acceptable_records_in_dense_candidates integer not null,
			acceptable_records_in_lexical_candidates integer not null,
			acceptable_records_in_candidate_union integer not null,
			primary key(lane, query_identifier)
		);
	`); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	if _, err := resultDatabase.Exec(`
		insert into evaluation_metadata values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		contract.modelIdentifier,
		contract.modelRevision,
		contract.adapter,
		contract.pooling,
		contract.queryEmbeddingRuntime,
		embeddingRuntimeVersion,
		contract.queryPrefix,
		contract.documentPrefix,
		contract.maximumInputTokens,
		contract.dimensions,
		corpusSHA256,
		manifestSHA256,
		querySHA256,
		vectorSHA256,
		documentCount,
		"canonical-record reciprocal rank fusion of dense cosine rank and SQLite FTS5 BM25 rank",
		"sanitized unique query tokens joined with OR; default FTS5 unicode61 tokenizer",
		hybridCandidateCanonicalRecordsPerBranch,
		reciprocalRankFusionRankConstant,
		maximumRetrievedCanonicalRecordsPerQuery,
		"inclusive comparison of substr(associated_time, 1, 10) against YYYY-MM-DD bounds",
	); err != nil {
		resultDatabase.Close()
		return nil, err
	}
	return resultDatabase, nil
}

func storeHybridRetrievalEvaluationResult(resultDatabase *sql.DB, lane retrievalEvaluationLane, query resolvedRetrievalEvaluationQuery, rankedRecords []hybridRankedCanonicalArchiveRecord, eligibleCanonicalRecordCount int64, metrics queryEvaluationMetrics, candidateMetrics hybridCandidateGenerationMetrics) error {
	transaction, err := resultDatabase.Begin()
	if err != nil {
		return err
	}
	for recordNumber, record := range rankedRecords {
		if _, err := transaction.Exec(`
			insert into retrieved_canonical_records values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			lane,
			query.Identifier,
			recordNumber+1,
			record.record.documentIdentifier,
			record.record.identity.registeredTrawler,
			record.record.identity.canonicalArchiveRecordReference,
			record.record.localShortReference,
			record.denseRank,
			record.lexicalRank,
			record.reciprocalRankScore,
		); err != nil {
			transaction.Rollback()
			return err
		}
	}
	if _, err := transaction.Exec(`
		insert into query_metrics values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lane,
		query.Identifier,
		query.Category,
		query.RequiredEvidenceScope,
		eligibleCanonicalRecordCount,
		len(rankedRecords),
		len(query.acceptableSupportingRecords),
		metrics.firstAcceptableRank,
		metrics.acceptableRecordsRetrievedAtFive,
		metrics.acceptableRecordsRetrievedAtTwenty,
		len(query.contradictionOrWeakeningRecords),
		metrics.weakeningRecordsRetrievedAtTwenty,
		metrics.acceptableSources,
		metrics.acceptableSourcesRetrievedAtTwenty,
		candidateMetrics.acceptableRecordsInDenseCandidates,
		candidateMetrics.acceptableRecordsInLexicalCandidates,
		candidateMetrics.acceptableRecordsInCandidateUnion,
	); err != nil {
		transaction.Rollback()
		return err
	}
	return transaction.Commit()
}
