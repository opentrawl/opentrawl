package main

import (
	"bytes"
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const maximumRetrievedCanonicalRecordsPerQuery = 20

type retrievalEvaluationLane string

const (
	unfilteredRetrievalEvaluationLane  retrievalEvaluationLane = "unfiltered"
	constrainedRetrievalEvaluationLane retrievalEvaluationLane = "constrained"
)

type retrievalEvaluationQuery struct {
	Identifier                    string                    `json:"task_id"`
	Category                      string                    `json:"category"`
	InformationNeed               string                    `json:"information_need"`
	AcceptableSupportingLinks     []string                  `json:"canonical_acceptable_supporting_refs"`
	ContradictionOrWeakeningLinks []string                  `json:"contradiction_or_weakening_refs"`
	RequiredEvidenceScope         string                    `json:"required_evidence_scope"`
	SuppliedConstraints           retrievalQueryConstraints `json:"supplied_constraints"`
}

type retrievalQueryConstraints struct {
	After   string   `json:"after"`
	Before  string   `json:"before"`
	Sources []string `json:"sources"`
}

type canonicalArchiveRecordIdentity struct {
	registeredTrawler               string
	canonicalArchiveRecordReference string
}

type resolvedRetrievalEvaluationQuery struct {
	retrievalEvaluationQuery
	acceptableSupportingRecords     map[canonicalArchiveRecordIdentity]struct{}
	contradictionOrWeakeningRecords map[canonicalArchiveRecordIdentity]struct{}
}

type semanticVectorIndexContract struct {
	modelIdentifier                  string
	modelRevision                    string
	adapter                          string
	pooling                          string
	queryPrefix                      string
	documentPrefix                   string
	maximumInputTokens               int
	dimensions                       int
	corpusSHA256                     string
	orderedDocumentIdentifiersSHA256 string
	indexedDocumentCount             int64
	vectorTable                      string
	vectorIdentifierColumn           string
	vectorEmbeddingColumn            string
	queryEmbeddingRuntime            queryEmbeddingRuntime
}

type queryEmbeddingRuntime string

const (
	ollamaQueryEmbeddingRuntime queryEmbeddingRuntime = "ollama"
	teiQueryEmbeddingRuntime    queryEmbeddingRuntime = "text-embeddings-inference"
)

type rankedCanonicalArchiveRecord struct {
	documentIdentifier  int64
	identity            canonicalArchiveRecordIdentity
	localShortReference string
	associatedTime      string
	cosineSimilarity    float64
}

type scoredArchiveDocumentHeap []rankedCanonicalArchiveRecord

func (documents scoredArchiveDocumentHeap) Len() int { return len(documents) }
func (documents scoredArchiveDocumentHeap) Less(left, right int) bool {
	return documents[left].cosineSimilarity < documents[right].cosineSimilarity
}
func (documents scoredArchiveDocumentHeap) Swap(left, right int) {
	documents[left], documents[right] = documents[right], documents[left]
}
func (documents *scoredArchiveDocumentHeap) Push(value any) {
	*documents = append(*documents, value.(rankedCanonicalArchiveRecord))
}
func (documents *scoredArchiveDocumentHeap) Pop() any {
	previous := *documents
	lastIndex := len(previous) - 1
	value := previous[lastIndex]
	*documents = previous[:lastIndex]
	return value
}

type queryEvaluationMetrics struct {
	firstAcceptableRank                int
	acceptableRecordsRetrievedAtFive   int
	acceptableRecordsRetrievedAtTwenty int
	weakeningRecordsRetrievedAtTwenty  int
	acceptableSources                  int
	acceptableSourcesRetrievedAtTwenty int
}

type retrievalAggregateMetrics struct {
	queryCount                          int
	queriesWithAcceptableRecordAtFive   int
	queriesWithAcceptableRecordAtTwenty int
	reciprocalRankTotal                 float64
	acceptableRecordLabels              int
	acceptableRecordLabelsRetrieved     int
	weakeningRecordLabels               int
	weakeningRecordLabelsRetrieved      int
	acceptableSourceLabels              int
	acceptableSourceLabelsRetrieved     int
}

type teiEmbeddingRequest struct {
	Inputs    []string `json:"inputs"`
	Truncate  bool     `json:"truncate"`
	Normalize bool     `json:"normalize"`
}

type teiRuntimeInformation struct {
	ModelIdentifier string `json:"model_id"`
	ModelRevision   string `json:"model_sha"`
	MaximumLength   int    `json:"max_input_length"`
	ModelType       struct {
		Embedding struct {
			Pooling string `json:"pooling"`
		} `json:"embedding"`
	} `json:"model_type"`
}

func evaluateSemanticRetrieval(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("evaluate", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private frozen corpus database")
	manifestPath := flags.String("manifest", "", "full-corpus frozen manifest")
	vectorPath := flags.String("vectors", "", "complete semantic vector index")
	queryPath := flags.String("queries", "", "private frozen retrieval questions")
	resultPath := flags.String("results", "", "new private evaluation result database")
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
	defer func() { _ = vectorDatabase.Close() }()
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
	defer func() { _ = corpus.Close() }()
	corpus.SetMaxOpenConns(1)
	if _, err := vectorDatabase.Exec("attach database ? as corpus", "file:"+*corpusPath+"?mode=ro&immutable=1"); err != nil {
		return fmt.Errorf("attach frozen corpus: %w", err)
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

	resultDatabase, err := createRetrievalEvaluationResultDatabase(
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
	defer func() { _ = resultDatabase.Close() }()

	startedAt := time.Now()
	retrievedRecordsByLaneAndQuery, eligibleRecordCountsByLaneAndQuery, err := retrieveAllExactSemanticMatches(
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
			rankedRecords := retrievedRecordsByLaneAndQuery[lane][queryNumber]
			eligibleCanonicalRecordCount := eligibleRecordCountsByLaneAndQuery[lane][queryNumber]
			metrics := calculateQueryEvaluationMetrics(query, rankedRecords)
			if err := storeRetrievalEvaluationResult(resultDatabase, lane, query, rankedRecords, eligibleCanonicalRecordCount, metrics); err != nil {
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
		_, _ = fmt.Fprintf(output, "lane=%s queries=%d hit_at_5=%d hit_at_20=%d mean_reciprocal_rank=%.6f labelled_record_recall_at_20=%.6f labelled_source_recall_at_20=%.6f weakening_record_recall_at_20=%.6f\n",
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
	_, _ = fmt.Fprintf(output, "results=%s elapsed=%s\n", *resultPath, elapsed.Round(time.Millisecond))
	return nil
}

func readFullCorpusManifestContract(manifestPath string, corpusSHA256 string) (int64, string, error) {
	manifest, err := sql.Open("sqlite3", "file:"+manifestPath+"?mode=ro&immutable=1")
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = manifest.Close() }()
	var storedCorpusSHA256, orderedDocumentIdentifiersSHA256 string
	var selectedDocumentCount int64
	if err := manifest.QueryRow(`
		select corpus_sha256, selected_document_count, ordered_document_identifiers_sha256
		from manifest_metadata`).Scan(&storedCorpusSHA256, &selectedDocumentCount, &orderedDocumentIdentifiersSHA256); err != nil {
		return 0, "", err
	}
	if storedCorpusSHA256 != corpusSHA256 {
		return 0, "", errors.New("manifest does not match the frozen corpus")
	}
	var actualSelectedDocumentCount int64
	if err := manifest.QueryRow("select count(*) from selected_documents").Scan(&actualSelectedDocumentCount); err != nil {
		return 0, "", err
	}
	if actualSelectedDocumentCount != selectedDocumentCount {
		return 0, "", errors.New("manifest document count does not match its metadata")
	}
	return selectedDocumentCount, orderedDocumentIdentifiersSHA256, nil
}

func readSemanticVectorIndexContract(vectorDatabase *sql.DB) (semanticVectorIndexContract, error) {
	var hasProductMetadata, hasScientificMetadata int
	if err := vectorDatabase.QueryRow(`
		select count(*) from sqlite_master
		where type = 'table' and name = 'embedding_index_metadata'`).Scan(&hasProductMetadata); err != nil {
		return semanticVectorIndexContract{}, err
	}
	if err := vectorDatabase.QueryRow(`
		select count(*) from sqlite_master
		where type = 'table' and name = 'index_metadata'`).Scan(&hasScientificMetadata); err != nil {
		return semanticVectorIndexContract{}, err
	}
	if hasProductMetadata+hasScientificMetadata != 1 {
		return semanticVectorIndexContract{}, errors.New("vector database has no unambiguous supported metadata contract")
	}
	if hasProductMetadata == 1 {
		var contract semanticVectorIndexContract
		var statusDocumentCount, corpusDocumentCount int64
		if err := vectorDatabase.QueryRow(`
			select model, model_manifest_sha256, document_prefix, query_prefix,
			       maximum_input_tokens, dimensions, corpus_sha256,
			       corpus_document_count, indexed_document_count
			from embedding_index_metadata where singleton = 1`).Scan(
			&contract.modelIdentifier,
			&contract.modelRevision,
			&contract.documentPrefix,
			&contract.queryPrefix,
			&contract.maximumInputTokens,
			&contract.dimensions,
			&contract.corpusSHA256,
			&corpusDocumentCount,
			&statusDocumentCount,
		); err != nil {
			return semanticVectorIndexContract{}, err
		}
		if statusDocumentCount != corpusDocumentCount {
			return semanticVectorIndexContract{}, errors.New("product semantic index is incomplete")
		}
		contract.indexedDocumentCount = statusDocumentCount
		contract.adapter = contract.modelIdentifier
		contract.vectorTable = "document_embeddings"
		contract.vectorIdentifierColumn = "rowid"
		contract.vectorEmbeddingColumn = "embedding"
		contract.queryEmbeddingRuntime = ollamaQueryEmbeddingRuntime
		return contract, nil
	}
	var contract semanticVectorIndexContract
	var status string
	if err := vectorDatabase.QueryRow(`
		select model, adapter, pooling, query_prefix, document_prefix,
		       stored_dimensions, corpus_sha256, ordered_document_identifiers_sha256,
		       configured_max_input_tokens, status, indexed_document_count
		from index_metadata`).Scan(
		&contract.modelIdentifier,
		&contract.adapter,
		&contract.pooling,
		&contract.queryPrefix,
		&contract.documentPrefix,
		&contract.dimensions,
		&contract.corpusSHA256,
		&contract.orderedDocumentIdentifiersSHA256,
		&contract.maximumInputTokens,
		&status,
		&contract.indexedDocumentCount,
	); err != nil {
		return semanticVectorIndexContract{}, err
	}
	if status != "complete" {
		return semanticVectorIndexContract{}, errors.New("scientific semantic index is incomplete")
	}
	contract.vectorTable = "document_vectors"
	contract.vectorIdentifierColumn = "document_identifier"
	contract.vectorEmbeddingColumn = "embedding"
	contract.queryEmbeddingRuntime = teiQueryEmbeddingRuntime
	return contract, nil
}

func readAndResolveRetrievalEvaluationQueries(queryPath string, corpus *sql.DB) ([]resolvedRetrievalEvaluationQuery, error) {
	body, err := os.ReadFile(queryPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	queries := make([]resolvedRetrievalEvaluationQuery, 0, len(lines))
	identifiers := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		var query retrievalEvaluationQuery
		if err := json.Unmarshal([]byte(line), &query); err != nil {
			return nil, err
		}
		if query.Identifier == "" || query.Category == "" || strings.TrimSpace(query.InformationNeed) == "" || len(query.AcceptableSupportingLinks) == 0 {
			return nil, errors.New("retrieval query is missing required typed values")
		}
		if _, duplicate := identifiers[query.Identifier]; duplicate {
			return nil, errors.New("retrieval query identifiers are not unique")
		}
		identifiers[query.Identifier] = struct{}{}
		acceptableSupportingRecords, err := resolveOpenTrawlLinksToCanonicalRecords(corpus, query.AcceptableSupportingLinks)
		if err != nil {
			return nil, fmt.Errorf("resolve supporting records for %s: %w", query.Identifier, err)
		}
		contradictionOrWeakeningRecords, err := resolveOpenTrawlLinksToCanonicalRecords(corpus, query.ContradictionOrWeakeningLinks)
		if err != nil {
			return nil, fmt.Errorf("resolve weakening records for %s: %w", query.Identifier, err)
		}
		queries = append(queries, resolvedRetrievalEvaluationQuery{
			retrievalEvaluationQuery:        query,
			acceptableSupportingRecords:     acceptableSupportingRecords,
			contradictionOrWeakeningRecords: contradictionOrWeakeningRecords,
		})
	}
	if len(queries) == 0 {
		return nil, errors.New("retrieval query file is empty")
	}
	return queries, nil
}

func resolveOpenTrawlLinksToCanonicalRecords(corpus *sql.DB, links []string) (map[canonicalArchiveRecordIdentity]struct{}, error) {
	records := make(map[canonicalArchiveRecordIdentity]struct{}, len(links))
	for _, link := range links {
		registeredTrawler, localShortReference, found := strings.Cut(link, ":")
		if !found || registeredTrawler == "" || localShortReference == "" {
			return nil, errors.New("evidence link is malformed")
		}
		rows, err := corpus.Query(`
			select distinct canonical_archive_record_reference
			from archive_documents
			where registered_trawler = ? and local_short_reference = ?`,
			registeredTrawler,
			localShortReference,
		)
		if err != nil {
			return nil, err
		}
		var canonicalReferences []string
		for rows.Next() {
			var canonicalReference string
			if err := rows.Scan(&canonicalReference); err != nil {
				_ = rows.Close()
				return nil, err
			}
			canonicalReferences = append(canonicalReferences, canonicalReference)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if len(canonicalReferences) != 1 {
			return nil, errors.New("evidence link does not identify one canonical record")
		}
		records[canonicalArchiveRecordIdentity{
			registeredTrawler:               registeredTrawler,
			canonicalArchiveRecordReference: canonicalReferences[0],
		}] = struct{}{}
	}
	return records, nil
}

func validateConstrainedSupportingEvidenceEligibility(queries []resolvedRetrievalEvaluationQuery, corpus *sql.DB) error {
	for _, query := range queries {
		for identity := range query.acceptableSupportingRecords {
			queryText := `
				select count(*) from archive_documents document
				where registered_trawler = ? and canonical_archive_record_reference = ?`
			arguments := []any{identity.registeredTrawler, identity.canonicalArchiveRecordReference}
			queryText, arguments = addRetrievalConstraints(queryText, arguments, query.SuppliedConstraints)
			var eligibleDocumentCount int
			if err := corpus.QueryRow(queryText, arguments...).Scan(&eligibleDocumentCount); err != nil {
				return err
			}
			if eligibleDocumentCount == 0 {
				return fmt.Errorf("supporting evidence for query %s falls outside its own constraints", query.Identifier)
			}
		}
	}
	return nil
}

func embedEvaluationQueries(ctx context.Context, queries []resolvedRetrievalEvaluationQuery, contract semanticVectorIndexContract, ollamaURL string, teiURL string) ([][]float32, string, error) {
	inputs := make([]string, len(queries))
	for index, query := range queries {
		inputs[index] = contract.queryPrefix + query.InformationNeed
	}
	switch contract.queryEmbeddingRuntime {
	case ollamaQueryEmbeddingRuntime:
		vectors, runtimeRevision, err := requestEvaluationOllamaEmbeddings(ctx, ollamaURL, contract, inputs)
		return vectors, runtimeRevision, err
	case teiQueryEmbeddingRuntime:
		vectors, runtimeRevision, err := requestEvaluationTEIEmbeddings(ctx, teiURL, contract, inputs)
		return vectors, runtimeRevision, err
	default:
		return nil, "", errors.New("semantic index uses an unsupported query embedding runtime")
	}
}

func requestEvaluationOllamaEmbeddings(ctx context.Context, endpoint string, contract semanticVectorIndexContract, inputs []string) ([][]float32, string, error) {
	requestBody := ollamaEmbeddingRequest{Model: contract.modelIdentifier, Input: inputs, Dimensions: contract.dimensions, KeepAlive: "30m", Truncate: true}
	requestBody.Options.ContextTokens = contract.maximumInputTokens
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(endpoint, "/")+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, "", fmt.Errorf("ollama returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded ollamaEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, "", err
	}
	if err := validateEvaluationEmbeddingVectors(decoded.Embeddings, len(inputs), contract.dimensions); err != nil {
		return nil, "", err
	}
	return decoded.Embeddings, contract.modelRevision, nil
}

func requestEvaluationTEIEmbeddings(ctx context.Context, endpoint string, contract semanticVectorIndexContract, inputs []string) ([][]float32, string, error) {
	runtimeInformationRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/")+"/info", nil)
	if err != nil {
		return nil, "", err
	}
	runtimeInformationResponse, err := http.DefaultClient.Do(runtimeInformationRequest)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = runtimeInformationResponse.Body.Close() }()
	if runtimeInformationResponse.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("text embeddings inference info returned HTTP %d", runtimeInformationResponse.StatusCode)
	}
	var runtimeInformation teiRuntimeInformation
	if err := json.NewDecoder(runtimeInformationResponse.Body).Decode(&runtimeInformation); err != nil {
		return nil, "", err
	}
	if runtimeInformation.ModelIdentifier != contract.modelIdentifier || runtimeInformation.MaximumLength != contract.maximumInputTokens || runtimeInformation.ModelType.Embedding.Pooling != contract.pooling {
		return nil, "", errors.New("text embeddings inference runtime does not match the semantic index contract")
	}
	vectors := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += 4 {
		end := min(start+4, len(inputs))
		requestBody := teiEmbeddingRequest{Inputs: inputs[start:end], Truncate: true, Normalize: true}
		payload, err := json.Marshal(requestBody)
		if err != nil {
			return nil, "", err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(endpoint, "/")+"/embed", bytes.NewReader(payload))
		if err != nil {
			return nil, "", err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, "", err
		}
		if response.StatusCode/100 != 2 {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			return nil, "", fmt.Errorf("text embeddings inference returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		var nativeVectors [][]float32
		if err := json.NewDecoder(response.Body).Decode(&nativeVectors); err != nil {
			_ = response.Body.Close()
			return nil, "", err
		}
		_ = response.Body.Close()
		for _, nativeVector := range nativeVectors {
			if len(nativeVector) < contract.dimensions {
				return nil, "", errors.New("text embeddings inference returned fewer dimensions than the index contract")
			}
			vector := append([]float32(nil), nativeVector[:contract.dimensions]...)
			normalizeFloat32Vector(vector)
			vectors = append(vectors, vector)
		}
	}
	if err := validateEvaluationEmbeddingVectors(vectors, len(inputs), contract.dimensions); err != nil {
		return nil, "", err
	}
	return vectors, runtimeInformation.ModelRevision, nil
}

func validateEvaluationEmbeddingVectors(vectors [][]float32, expectedCount int, expectedDimensions int) error {
	if len(vectors) != expectedCount {
		return errors.New("query embedding response contains the wrong vector count")
	}
	for _, vector := range vectors {
		if len(vector) != expectedDimensions {
			return errors.New("query embedding response contains the wrong dimensions")
		}
		if err := validateEmbeddingVector(vector); err != nil {
			return err
		}
	}
	return nil
}

func normalizeFloat32Vector(vector []float32) {
	var squaredLength float64
	for _, value := range vector {
		squaredLength += float64(value) * float64(value)
	}
	length := math.Sqrt(squaredLength)
	if length == 0 {
		return
	}
	for index := range vector {
		vector[index] = float32(float64(vector[index]) / length)
	}
}

func retrieveAllExactSemanticMatches(vectorDatabase *sql.DB, corpus *sql.DB, contract semanticVectorIndexContract, queryEmbeddings [][]float32, queries []resolvedRetrievalEvaluationQuery) (map[retrievalEvaluationLane][][]rankedCanonicalArchiveRecord, map[retrievalEvaluationLane][]int64, error) {
	var maximumChunksPerCanonicalRecord int
	if err := corpus.QueryRow(`
		select max(chunk_count) from (
			select count(*) chunk_count
			from archive_documents
			group by registered_trawler, canonical_archive_record_reference
		)`).Scan(&maximumChunksPerCanonicalRecord); err != nil {
		return nil, nil, err
	}
	retainedDocumentCountPerQuery := maximumRetrievedCanonicalRecordsPerQuery * maximumChunksPerCanonicalRecord
	lanes := []retrievalEvaluationLane{unfilteredRetrievalEvaluationLane, constrainedRetrievalEvaluationLane}
	heapsByLaneAndQuery := make(map[retrievalEvaluationLane][]*scoredArchiveDocumentHeap, len(lanes))
	eligibleRecordCountsByLaneAndQuery := make(map[retrievalEvaluationLane][]int64, len(lanes))
	for _, lane := range lanes {
		heapsByLaneAndQuery[lane] = make([]*scoredArchiveDocumentHeap, len(queries))
		eligibleRecordCountsByLaneAndQuery[lane] = make([]int64, len(queries))
		for queryNumber := range queries {
			documents := make(scoredArchiveDocumentHeap, 0, retainedDocumentCountPerQuery)
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
	defer func() { _ = vectorRows.Close() }()
	const documentMatrixBatchSize = 8192
	documentIdentifiers := make([]int64, 0, documentMatrixBatchSize)
	documentVectors := make([]float32, 0, documentMatrixBatchSize*contract.dimensions)
	canonicalRecordsCountedForEligibility := make(map[canonicalArchiveRecordIdentity]struct{}, contract.indexedDocumentCount)
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
			_, canonicalRecordAlreadyCounted := canonicalRecordsCountedForEligibility[record.identity]
			if !canonicalRecordAlreadyCounted {
				canonicalRecordsCountedForEligibility[record.identity] = struct{}{}
			}
			for queryNumber, query := range queries {
				record.cosineSimilarity = float64(scores[documentNumber*len(queries)+queryNumber])
				retainScoredArchiveDocument(heapsByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber], record, retainedDocumentCountPerQuery)
				if !canonicalRecordAlreadyCounted {
					eligibleRecordCountsByLaneAndQuery[unfilteredRetrievalEvaluationLane][queryNumber]++
				}
				if evaluationQueryAcceptsRecord(query.SuppliedConstraints, record) {
					retainScoredArchiveDocument(heapsByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber], record, retainedDocumentCountPerQuery)
					if !canonicalRecordAlreadyCounted {
						eligibleRecordCountsByLaneAndQuery[constrainedRetrievalEvaluationLane][queryNumber]++
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
	retrievedRecordsByLaneAndQuery := make(map[retrievalEvaluationLane][][]rankedCanonicalArchiveRecord, len(lanes))
	for _, lane := range lanes {
		retrievedRecordsByLaneAndQuery[lane] = make([][]rankedCanonicalArchiveRecord, len(queries))
		for queryNumber := range queries {
			rankedDocuments := append([]rankedCanonicalArchiveRecord(nil), *heapsByLaneAndQuery[lane][queryNumber]...)
			sort.Slice(rankedDocuments, func(left, right int) bool {
				if rankedDocuments[left].cosineSimilarity != rankedDocuments[right].cosineSimilarity {
					return rankedDocuments[left].cosineSimilarity > rankedDocuments[right].cosineSimilarity
				}
				return rankedDocuments[left].documentIdentifier < rankedDocuments[right].documentIdentifier
			})
			seenCanonicalRecords := make(map[canonicalArchiveRecordIdentity]struct{}, maximumRetrievedCanonicalRecordsPerQuery)
			for _, record := range rankedDocuments {
				if _, duplicate := seenCanonicalRecords[record.identity]; duplicate {
					continue
				}
				seenCanonicalRecords[record.identity] = struct{}{}
				retrievedRecordsByLaneAndQuery[lane][queryNumber] = append(retrievedRecordsByLaneAndQuery[lane][queryNumber], record)
				if len(retrievedRecordsByLaneAndQuery[lane][queryNumber]) == maximumRetrievedCanonicalRecordsPerQuery {
					break
				}
			}
		}
	}
	return retrievedRecordsByLaneAndQuery, eligibleRecordCountsByLaneAndQuery, nil
}

func readEvaluationDocumentMetadata(corpus *sql.DB, documentIdentifiers []int64) (map[int64]rankedCanonicalArchiveRecord, error) {
	placeholders := make([]string, len(documentIdentifiers))
	arguments := make([]any, len(documentIdentifiers))
	for index, documentIdentifier := range documentIdentifiers {
		placeholders[index] = "?"
		arguments[index] = documentIdentifier
	}
	rows, err := corpus.Query(`
		select document_identifier, registered_trawler,
		       canonical_archive_record_reference, local_short_reference, associated_time
		from archive_documents
		where document_identifier in (`+strings.Join(placeholders, ",")+")",
		arguments...,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := make(map[int64]rankedCanonicalArchiveRecord, len(documentIdentifiers))
	for rows.Next() {
		var record rankedCanonicalArchiveRecord
		if err := rows.Scan(
			&record.documentIdentifier,
			&record.identity.registeredTrawler,
			&record.identity.canonicalArchiveRecordReference,
			&record.localShortReference,
			&record.associatedTime,
		); err != nil {
			return nil, err
		}
		records[record.documentIdentifier] = record
	}
	return records, rows.Err()
}

func retainScoredArchiveDocument(documents *scoredArchiveDocumentHeap, document rankedCanonicalArchiveRecord, maximumDocuments int) {
	if documents.Len() < maximumDocuments {
		heap.Push(documents, document)
		return
	}
	if document.cosineSimilarity <= (*documents)[0].cosineSimilarity {
		return
	}
	(*documents)[0] = document
	heap.Fix(documents, 0)
}

func evaluationQueryAcceptsRecord(constraints retrievalQueryConstraints, record rankedCanonicalArchiveRecord) bool {
	if len(constraints.Sources) > 0 {
		acceptedSource := false
		for _, source := range constraints.Sources {
			if source == record.identity.registeredTrawler {
				acceptedSource = true
				break
			}
		}
		if !acceptedSource {
			return false
		}
	}
	associatedDate := record.associatedTime
	if len(associatedDate) > 10 {
		associatedDate = associatedDate[:10]
	}
	if constraints.After != "" && associatedDate < constraints.After {
		return false
	}
	if constraints.Before != "" && associatedDate > constraints.Before {
		return false
	}
	return true
}

func addRetrievalConstraints(queryText string, arguments []any, constraints retrievalQueryConstraints) (string, []any) {
	if len(constraints.Sources) > 0 {
		placeholders := make([]string, len(constraints.Sources))
		for index, source := range constraints.Sources {
			placeholders[index] = "?"
			arguments = append(arguments, source)
		}
		queryText += " and document.registered_trawler in (" + strings.Join(placeholders, ",") + ")"
	}
	if constraints.After != "" {
		queryText += " and substr(document.associated_time, 1, 10) >= ?"
		arguments = append(arguments, constraints.After)
	}
	if constraints.Before != "" {
		queryText += " and substr(document.associated_time, 1, 10) <= ?"
		arguments = append(arguments, constraints.Before)
	}
	return queryText, arguments
}

func calculateQueryEvaluationMetrics(query resolvedRetrievalEvaluationQuery, rankedRecords []rankedCanonicalArchiveRecord) queryEvaluationMetrics {
	metrics := queryEvaluationMetrics{}
	acceptableSources := make(map[string]struct{})
	retrievedAcceptableSources := make(map[string]struct{})
	for identity := range query.acceptableSupportingRecords {
		acceptableSources[identity.registeredTrawler] = struct{}{}
	}
	for index, record := range rankedRecords {
		rank := index + 1
		if _, acceptable := query.acceptableSupportingRecords[record.identity]; acceptable {
			if metrics.firstAcceptableRank == 0 {
				metrics.firstAcceptableRank = rank
			}
			if rank <= 5 {
				metrics.acceptableRecordsRetrievedAtFive++
			}
			metrics.acceptableRecordsRetrievedAtTwenty++
			retrievedAcceptableSources[record.identity.registeredTrawler] = struct{}{}
		}
		if _, weakening := query.contradictionOrWeakeningRecords[record.identity]; weakening {
			metrics.weakeningRecordsRetrievedAtTwenty++
		}
	}
	metrics.acceptableSources = len(acceptableSources)
	metrics.acceptableSourcesRetrievedAtTwenty = len(retrievedAcceptableSources)
	return metrics
}

func addQueryEvaluationMetrics(aggregate *retrievalAggregateMetrics, query resolvedRetrievalEvaluationQuery, metrics queryEvaluationMetrics) {
	aggregate.queryCount++
	if metrics.firstAcceptableRank > 0 {
		aggregate.reciprocalRankTotal += 1 / float64(metrics.firstAcceptableRank)
		if metrics.firstAcceptableRank <= 5 {
			aggregate.queriesWithAcceptableRecordAtFive++
		}
		if metrics.firstAcceptableRank <= 20 {
			aggregate.queriesWithAcceptableRecordAtTwenty++
		}
	}
	aggregate.acceptableRecordLabels += len(query.acceptableSupportingRecords)
	aggregate.acceptableRecordLabelsRetrieved += metrics.acceptableRecordsRetrievedAtTwenty
	aggregate.weakeningRecordLabels += len(query.contradictionOrWeakeningRecords)
	aggregate.weakeningRecordLabelsRetrieved += metrics.weakeningRecordsRetrievedAtTwenty
	aggregate.acceptableSourceLabels += metrics.acceptableSources
	aggregate.acceptableSourceLabelsRetrieved += metrics.acceptableSourcesRetrievedAtTwenty
}

func ratio(numerator int, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func createRetrievalEvaluationResultDatabase(resultPath string, contract semanticVectorIndexContract, corpusSHA256 string, manifestSHA256 string, querySHA256 string, vectorSHA256 string, documentCount int64, embeddingRuntimeVersion string) (*sql.DB, error) {
	if err := os.MkdirAll(filepathDirectory(resultPath), 0o700); err != nil {
		return nil, err
	}
	resultDatabase, err := sql.Open("sqlite3", resultPath)
	if err != nil {
		return nil, err
	}
	resultDatabase.SetMaxOpenConns(1)
	if err := resultDatabase.Ping(); err != nil {
		_ = resultDatabase.Close()
		return nil, err
	}
	if err := os.Chmod(resultPath, 0o600); err != nil {
		_ = resultDatabase.Close()
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
			cosine_similarity real not null,
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
			primary key(lane, query_identifier)
		);
	`); err != nil {
		_ = resultDatabase.Close()
		return nil, err
	}
	if _, err := resultDatabase.Exec(`
		insert into evaluation_metadata values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
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
		maximumRetrievedCanonicalRecordsPerQuery,
		"inclusive comparison of substr(associated_time, 1, 10) against YYYY-MM-DD bounds",
	); err != nil {
		_ = resultDatabase.Close()
		return nil, err
	}
	return resultDatabase, nil
}

func storeRetrievalEvaluationResult(resultDatabase *sql.DB, lane retrievalEvaluationLane, query resolvedRetrievalEvaluationQuery, rankedRecords []rankedCanonicalArchiveRecord, eligibleCanonicalRecordCount int64, metrics queryEvaluationMetrics) error {
	transaction, err := resultDatabase.Begin()
	if err != nil {
		return err
	}
	for index, record := range rankedRecords {
		if _, err := transaction.Exec(`
			insert into retrieved_canonical_records values (?, ?, ?, ?, ?, ?, ?, ?)`,
			lane,
			query.Identifier,
			index+1,
			record.documentIdentifier,
			record.identity.registeredTrawler,
			record.identity.canonicalArchiveRecordReference,
			record.localShortReference,
			record.cosineSimilarity,
		); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	if _, err := transaction.Exec(`
		insert into query_metrics values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
	); err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}

func filepathDirectory(path string) string {
	lastSeparator := strings.LastIndex(path, string(os.PathSeparator))
	if lastSeparator < 0 {
		return "."
	}
	return path[:lastSeparator]
}
