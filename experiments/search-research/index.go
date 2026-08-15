package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type passageEmbeddingInput struct {
	passageIdentifier int64
	modelInput        string
}

type completedPassageEmbeddingBatch struct {
	passages         []passageEmbeddingInput
	embeddings       [][]float32
	promptTokenCount int64
	inferenceElapsed time.Duration
	err              error
}

func buildSearchResearchEmbeddingIndex(arguments []string, output io.Writer, progressOutput io.Writer) (returnedError error) {
	flags := flag.NewFlagSet("experiment index build", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	indexPath := flags.String("index", "", "private embedding index database")
	modelArtifactName := flags.String("model-artifact-name", "", "runtime model artifact name")
	modelContractSHA256 := flags.String("model-contract-sha256", "", "embedding model contract SHA-256")
	runtimeModelDigest := flags.String("runtime-model-digest", "", "runtime model manifest digest")
	runtimeLoadedModelBytes := flags.Int64("runtime-loaded-model-bytes", 0, "runtime loaded model bytes")
	runtimeName := flags.String("runtime-name", "ollama", "embedding runtime name")
	runtimeVersion := flags.String("runtime-version", "", "embedding runtime version")
	runtimeEndpoint := flags.String("runtime-endpoint", "http://127.0.0.1:11434", "embedding runtime endpoint")
	documentInputPrefix := flags.String("document-input-prefix", "", "official document input prefix")
	queryInputPrefix := flags.String("query-input-prefix", "", "official query input prefix")
	maximumInputTokens := flags.Int("maximum-input-tokens", 0, "runtime input token limit")
	nativeEmbeddingDimensions := flags.Int("native-dimensions", 0, "runtime embedding dimensions")
	storedEmbeddingDimensions := flags.Int("stored-dimensions", 0, "stored embedding dimensions")
	storedPrecision := flags.String("stored-precision", "float32", "stored vector precision")
	maximumConcurrentRequests := flags.Int("maximum-concurrent-requests", 1, "bounded concurrent embedding requests")
	maximumBatchPassages := flags.Int("maximum-batch-passages", 32, "passages in one embedding request")
	runtimeRootProcessIdentifier := flags.Int("runtime-process-id", 0, "root process of the isolated embedding runtime")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	configuration := embeddingDeploymentConfiguration{
		modelArtifactName:         strings.TrimSpace(*modelArtifactName),
		modelContractSHA256:       strings.TrimSpace(*modelContractSHA256),
		runtimeModelDigest:        strings.TrimSpace(*runtimeModelDigest),
		runtimeLoadedModelBytes:   *runtimeLoadedModelBytes,
		runtimeName:               strings.TrimSpace(*runtimeName),
		runtimeVersion:            strings.TrimSpace(*runtimeVersion),
		runtimeEndpoint:           strings.TrimSpace(*runtimeEndpoint),
		documentInputPrefix:       *documentInputPrefix,
		queryInputPrefix:          *queryInputPrefix,
		maximumInputTokens:        *maximumInputTokens,
		nativeEmbeddingDimensions: *nativeEmbeddingDimensions,
		storedEmbeddingDimensions: *storedEmbeddingDimensions,
		storedEmbeddingPrecision:  embeddingPrecision(*storedPrecision),
	}
	if err := validateEmbeddingDeploymentConfiguration(configuration); err != nil {
		return err
	}
	if *corpusPath == "" || *indexPath == "" || *maximumConcurrentRequests <= 0 || *maximumBatchPassages <= 0 || *runtimeRootProcessIdentifier <= 0 {
		return errors.New("--corpus, --index, --runtime-process-id and positive concurrency and batch limits are required")
	}
	if err := verifyOllamaEmbeddingDeployment(context.Background(), configuration); err != nil {
		return err
	}
	corpusDatabase, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, corpusDatabase.Close()) }()
	if _, err := verifySearchResearchSourceSnapshot(corpusDatabase); err != nil {
		return err
	}
	var contentSHA256 string
	var corpusPassageCount int64
	if err := corpusDatabase.QueryRow(`select passage_corpus_sha256 from corpus_metadata where singleton = 1`).Scan(&contentSHA256); err != nil {
		return err
	}
	if contentSHA256 == "" {
		return errors.New("corpus does not have a content hash")
	}
	if err := corpusDatabase.QueryRow(`select count(*) from searchable_passages`).Scan(&corpusPassageCount); err != nil {
		return err
	}
	indexDatabase, err := sql.Open("sqlite3", *indexPath)
	if err != nil {
		return err
	}
	defer func() { returnedError = errors.Join(returnedError, indexDatabase.Close()) }()
	indexDatabase.SetMaxOpenConns(1)
	if err := initializeEmbeddingIndex(indexDatabase, configuration, corpusContentSHA256(contentSHA256), corpusPassageCount); err != nil {
		return err
	}
	if err := os.Chmod(*indexPath, 0o600); err != nil {
		return err
	}
	runIdentifier, measurement, err := startExperimentRun(indexDatabase, "embedding_index_build", configuration.runtimeLoadedModelBytes, *runtimeRootProcessIdentifier)
	if err != nil {
		return err
	}
	memorySampler := startProcessMemorySampler(*runtimeRootProcessIdentifier)
	defer func() {
		memoryMeasurement := memorySampler.finish()
		measurement.baselineHarnessProcessMemoryBytes = memoryMeasurement.baselineHarnessProcessMemoryBytes
		measurement.baselineRuntimeProcessMemoryBytes = memoryMeasurement.baselineRuntimeProcessMemoryBytes
		measurement.peakProcessMemoryBytes = memoryMeasurement.peakHarnessAndRuntimeMemoryBytes
		measurement.steadyProcessMemoryBytes = memoryMeasurement.steadyHarnessAndRuntimeMemoryBytes
		measurement.indexBytes = sqliteDatabaseFileSetSize(*indexPath)
		if returnedError != nil {
			measurement.failureMessage = returnedError.Error()
			if measurement.failureStage == "" {
				measurement.failureStage = embeddingRequestFailure
			}
		}
		returnedError = errors.Join(returnedError, finishExperimentRun(indexDatabase, runIdentifier, measurement))
	}()
	var lastCommittedPassageIdentifier, indexedPassageCount, totalPromptTokens, totalInferenceNanoseconds, totalStorageNanoseconds int64
	if err := indexDatabase.QueryRow(`
		select last_committed_searchable_passage_identifier, indexed_passage_count,
		       prompt_tokens, inference_nanoseconds, storage_nanoseconds
		from embedding_index_checkpoint where singleton = 1`).Scan(
		&lastCommittedPassageIdentifier, &indexedPassageCount, &totalPromptTokens,
		&totalInferenceNanoseconds, &totalStorageNanoseconds,
	); err != nil {
		return err
	}
	startingPromptTokens := totalPromptTokens
	startingInferenceNanoseconds := totalInferenceNanoseconds
	startingStorageNanoseconds := totalStorageNanoseconds
	lastProgressAt := time.Now()
	for indexedPassageCount < corpusPassageCount {
		passages, err := readEmbeddingPassageWindow(
			corpusDatabase,
			lastCommittedPassageIdentifier,
			*maximumConcurrentRequests**maximumBatchPassages,
			configuration.documentInputPrefix,
		)
		if err != nil {
			return err
		}
		if len(passages) == 0 {
			return errors.New("embedding checkpoint does not match corpus passages")
		}
		completedBatches, err := embedPassageWindow(
			context.Background(), configuration, passages,
			*maximumConcurrentRequests, *maximumBatchPassages,
		)
		if err != nil {
			measurement.failureStage = embeddingRequestFailure
			return err
		}
		storageStartedAt := time.Now()
		lastCommittedPassageIdentifier, err = storeCompletedPassageEmbeddingBatches(
			indexDatabase, completedBatches, indexedPassageCount,
			totalPromptTokens, totalInferenceNanoseconds, totalStorageNanoseconds,
		)
		storageElapsed := time.Since(storageStartedAt)
		if err != nil {
			measurement.failureStage = vectorStorageFailure
			return err
		}
		for _, batch := range completedBatches {
			indexedPassageCount += int64(len(batch.passages))
			totalPromptTokens += batch.promptTokenCount
			totalInferenceNanoseconds += batch.inferenceElapsed.Nanoseconds()
		}
		totalStorageNanoseconds += storageElapsed.Nanoseconds()
		if _, err := indexDatabase.Exec(`
			update embedding_index_checkpoint set
				indexed_passage_count = ?, last_committed_searchable_passage_identifier = ?,
				prompt_tokens = ?, inference_nanoseconds = ?, storage_nanoseconds = ?,
				completed_at = case when ? = corpus_passage_count then strftime('%Y-%m-%dT%H:%M:%fZ', 'now') else null end
			where singleton = 1`, indexedPassageCount, lastCommittedPassageIdentifier,
			totalPromptTokens, totalInferenceNanoseconds, totalStorageNanoseconds, indexedPassageCount); err != nil {
			measurement.failureStage = vectorStorageFailure
			return err
		}
		if time.Since(lastProgressAt) >= 10*time.Second {
			_, _ = fmt.Fprintf(progressOutput, "indexed_passages=%d/%d index_bytes=%d\n", indexedPassageCount, corpusPassageCount, sqliteDatabaseFileSetSize(*indexPath))
			lastProgressAt = time.Now()
		}
	}
	measurement.promptTokens = totalPromptTokens - startingPromptTokens
	measurement.inferenceElapsed = time.Duration(totalInferenceNanoseconds - startingInferenceNanoseconds)
	measurement.storageElapsed = time.Duration(totalStorageNanoseconds - startingStorageNanoseconds)
	_, err = fmt.Fprintf(output, "deployment_sha256=%s indexed_passages=%d prompt_tokens=%d inference=%s storage=%s index_bytes=%d\n",
		embeddingDeploymentConfigurationSHA256(configuration), indexedPassageCount, totalPromptTokens,
		time.Duration(totalInferenceNanoseconds).Round(time.Millisecond),
		time.Duration(totalStorageNanoseconds).Round(time.Millisecond), sqliteDatabaseFileSetSize(*indexPath))
	return err
}

