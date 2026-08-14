package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

const embeddingIndexTransactionDocuments = 4096

type embeddingModelDefinition struct {
	name                                string
	manifestSHA256                      string
	documentPrefix                      string
	queryPrefix                         string
	maximumBatchDocuments               int
	maximumBatchCharacters              int
	maximumOutstandingEmbeddingRequests int
	nativeDimensions                    int
	storedDimensions                    int
}

type embeddingDocument struct {
	documentIdentifier int64
	registeredTrawler  string
	searchableText     string
}

type embeddedDocument struct {
	embeddingDocument
	embedding []float32
}

type embeddedDocumentBatch struct {
	documents        []embeddedDocument
	promptTokenCount int64
	embeddingElapsed time.Duration
}

type embeddingDocumentBatchCompletion struct {
	batch embeddedDocumentBatch
	err   error
}

type embeddingIndexBuildMeasurement struct {
	startingDocumentCount            int64
	startingPromptTokens             int64
	startingModelEmbeddingElapsed    time.Duration
	startingVectorTransactionElapsed time.Duration
	indexedDocumentCount             int64
	promptTokenCount                 int64
	modelEmbeddingElapsed            time.Duration
	vectorTransactionElapsed         time.Duration
	totalElapsed                     time.Duration
	resumed                          bool
}

type embeddingIndexCheckpoint struct {
	dimensions                      int
	indexedDocumentCount            int64
	lastCommittedDocumentIdentifier int64
	promptTokenCount                int64
	modelEmbeddingElapsed           time.Duration
	vectorTransactionElapsed        time.Duration
}