func validateEmbeddingDeploymentConfiguration(configuration embeddingDeploymentConfiguration) error {
	if configuration.modelArtifactName == "" || configuration.modelContractSHA256 == "" ||
		configuration.runtimeModelDigest == "" || configuration.runtimeLoadedModelBytes <= 0 || configuration.runtimeName == "" ||
		configuration.runtimeVersion == "" || configuration.runtimeEndpoint == "" ||
		configuration.maximumInputTokens <= 0 || configuration.nativeEmbeddingDimensions <= 0 ||
		configuration.storedEmbeddingDimensions <= 0 ||
		configuration.storedEmbeddingDimensions > configuration.nativeEmbeddingDimensions {
		return errors.New("the embedding deployment needs artifact, runtime, formatting, token limit, dimensions and precision")
	}
	if configuration.storedEmbeddingPrecision != float32EmbeddingPrecision {
		return fmt.Errorf("stored precision %q is unsupported", configuration.storedEmbeddingPrecision)
	}
	return nil
}

func embeddingDeploymentConfigurationSHA256(configuration embeddingDeploymentConfiguration) embeddingDeploymentSHA256 {
	hash := sha256.New()
	for _, field := range []string{
		configuration.modelArtifactName, configuration.modelContractSHA256,
		configuration.runtimeModelDigest, fmt.Sprintf("%d", configuration.runtimeLoadedModelBytes),
		configuration.runtimeName,
		configuration.runtimeVersion, configuration.runtimeEndpoint,
		configuration.documentInputPrefix, configuration.queryInputPrefix,
		fmt.Sprintf("%d", configuration.maximumInputTokens),
		fmt.Sprintf("%d", configuration.nativeEmbeddingDimensions),
		fmt.Sprintf("%d", configuration.storedEmbeddingDimensions),
		string(configuration.storedEmbeddingPrecision),
	} {
		_, _ = fmt.Fprintf(hash, "%d:%s", len(field), field)
	}
	return embeddingDeploymentSHA256(hex.EncodeToString(hash.Sum(nil)))
}

func initializeEmbeddingIndex(
	database *sql.DB,
	configuration embeddingDeploymentConfiguration,
	contentSHA256 corpusContentSHA256,
	corpusPassageCount int64,
) error {
	if err := initializeExperimentRunSchema(database); err != nil {
		return err
	}
	var metadataTableCount int
	if err := database.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'embedding_deployment'`).Scan(&metadataTableCount); err != nil {
		return err
	}
	if metadataTableCount == 0 {
		if _, err := database.Exec(`pragma journal_mode = wal; pragma synchronous = normal;`); err != nil {
			return err
		}
		transaction, err := database.Begin()
		if err != nil {
			return err
		}
		if _, err := transaction.Exec(`
			create table embedding_deployment (
				singleton integer primary key check (singleton = 1),
				embedding_deployment_sha256 text not null,
				model_artifact_name text not null,
				model_contract_sha256 text not null,
				runtime_model_digest text not null,
				runtime_loaded_model_bytes integer not null,
				runtime_name text not null,
				runtime_version text not null,
				runtime_endpoint text not null,
				document_input_prefix text not null,
				query_input_prefix text not null,
				maximum_input_tokens integer not null,
				native_embedding_dimensions integer not null,
				stored_embedding_dimensions integer not null,
				stored_embedding_precision text not null,
				maximum_searchable_passage_content_utf8_bytes integer not null,
				corpus_content_sha256 text not null
			);
			create table embedding_index_checkpoint (
				singleton integer primary key check (singleton = 1),
				corpus_passage_count integer not null,
				indexed_passage_count integer not null,
				last_committed_searchable_passage_identifier integer not null,
				prompt_tokens integer not null,
				inference_nanoseconds integer not null,
				storage_nanoseconds integer not null,
				started_at text not null,
				completed_at text
			);
			create table searchable_passage_embeddings (
				searchable_passage_identifier integer primary key,
				embedding_float32_blob blob not null
			);`); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if _, err := transaction.Exec(`insert into embedding_deployment values (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			embeddingDeploymentConfigurationSHA256(configuration), configuration.modelArtifactName,
			configuration.modelContractSHA256, configuration.runtimeModelDigest,
			configuration.runtimeLoadedModelBytes, configuration.runtimeName,
			configuration.runtimeVersion, configuration.runtimeEndpoint, configuration.documentInputPrefix,
			configuration.queryInputPrefix, configuration.maximumInputTokens,
			configuration.nativeEmbeddingDimensions, configuration.storedEmbeddingDimensions,
			configuration.storedEmbeddingPrecision, maximumSearchablePassageContentUTF8Bytes, contentSHA256); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if _, err := transaction.Exec(`insert into embedding_index_checkpoint values (1, ?, 0, 0, 0, 0, 0, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), null)`, corpusPassageCount); err != nil {
			_ = transaction.Rollback()
			return err
		}
		return transaction.Commit()
	}
	existingConfiguration, existingCorpusSHA256, err := readEmbeddingDeploymentConfiguration(database)
	if err != nil {
		return err
	}
	if embeddingDeploymentConfigurationSHA256(existingConfiguration) != embeddingDeploymentConfigurationSHA256(configuration) || existingCorpusSHA256 != contentSHA256 {
		return errors.New("existing index uses a different corpus or embedding deployment configuration")
	}
	var existingCorpusPassageCount int64
	if err := database.QueryRow(`select corpus_passage_count from embedding_index_checkpoint where singleton = 1`).Scan(&existingCorpusPassageCount); err != nil {
		return err
	}
	if existingCorpusPassageCount != corpusPassageCount {
		return errors.New("existing index corpus passage count does not match")
	}
	return nil
}