func buildEmbeddingIndex(ctx context.Context, corpusPath string, vectorPath string, model embeddingModelDefinition, progressOutput io.Writer) (embeddingIndexBuildMeasurement, error) {
	startedAt := time.Now()
	var measurement embeddingIndexBuildMeasurement
	corpusSHA256, err := sha256File(corpusPath)
	if err != nil {
		return measurement, fmt.Errorf("fingerprint corpus: %w", err)
	}
	corpus, err := sql.Open("sqlite3", "file:"+corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return measurement, err
	}
	defer func() { _ = corpus.Close() }()
	var corpusDocumentCount int64
	if err := corpus.QueryRowContext(ctx, `select count(*) from archive_documents`).Scan(&corpusDocumentCount); err != nil {
		return measurement, err
	}
	vectorDatabaseAlreadyExists := false
	if _, err := os.Stat(vectorPath); err == nil {
		vectorDatabaseAlreadyExists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return measurement, err
	}
	if err := os.MkdirAll(filepath.Dir(vectorPath), 0o700); err != nil {
		return measurement, err
	}
	vectorDatabase, err := sql.Open("sqlite3", vectorPath)
	if err != nil {
		return measurement, err
	}
	defer func() { _ = vectorDatabase.Close() }()
	vectorDatabase.SetMaxOpenConns(1)
	if err := vectorDatabase.PingContext(ctx); err != nil {
		return measurement, err
	}
	if err := os.Chmod(vectorPath, 0o600); err != nil {
		return measurement, err
	}
	checkpoint, err := readEmbeddingIndexCheckpoint(ctx, vectorDatabase, vectorDatabaseAlreadyExists, model, corpusSHA256, corpusDocumentCount)
	if err != nil {
		return measurement, err
	}
	measurement.startingDocumentCount = checkpoint.indexedDocumentCount
	measurement.startingPromptTokens = checkpoint.promptTokenCount
	measurement.indexedDocumentCount = checkpoint.indexedDocumentCount
	measurement.promptTokenCount = checkpoint.promptTokenCount
	measurement.modelEmbeddingElapsed = checkpoint.modelEmbeddingElapsed
	measurement.vectorTransactionElapsed = checkpoint.vectorTransactionElapsed
	measurement.startingModelEmbeddingElapsed = checkpoint.modelEmbeddingElapsed
	measurement.startingVectorTransactionElapsed = checkpoint.vectorTransactionElapsed
	measurement.resumed = checkpoint.indexedDocumentCount > 0
	if checkpoint.indexedDocumentCount == corpusDocumentCount {
		measurement.indexedDocumentCount = corpusDocumentCount
		measurement.totalElapsed = time.Since(startedAt)
		return measurement, verifyCompleteEmbeddingIndex(ctx, vectorDatabase, corpusDocumentCount)
	}
	rows, err := corpus.QueryContext(ctx, `
		select document_identifier, registered_trawler, searchable_text
		from archive_documents
		where document_identifier > ?
		order by document_identifier`, checkpoint.lastCommittedDocumentIdentifier)
	if err != nil {
		return measurement, err
	}
	defer func() { _ = rows.Close() }()

	expectedDimensions := model.storedDimensions
	if expectedDimensions == 0 {
		expectedDimensions = model.nativeDimensions
	}
	if expectedDimensions <= 0 {
		return measurement, errors.New("embedding model does not define its output dimensions")
	}
	if model.maximumOutstandingEmbeddingRequests < 1 || model.maximumOutstandingEmbeddingRequests > 2 {
		return measurement, errors.New("embedding model must allow one or two outstanding embedding requests")
	}
	if checkpoint.dimensions == 0 {
		if err := createEmbeddingIndexSchema(ctx, vectorDatabase, model, expectedDimensions, corpusSHA256, corpusDocumentCount); err != nil {
			return measurement, err
		}
	} else if checkpoint.dimensions != expectedDimensions {
		return measurement, fmt.Errorf("index contains %d dimensions, requested %d", checkpoint.dimensions, expectedDimensions)
	}

	embeddedBatches := make(chan embeddedDocumentBatch, 2)
	writerCompleted := make(chan embeddingIndexBuildMeasurement, 1)
	writerFailed := make(chan error, 1)
	go writeEmbeddedDocumentBatches(ctx, vectorDatabase, vectorPath, model.name, progressOutput, embeddedBatches, measurement, corpusDocumentCount, writerCompleted, writerFailed)
	embeddedAnyDocuments := false
	for {
		firstBatch, err := readNextEmbeddingDocumentBatch(rows, model)
		if err != nil {
			close(embeddedBatches)
			return measurement, err
		}
		if len(firstBatch) == 0 {
			if !embeddedAnyDocuments {
				close(embeddedBatches)
				return measurement, errors.New("embedding checkpoint does not match the remaining corpus")
			}
			break
		}
		var secondBatch []embeddingDocument
		if model.maximumOutstandingEmbeddingRequests == 2 {
			secondBatch, err = readNextEmbeddingDocumentBatch(rows, model)
			if err != nil {
				close(embeddedBatches)
				return measurement, err
			}
		}
		embeddedBatchPair, err := embedDocumentBatchPair(ctx, model, firstBatch, secondBatch)
		if err != nil {
			close(embeddedBatches)
			return measurement, err
		}
		for _, embeddedBatch := range embeddedBatchPair {
			if len(embeddedBatch.documents) == 0 {
				continue
			}
			actualDimensions := len(embeddedBatch.documents[0].embedding)
			if actualDimensions != expectedDimensions {
				close(embeddedBatches)
				return measurement, fmt.Errorf("model returned %d dimensions, expected %d", actualDimensions, expectedDimensions)
			}
			if err := sendEmbeddedBatch(ctx, embeddedBatches, writerFailed, embeddedBatch); err != nil {
				close(embeddedBatches)
				return measurement, err
			}
		}
		embeddedAnyDocuments = true
		if model.maximumOutstandingEmbeddingRequests == 2 && len(secondBatch) == 0 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		close(embeddedBatches)
		return measurement, err
	}
	close(embeddedBatches)
	select {
	case err := <-writerFailed:
		return measurement, err
	case measurement = <-writerCompleted:
	case <-ctx.Done():
		return measurement, ctx.Err()
	}
	measurement.totalElapsed = time.Since(startedAt)
	if err := verifyCompleteEmbeddingIndex(ctx, vectorDatabase, corpusDocumentCount); err != nil {
		return measurement, err
	}
	return measurement, nil
}

func readEmbeddingIndexCheckpoint(ctx context.Context, database *sql.DB, databaseAlreadyExists bool, model embeddingModelDefinition, corpusSHA256 string, corpusDocumentCount int64) (embeddingIndexCheckpoint, error) {
	if !databaseAlreadyExists {
		return embeddingIndexCheckpoint{}, nil
	}
	var existingSchemaObjects int
	if err := database.QueryRowContext(ctx, `select count(*) from sqlite_master where name in ('embedding_index_metadata', 'document_embeddings')`).Scan(&existingSchemaObjects); err != nil {
		return embeddingIndexCheckpoint{}, err
	}
	if existingSchemaObjects == 0 {
		return embeddingIndexCheckpoint{}, nil
	}
	if existingSchemaObjects != 2 {
		return embeddingIndexCheckpoint{}, errors.New("existing vector database contains an incomplete embedding index schema")
	}
	var checkpoint embeddingIndexCheckpoint
	var existingModel, existingManifestSHA256, existingDocumentPrefix, existingQueryPrefix, existingCorpusSHA256 string
	var existingCorpusDocumentCount int64
	var existingMaximumInputTokens int
	var modelEmbeddingNanoseconds, vectorTransactionNanoseconds int64
	err := database.QueryRowContext(ctx, `
		select model, model_manifest_sha256, document_prefix, query_prefix, maximum_input_tokens, dimensions, corpus_sha256,
		       corpus_document_count, indexed_document_count, last_committed_document_identifier,
		       prompt_token_count, model_embedding_nanoseconds, vector_transaction_nanoseconds
		from embedding_index_metadata where singleton = 1`).Scan(
		&existingModel, &existingManifestSHA256, &existingDocumentPrefix, &existingQueryPrefix, &existingMaximumInputTokens, &checkpoint.dimensions,
		&existingCorpusSHA256, &existingCorpusDocumentCount, &checkpoint.indexedDocumentCount,
		&checkpoint.lastCommittedDocumentIdentifier, &checkpoint.promptTokenCount,
		&modelEmbeddingNanoseconds, &vectorTransactionNanoseconds,
	)
	if err != nil {
		return checkpoint, fmt.Errorf("existing vector database does not contain a resumable embedding index: %w", err)
	}
	if existingModel != model.name || existingManifestSHA256 != model.manifestSHA256 || existingDocumentPrefix != model.documentPrefix || existingQueryPrefix != model.queryPrefix || existingMaximumInputTokens != embeddingInputTokenLimit || existingCorpusSHA256 != corpusSHA256 || existingCorpusDocumentCount != corpusDocumentCount {
		return checkpoint, errors.New("existing vector database was built from a different corpus or model contract")
	}
	checkpoint.modelEmbeddingElapsed = time.Duration(modelEmbeddingNanoseconds)
	checkpoint.vectorTransactionElapsed = time.Duration(vectorTransactionNanoseconds)
	if model.storedDimensions > 0 && checkpoint.dimensions != model.storedDimensions {
		return checkpoint, fmt.Errorf("existing vector database has %d dimensions, requested %d", checkpoint.dimensions, model.storedDimensions)
	}
	return checkpoint, nil
}

func createEmbeddingIndexSchema(ctx context.Context, database *sql.DB, model embeddingModelDefinition, dimensions int, corpusSHA256 string, corpusDocumentCount int64) error {
	if model.storedDimensions > 0 && dimensions != model.storedDimensions {
		return fmt.Errorf("model returned %d dimensions, requested %d", dimensions, model.storedDimensions)
	}
	if _, err := database.ExecContext(ctx, `pragma journal_mode = wal; pragma synchronous = normal;`); err != nil {
		return err
	}
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		create table embedding_index_metadata (
			singleton integer primary key check (singleton = 1),
			model text not null,
			model_manifest_sha256 text not null,
			document_prefix text not null,
			query_prefix text not null,
			maximum_input_tokens integer not null,
			dimensions integer not null,
			corpus_sha256 text not null,
			corpus_document_count integer not null,
			indexed_document_count integer not null,
			last_committed_document_identifier integer not null,
			prompt_token_count integer not null,
			model_embedding_nanoseconds integer not null,
			vector_transaction_nanoseconds integer not null,
			started_at text not null,
			completed_at text
		);`); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if _, err := transaction.ExecContext(ctx, fmt.Sprintf(`create virtual table document_embeddings using vec0(registered_trawler text partition key, embedding float[%d] distance_metric=cosine)`, dimensions)); err != nil {
		_ = transaction.Rollback()
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		insert into embedding_index_metadata(
			singleton, model, model_manifest_sha256, document_prefix, query_prefix, maximum_input_tokens, dimensions,
			corpus_sha256, corpus_document_count, indexed_document_count,
			last_committed_document_identifier, prompt_token_count,
			model_embedding_nanoseconds, vector_transaction_nanoseconds, started_at
		) values (1, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0, ?)`,
		model.name, model.manifestSHA256, model.documentPrefix, model.queryPrefix, embeddingInputTokenLimit, dimensions,
		corpusSHA256, corpusDocumentCount, time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = transaction.Rollback()
		return err
	}
	return transaction.Commit()
}