func readEmbeddingDeploymentConfiguration(database *sql.DB) (embeddingDeploymentConfiguration, corpusContentSHA256, error) {
	var configuration embeddingDeploymentConfiguration
	var precision string
	var storedMaximumPassageBytes int
	var contentSHA256 string
	err := database.QueryRow(`
		select model_artifact_name, model_contract_sha256, runtime_model_digest,
		       runtime_loaded_model_bytes,
		       runtime_name, runtime_version, runtime_endpoint, document_input_prefix,
		       query_input_prefix, maximum_input_tokens, native_embedding_dimensions,
		       stored_embedding_dimensions,
		       stored_embedding_precision, maximum_searchable_passage_content_utf8_bytes,
		       corpus_content_sha256
		from embedding_deployment where singleton = 1`).Scan(
		&configuration.modelArtifactName, &configuration.modelContractSHA256,
		&configuration.runtimeModelDigest, &configuration.runtimeLoadedModelBytes,
		&configuration.runtimeName, &configuration.runtimeVersion, &configuration.runtimeEndpoint,
		&configuration.documentInputPrefix, &configuration.queryInputPrefix,
		&configuration.maximumInputTokens, &configuration.nativeEmbeddingDimensions,
		&configuration.storedEmbeddingDimensions,
		&precision, &storedMaximumPassageBytes, &contentSHA256,
	)
	configuration.storedEmbeddingPrecision = embeddingPrecision(precision)
	if err == nil && storedMaximumPassageBytes != maximumSearchablePassageContentUTF8Bytes {
		err = errors.New("index uses a different searchable passage contract")
	}
	return configuration, corpusContentSHA256(contentSHA256), err
}

func readEmbeddingPassageWindow(database *sql.DB, afterPassageIdentifier int64, limit int, documentPrefix string) ([]passageEmbeddingInput, error) {
	rows, err := database.Query(`
		select passage.searchable_passage_identifier,
		       section.searchable_record_text_section_name, passage.searchable_passage_content
		from searchable_passages passage
		join searchable_record_text_sections section using (searchable_record_text_section_identifier)
		join archive_records record using (archive_record_identifier)
		where passage.searchable_passage_identifier > ?
		order by passage.searchable_passage_identifier
		limit ?`, afterPassageIdentifier, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	passages := make([]passageEmbeddingInput, 0, limit)
	for rows.Next() {
		var passage passageEmbeddingInput
		var sectionName, content string
		if err := rows.Scan(&passage.passageIdentifier, &sectionName, &content); err != nil {
			return nil, err
		}
		passage.modelInput = documentPrefix + sectionName + ":\n" + content
		passages = append(passages, passage)
	}
	return passages, rows.Err()
}

func embedPassageWindow(
	ctx context.Context,
	configuration embeddingDeploymentConfiguration,
	passages []passageEmbeddingInput,
	maximumConcurrentRequests int,
	maximumBatchPassages int,
) ([]completedPassageEmbeddingBatch, error) {
	batchCount := (len(passages) + maximumBatchPassages - 1) / maximumBatchPassages
	completed := make(chan completedPassageEmbeddingBatch, batchCount)
	semaphore := make(chan struct{}, maximumConcurrentRequests)
	var waitGroup sync.WaitGroup
	for start := 0; start < len(passages); start += maximumBatchPassages {
		end := min(start+maximumBatchPassages, len(passages))
		batchPassages := passages[start:end]
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			inputs := make([]string, len(batchPassages))
			for index := range batchPassages {
				inputs[index] = batchPassages[index].modelInput
			}
			response, inferenceElapsed, err := requestOllamaEmbeddings(ctx, configuration, inputs)
			storedEmbeddings := make([][]float32, len(response.Embeddings))
			if err == nil && len(response.Embeddings) != len(batchPassages) {
				err = fmt.Errorf("runtime returned %d embeddings for %d passages", len(response.Embeddings), len(batchPassages))
			}
			if err == nil {
				for embeddingIndex, embedding := range response.Embeddings {
					if vectorError := validateRuntimeEmbedding(embedding, configuration.nativeEmbeddingDimensions); vectorError != nil {
						err = vectorError
						break
					}
					storedEmbeddings[embeddingIndex], err = normalizeAndTruncateRuntimeEmbedding(
						embedding,
						configuration.storedEmbeddingDimensions,
					)
					if err != nil {
						break
					}
				}
			}
			completed <- completedPassageEmbeddingBatch{
				passages: batchPassages, embeddings: storedEmbeddings,
				promptTokenCount: response.PromptTokenCount,
				inferenceElapsed: inferenceElapsed, err: err,
			}
		}()
	}
	waitGroup.Wait()
	close(completed)
	completedBatches := make([]completedPassageEmbeddingBatch, 0, batchCount)
	for batch := range completed {
		if batch.err != nil {
			return nil, batch.err
		}
		completedBatches = append(completedBatches, batch)
	}
	sort.Slice(completedBatches, func(left, right int) bool {
		return completedBatches[left].passages[0].passageIdentifier < completedBatches[right].passages[0].passageIdentifier
	})
	return completedBatches, nil
}