func readNextEmbeddingDocumentBatch(rows *sql.Rows, model embeddingModelDefinition) ([]embeddingDocument, error) {
	documents := make([]embeddingDocument, 0, model.maximumBatchDocuments)
	totalCharacters := 0
	for len(documents) < model.maximumBatchDocuments && rows.Next() {
		var document embeddingDocument
		if err := rows.Scan(&document.documentIdentifier, &document.registeredTrawler, &document.searchableText); err != nil {
			return nil, err
		}
		document.searchableText = model.documentPrefix + document.searchableText
		documents = append(documents, document)
		totalCharacters += len([]rune(document.searchableText))
		if totalCharacters >= model.maximumBatchCharacters {
			break
		}
	}
	return documents, nil
}

func embedDocumentBatch(ctx context.Context, model embeddingModelDefinition, documents []embeddingDocument) (embeddedDocumentBatch, error) {
	texts := make([]string, len(documents))
	for index := range documents {
		texts[index] = documents[index].searchableText
	}
	startedAt := time.Now()
	response, err := requestOllamaEmbeddings(ctx, model.name, model.storedDimensions, texts)
	if err != nil {
		return embeddedDocumentBatch{}, err
	}
	if len(response.Embeddings) != len(documents) {
		return embeddedDocumentBatch{}, fmt.Errorf("ollama returned %d vectors for %d documents", len(response.Embeddings), len(documents))
	}
	embeddedDocuments := make([]embeddedDocument, len(documents))
	for index, embedding := range response.Embeddings {
		if err := validateEmbeddingVector(embedding); err != nil {
			return embeddedDocumentBatch{}, err
		}
		embeddedDocuments[index] = embeddedDocument{embeddingDocument: documents[index], embedding: embedding}
	}
	return embeddedDocumentBatch{
		documents:        embeddedDocuments,
		promptTokenCount: response.PromptTokens,
		embeddingElapsed: time.Since(startedAt),
	}, nil
}

func embedDocumentBatchPair(ctx context.Context, model embeddingModelDefinition, firstDocuments []embeddingDocument, secondDocuments []embeddingDocument) ([2]embeddedDocumentBatch, error) {
	if len(secondDocuments) == 0 {
		firstBatch, err := embedDocumentBatch(ctx, model, firstDocuments)
		return [2]embeddedDocumentBatch{firstBatch}, err
	}
	pairStartedAt := time.Now()
	firstCompletion := make(chan embeddingDocumentBatchCompletion, 1)
	secondCompletion := make(chan embeddingDocumentBatchCompletion, 1)
	go func() {
		batch, err := embedDocumentBatch(ctx, model, firstDocuments)
		firstCompletion <- embeddingDocumentBatchCompletion{batch: batch, err: err}
	}()
	go func() {
		batch, err := embedDocumentBatch(ctx, model, secondDocuments)
		secondCompletion <- embeddingDocumentBatchCompletion{batch: batch, err: err}
	}()
	first := <-firstCompletion
	second := <-secondCompletion
	if first.err != nil {
		return [2]embeddedDocumentBatch{}, first.err
	}
	if second.err != nil {
		return [2]embeddedDocumentBatch{}, second.err
	}
	first.batch.embeddingElapsed = time.Since(pairStartedAt)
	second.batch.embeddingElapsed = 0
	return [2]embeddedDocumentBatch{first.batch, second.batch}, nil
}

func validateEmbeddingVector(embedding []float32) error {
	if len(embedding) == 0 {
		return errors.New("embedding model returned an empty vector")
	}
	var squaredNorm float64
	for _, component := range embedding {
		if math.IsNaN(float64(component)) || math.IsInf(float64(component), 0) {
			return errors.New("embedding model returned a non-finite vector")
		}
		squaredNorm += float64(component) * float64(component)
	}
	if squaredNorm == 0 {
		return errors.New("embedding model returned a zero vector")
	}
	return nil
}

func sendEmbeddedBatch(ctx context.Context, batches chan<- embeddedDocumentBatch, writerFailed <-chan error, batch embeddedDocumentBatch) error {
	select {
	case batches <- batch:
		return nil
	case err := <-writerFailed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeEmbeddedDocumentBatches(ctx context.Context, database *sql.DB, vectorPath string, modelName string, progressOutput io.Writer, batches <-chan embeddedDocumentBatch, measurement embeddingIndexBuildMeasurement, corpusDocumentCount int64, completed chan<- embeddingIndexBuildMeasurement, failed chan<- error) {
	pending := make([]embeddedDocument, 0, embeddingIndexTransactionDocuments+64)
	lastProgressAt := time.Now()
	startedAt := time.Now()
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		writeStartedAt := time.Now()
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		insert, err := transaction.PrepareContext(ctx, `insert into document_embeddings(rowid, registered_trawler, embedding) values (?, ?, ?)`)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		for _, document := range pending {
			if _, err := insert.ExecContext(ctx, document.documentIdentifier, document.registeredTrawler, encodeFloat32Vector(document.embedding)); err != nil {
				_ = insert.Close()
				_ = transaction.Rollback()
				return err
			}
		}
		if err := insert.Close(); err != nil {
			_ = transaction.Rollback()
			return err
		}
		measurement.indexedDocumentCount += int64(len(pending))
		lastDocumentIdentifier := pending[len(pending)-1].documentIdentifier
		completedAt := any(nil)
		if measurement.indexedDocumentCount == corpusDocumentCount {
			completedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if _, err := transaction.ExecContext(ctx, `
			update embedding_index_metadata
			set indexed_document_count = ?, last_committed_document_identifier = ?,
			    prompt_token_count = ?, model_embedding_nanoseconds = ?,
			    vector_transaction_nanoseconds = ?, completed_at = ?
			where singleton = 1`,
			measurement.indexedDocumentCount, lastDocumentIdentifier,
			measurement.promptTokenCount, measurement.modelEmbeddingElapsed.Nanoseconds(),
			measurement.vectorTransactionElapsed.Nanoseconds(), completedAt); err != nil {
			_ = transaction.Rollback()
			return err
		}
		if err := transaction.Commit(); err != nil {
			return err
		}
		measurement.vectorTransactionElapsed += time.Since(writeStartedAt)
		if _, err := database.ExecContext(ctx, `update embedding_index_metadata set vector_transaction_nanoseconds = ? where singleton = 1`, measurement.vectorTransactionElapsed.Nanoseconds()); err != nil {
			return err
		}
		pending = pending[:0]
		if time.Since(lastProgressAt) >= 10*time.Second {
			processedThisRun := measurement.indexedDocumentCount - measurement.startingDocumentCount
			elapsedThisRun := time.Since(startedAt).Seconds()
			documentsPerSecond := float64(processedThisRun) / elapsedThisRun
			tokensPerSecond := float64(measurement.promptTokenCount-measurement.startingPromptTokens) / elapsedThisRun
			remaining := corpusDocumentCount - measurement.indexedDocumentCount
			eta := time.Duration(0)
			if documentsPerSecond > 0 {
				eta = time.Duration(float64(remaining)/documentsPerSecond) * time.Second
			}
			_, _ = fmt.Fprintf(progressOutput, "model=%s indexed=%d/%d documents_per_second=%.1f tokens_per_second=%.1f eta=%s database_bytes=%d\n",
				modelName, measurement.indexedDocumentCount, corpusDocumentCount, documentsPerSecond, tokensPerSecond, eta.Round(time.Second), fileSize(vectorPath))
			lastProgressAt = time.Now()
		}
		return nil
	}
	for batch := range batches {
		measurement.promptTokenCount += batch.promptTokenCount
		measurement.modelEmbeddingElapsed += batch.embeddingElapsed
		pending = append(pending, batch.documents...)
		if len(pending) >= embeddingIndexTransactionDocuments {
			if err := flush(); err != nil {
				failed <- err
				return
			}
		}
	}
	if err := flush(); err != nil {
		failed <- err
		return
	}
	completed <- measurement
}

func verifyCompleteEmbeddingIndex(ctx context.Context, database *sql.DB, expectedDocuments int64) error {
	var indexedDocuments, vectorRows int64
	var completedAt sql.NullString
	if err := database.QueryRowContext(ctx, `select indexed_document_count, completed_at from embedding_index_metadata where singleton = 1`).Scan(&indexedDocuments, &completedAt); err != nil {
		return err
	}
	if err := database.QueryRowContext(ctx, `select count(*) from document_embeddings`).Scan(&vectorRows); err != nil {
		return err
	}
	if indexedDocuments != expectedDocuments || vectorRows != expectedDocuments || !completedAt.Valid {
		return fmt.Errorf("embedding index is incomplete: metadata=%d vectors=%d expected=%d", indexedDocuments, vectorRows, expectedDocuments)
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