func storeCompletedPassageEmbeddingBatches(
	database *sql.DB,
	completedBatches []completedPassageEmbeddingBatch,
	indexedPassageCount int64,
	promptTokens int64,
	inferenceNanoseconds int64,
	storageNanoseconds int64,
) (int64, error) {
	transaction, err := database.Begin()
	if err != nil {
		return 0, err
	}
	insert, err := transaction.Prepare(`insert into searchable_passage_embeddings(searchable_passage_identifier, embedding_float32_blob) values (?, ?)`)
	if err != nil {
		_ = transaction.Rollback()
		return 0, err
	}
	defer func() { _ = insert.Close() }()
	lastPassageIdentifier := int64(0)
	for _, batch := range completedBatches {
		promptTokens += batch.promptTokenCount
		inferenceNanoseconds += batch.inferenceElapsed.Nanoseconds()
		for index, passage := range batch.passages {
			if _, err := insert.Exec(passage.passageIdentifier, encodeFloat32Embedding(batch.embeddings[index])); err != nil {
				_ = transaction.Rollback()
				return 0, err
			}
			indexedPassageCount++
			lastPassageIdentifier = passage.passageIdentifier
		}
	}
	if _, err := transaction.Exec(`
		update embedding_index_checkpoint set indexed_passage_count = ?,
		       last_committed_searchable_passage_identifier = ?, prompt_tokens = ?,
		       inference_nanoseconds = ?, storage_nanoseconds = ?
		where singleton = 1`, indexedPassageCount, lastPassageIdentifier,
		promptTokens, inferenceNanoseconds, storageNanoseconds); err != nil {
		_ = transaction.Rollback()
		return 0, err
	}
	return lastPassageIdentifier, transaction.Commit()
}

func validateRuntimeEmbedding(embedding []float64, expectedDimensions int) error {
	if len(embedding) != expectedDimensions {
		return fmt.Errorf("runtime returned %d embedding dimensions; expected %d", len(embedding), expectedDimensions)
	}
	var squaredNorm float64
	for _, component := range embedding {
		if math.IsNaN(component) || math.IsInf(component, 0) {
			return errors.New("runtime returned a non-finite embedding")
		}
		squaredNorm += component * component
	}
	if squaredNorm == 0 {
		return errors.New("runtime returned a zero embedding")
	}
	return nil
}

func normalizeAndTruncateRuntimeEmbedding(runtimeEmbedding []float64, storedDimensions int) ([]float32, error) {
	if storedDimensions <= 0 || storedDimensions > len(runtimeEmbedding) {
		return nil, errors.New("stored embedding dimensions do not fit the runtime embedding")
	}
	var squaredNorm float64
	for _, component := range runtimeEmbedding[:storedDimensions] {
		squaredNorm += component * component
	}
	norm := math.Sqrt(squaredNorm)
	if norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("stored embedding prefix has a zero or non-finite norm")
	}
	storedEmbedding := make([]float32, storedDimensions)
	for componentIndex, component := range runtimeEmbedding[:storedDimensions] {
		storedEmbedding[componentIndex] = float32(component / norm)
	}
	return storedEmbedding, nil
}

func encodeFloat32Embedding(embedding []float32) []byte {
	encoded := make([]byte, len(embedding)*4)
	for index, component := range embedding {
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(component))
	}
	return encoded
}

func sqliteDatabaseFileSetSize(databasePath string) int64 {
	return fileSize(databasePath) + fileSize(databasePath+"-wal") + fileSize(databasePath+"-shm")
}
