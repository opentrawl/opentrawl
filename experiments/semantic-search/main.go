package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"path/filepath"
	"strings"
	"time"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const maximumEmbeddingInputCharacters = 6000

var experimentalLocalEmbeddingModels = map[string]string{
	"embeddinggemma":       "85462619ee721b466c5927d109d4cb765861907d5417b9109caebc4e614679f1",
	"qwen3-embedding:0.6b": "ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d",
	"bge-m3":               "7907646426070047a77226ac3e684fbbe8410524f7b4a74d02837e43f2146bab",
}

type archiveDocumentSource struct {
	registeredTrawler string
	recordKind        string
	archivePath       string
	query             string
}

type ollamaEmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
	KeepAlive  string   `json:"keep_alive"`
}

type ollamaEmbeddingResponse struct {
	Embeddings   [][]float32 `json:"embeddings"`
	PromptTokens int64       `json:"prompt_eval_count"`
}

type ollamaLocalModelList struct {
	Models []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
	} `json:"models"`
}

type semanticSearchMatch struct {
	documentIdentifier  int64
	distance            float64
	reference           string
	localShortReference string
	registeredTrawler   string
	recordKind          string
	associatedTime      string
	searchableText      string
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: semantic-search-experiment <build-corpus|embed|screen-lexical|search|measure> [options]")
	}
	sqlitevec.Auto()
	var err error
	switch os.Args[1] {
	case "build-corpus":
		err = buildCorpus(os.Args[2:])
	case "embed":
		err = embedCorpus(os.Args[2:])
	case "screen-lexical":
		err = searchBalancedLexicalSample(os.Args[2:])
	case "search":
		err = searchCorpus(os.Args[2:])
	case "measure":
		err = measureIndex(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func buildCorpus(arguments []string) error {
	flags := flag.NewFlagSet("build-corpus", flag.ContinueOnError)
	stateRoot := flags.String("state-root", "", "OpenTrawl state root")
	databasePath := flags.String("database", "", "private derived corpus database")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if strings.TrimSpace(*stateRoot) == "" || strings.TrimSpace(*databasePath) == "" {
		return errors.New("--state-root and --database are required")
	}
	if _, err := os.Stat(*databasePath); err == nil {
		return errors.New("corpus database already exists; build a new snapshot instead of reusing it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*databasePath), 0o700); err != nil {
		return fmt.Errorf("create corpus directory: %w", err)
	}
	corpus, err := sql.Open("sqlite3", *databasePath)
	if err != nil {
		return err
	}
	defer corpus.Close()
	if err := corpus.Ping(); err != nil {
		return err
	}
	if err := os.Chmod(*databasePath, 0o600); err != nil {
		return err
	}
	if _, err := corpus.Exec(`
		pragma journal_mode = wal;
		create table if not exists archive_documents (
			document_identifier integer primary key,
			canonical_archive_record_reference text not null,
			local_short_reference text not null,
			registered_trawler text not null,
			archive_record_kind text not null,
			associated_time text not null,
			searchable_text text not null,
			searchable_text_sha256 blob not null,
			unique(canonical_archive_record_reference, searchable_text_sha256)
		);
		create virtual table if not exists archive_documents_fts using fts5(
			searchable_text,
			content='archive_documents',
			content_rowid='document_identifier'
		);
		create trigger if not exists archive_documents_after_insert after insert on archive_documents begin
			insert into archive_documents_fts(rowid, searchable_text) values (new.document_identifier, new.searchable_text);
		end;
	`); err != nil {
		return fmt.Errorf("create corpus schema: %w", err)
	}

	for _, source := range experimentalArchiveDocumentSources(*stateRoot) {
		if err := addArchiveDocuments(corpus, source); err != nil {
			return fmt.Errorf("project %s: %w", source.registeredTrawler, err)
		}
	}
	var documentCount int64
	var searchableTextBytes int64
	if err := corpus.QueryRow(`select count(*), coalesce(sum(length(searchable_text)), 0) from archive_documents`).Scan(&documentCount, &searchableTextBytes); err != nil {
		return err
	}
	fmt.Printf("documents=%d searchable_text_bytes=%d database_bytes=%d\n", documentCount, searchableTextBytes, fileSize(*databasePath))
	return nil
}

func experimentalArchiveDocumentSources(stateRoot string) []archiveDocumentSource {
	return []archiveDocumentSource{
		{
			registeredTrawler: "imessage", recordKind: "message",
			archivePath: filepath.Join(stateRoot, "imessage", "imessage.db"),
			query:       `select 'imessage:msg/' || fts.c0, datetime(messages.date / 1000000000 + 978307200, 'unixepoch'), fts.c1 from messages_fts_content fts join messages on messages.source_rowid = cast(fts.c0 as integer) where trim(fts.c1) <> ''`,
		},
		{
			registeredTrawler: "whatsapp", recordKind: "message",
			archivePath: filepath.Join(stateRoot, "whatsapp", "whatsapp.db"),
			query:       `select 'whatsapp:msg/' || messages.msg_id, datetime(messages.ts, 'unixepoch'), trim(coalesce(fts.c0, '') || char(10) || coalesce(fts.c3, '')) from messages_fts_content fts join messages on messages.rowid = fts.id where trim(coalesce(fts.c0, '') || coalesce(fts.c3, '')) <> ''`,
		},
		{
			registeredTrawler: "telegram", recordKind: "message",
			archivePath: filepath.Join(stateRoot, "telegram", "telegram.db"),
			query:       `select 'telegram:msg/' || messages.source_pk, datetime(messages.ts, 'unixepoch'), trim(coalesce(fts.c0, '') || char(10) || coalesce(fts.c3, '')) from messages_fts_content fts join messages on messages.rowid = fts.id where trim(coalesce(fts.c0, '') || coalesce(fts.c3, '')) <> ''`,
		},
		{
			registeredTrawler: "notes", recordKind: "note-version",
			archivePath: filepath.Join(stateRoot, "notes", "notes.db"),
			query:       `select 'notes:version/' || fts.c0 || '/' || fts.c1, notes.modified_at, trim(coalesce(fts.c2, '') || char(10) || coalesce(fts.c3, '')) from notes_fts_content fts join notes on notes.note_id = fts.c0 where trim(coalesce(fts.c2, '') || coalesce(fts.c3, '')) <> ''`,
		},
		{
			registeredTrawler: "gmail", recordKind: "message",
			archivePath: filepath.Join(stateRoot, "gmail", "gmail.db"),
			query:       `select 'gmail:msg/' || fts.c0, messages.time, trim(coalesce(fts.c1, '') || char(10) || coalesce(fts.c2, '')) from messages_fts_content fts join messages on messages.id = fts.c0 where trim(coalesce(fts.c1, '') || coalesce(fts.c2, '')) <> ''`,
		},
		{
			registeredTrawler: "calendar", recordKind: "event",
			archivePath: filepath.Join(stateRoot, "calendar", "calendar.db"),
			query:       `select 'calendar:event/' || fts.c0, events.start_time, trim(coalesce(fts.c1, '') || char(10) || coalesce(fts.c2, '') || char(10) || coalesce(fts.c3, '') || char(10) || coalesce(fts.c4, '')) from events_fts_content fts join events on events.event_uid = fts.c0 where trim(coalesce(fts.c1, '') || coalesce(fts.c2, '') || coalesce(fts.c3, '') || coalesce(fts.c4, '')) <> ''`,
		},
	}
}

func addArchiveDocuments(corpus *sql.DB, source archiveDocumentSource) error {
	archive, err := sql.Open("sqlite3", "file:"+source.archivePath+"?mode=ro")
	if err != nil {
		return err
	}
	defer archive.Close()
	rows, err := archive.Query(source.query)
	if err != nil {
		return err
	}
	defer rows.Close()
	references := make([]string, 0)
	projectedRows := make([][3]string, 0)
	for rows.Next() {
		var reference, associatedTime, searchableText string
		if err := rows.Scan(&reference, &associatedTime, &searchableText); err != nil {
			return err
		}
		references = append(references, reference)
		projectedRows = append(projectedRows, [3]string{reference, associatedTime, searchableText})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	aliases, err := shortref.NewSQLiteIndex(archive).Aliases(context.Background(), references)
	if err != nil {
		return err
	}
	transaction, err := corpus.Begin()
	if err != nil {
		return err
	}
	insert, err := transaction.Prepare(`
		insert or ignore into archive_documents(
			canonical_archive_record_reference, registered_trawler, archive_record_kind, local_short_reference,
			associated_time, searchable_text, searchable_text_sha256
		) values (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = transaction.Rollback()
		return err
	}
	defer insert.Close()
	for _, projectedRow := range projectedRows {
		reference, associatedTime, searchableText := projectedRow[0], projectedRow[1], projectedRow[2]
		localShortReference := aliases[reference]
		if localShortReference == "" {
			_ = transaction.Rollback()
			return fmt.Errorf("no local short reference for %s", reference)
		}
		for _, chunk := range splitTextIntoEmbeddingChunks(searchableText) {
			checksum := sha256.Sum256([]byte(chunk))
			if _, err := insert.Exec(reference, source.registeredTrawler, source.recordKind, localShortReference, associatedTime, chunk, checksum[:]); err != nil {
				_ = transaction.Rollback()
				return err
			}
		}
	}
	return transaction.Commit()
}

func splitTextIntoEmbeddingChunks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= maximumEmbeddingInputCharacters {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)+maximumEmbeddingInputCharacters-1)/maximumEmbeddingInputCharacters)
	for start := 0; start < len(runes); start += maximumEmbeddingInputCharacters {
		end := min(start+maximumEmbeddingInputCharacters, len(runes))
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func embedCorpus(arguments []string) error {
	flags := flag.NewFlagSet("embed", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	vectorPath := flags.String("vectors", "", "private vector database")
	model := flags.String("model", "", "Ollama embedding model")
	dimensions := flags.Int("dimensions", 0, "embedding dimensions; zero uses the model default")
	batchSize := flags.Int("batch-size", 64, "documents per Ollama request")
	maximumDocuments := flags.Int64("maximum-documents", 0, "optional benchmark limit")
	documentsPerTrawler := flags.Int64("documents-per-trawler", 0, "optional deterministic source-balanced benchmark limit")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *corpusPath == "" || *vectorPath == "" || *model == "" || *batchSize <= 0 {
		return errors.New("--corpus, --vectors, --model and a positive --batch-size are required")
	}
	if _, allowed := experimentalLocalEmbeddingModels[*model]; !allowed {
		return fmt.Errorf("model %q is not one of the three pinned local experiment models", *model)
	}
	if err := verifyPinnedLocalOllamaModel(context.Background(), *model); err != nil {
		return err
	}
	if _, err := os.Stat(*vectorPath); err == nil {
		return errors.New("vector database already exists; build a new index instead of resuming it")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer corpus.Close()
	vectorDatabase, err := sql.Open("sqlite3", *vectorPath)
	if err != nil {
		return err
	}
	defer vectorDatabase.Close()
	if err := vectorDatabase.Ping(); err != nil {
		return err
	}
	if err := os.Chmod(*vectorPath, 0o600); err != nil {
		return err
	}
	if _, err := vectorDatabase.Exec(`create table if not exists embedding_index_metadata (
		model text not null,
		model_manifest_sha256 text not null,
		dimensions integer not null,
		created_at text not null,
		corpus_document_count integer not null,
		indexed_document_count integer not null
	)`); err != nil {
		return err
	}
	var corpusDocumentCount int64
	if err := corpus.QueryRow(`select count(*) from archive_documents`).Scan(&corpusDocumentCount); err != nil {
		return err
	}
	query := `select document_identifier, registered_trawler, searchable_text from archive_documents order by document_identifier`
	queryArguments := []any{}
	if *documentsPerTrawler > 0 {
		query = `
			select document_identifier, registered_trawler, searchable_text
			from (
				select document_identifier, registered_trawler, searchable_text,
				       row_number() over (partition by registered_trawler order by searchable_text_sha256, document_identifier) as source_document_number
				from archive_documents
			)
			where source_document_number <= ?
			order by document_identifier
		`
		queryArguments = []any{*documentsPerTrawler}
	} else if *maximumDocuments > 0 {
		query += ` limit ?`
		queryArguments = append(queryArguments, *maximumDocuments)
	}
	rows, err := corpus.Query(query, queryArguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	startedAt := time.Now()
	var embeddedDocuments int64
	var promptTokens int64
	for {
		documentIdentifiers := make([]int64, 0, *batchSize)
		registeredTrawlers := make([]string, 0, *batchSize)
		texts := make([]string, 0, *batchSize)
		for len(documentIdentifiers) < *batchSize && rows.Next() {
			var documentIdentifier int64
			var registeredTrawler string
			var searchableText string
			if err := rows.Scan(&documentIdentifier, &registeredTrawler, &searchableText); err != nil {
				return err
			}
			documentIdentifiers = append(documentIdentifiers, documentIdentifier)
			registeredTrawlers = append(registeredTrawlers, registeredTrawler)
			texts = append(texts, searchableText)
		}
		if len(documentIdentifiers) == 0 {
			break
		}
		response, err := requestOllamaEmbeddings(context.Background(), *model, *dimensions, texts)
		if err != nil {
			return err
		}
		if len(response.Embeddings) != len(documentIdentifiers) || len(response.Embeddings[0]) == 0 {
			return fmt.Errorf("Ollama returned %d vectors for %d documents", len(response.Embeddings), len(documentIdentifiers))
		}
		actualDimensions := len(response.Embeddings[0])
		if err := ensureVectorSchema(vectorDatabase, *model, actualDimensions, corpusDocumentCount); err != nil {
			return err
		}
		transaction, err := vectorDatabase.Begin()
		if err != nil {
			return err
		}
		insert, err := transaction.Prepare(`insert into document_embeddings(rowid, registered_trawler, embedding) values (?, ?, ?)`)
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
		for index, embedding := range response.Embeddings {
			if len(embedding) != actualDimensions {
				_ = insert.Close()
				_ = transaction.Rollback()
				return errors.New("embedding dimensions changed within one batch")
			}
			if _, err := insert.Exec(documentIdentifiers[index], registeredTrawlers[index], encodeFloat32Vector(embedding)); err != nil {
				_ = insert.Close()
				_ = transaction.Rollback()
				return err
			}
		}
		_ = insert.Close()
		if err := transaction.Commit(); err != nil {
			return err
		}
		embeddedDocuments += int64(len(documentIdentifiers))
		promptTokens += response.PromptTokens
		if embeddedDocuments%10000 == 0 {
			fmt.Fprintf(os.Stderr, "embedded=%d elapsed=%s\n", embeddedDocuments, time.Since(startedAt).Round(time.Second))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := vectorDatabase.Exec(`update embedding_index_metadata set indexed_document_count = ?`, embeddedDocuments); err != nil {
		return err
	}
	elapsed := time.Since(startedAt)
	fmt.Printf("model=%s documents=%d prompt_tokens=%d elapsed=%s documents_per_second=%.2f database_bytes=%d\n",
		*model, embeddedDocuments, promptTokens, elapsed.Round(time.Millisecond), float64(embeddedDocuments)/elapsed.Seconds(), fileSize(*vectorPath))
	return nil
}

func ensureVectorSchema(database *sql.DB, model string, dimensions int, corpusDocumentCount int64) error {
	var existingModel string
	var existingDimensions int
	err := database.QueryRow(`select model, dimensions from embedding_index_metadata limit 1`).Scan(&existingModel, &existingDimensions)
	if err == nil {
		if existingModel != model || existingDimensions != dimensions {
			return fmt.Errorf("vector database contains %s at %d dimensions", existingModel, existingDimensions)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := database.Exec(fmt.Sprintf(`create virtual table document_embeddings using vec0(registered_trawler text partition key, embedding float[%d] distance_metric=cosine)`, dimensions)); err != nil {
		return err
	}
	_, err = database.Exec(`insert into embedding_index_metadata(model, model_manifest_sha256, dimensions, created_at, corpus_document_count, indexed_document_count) values (?, ?, ?, ?, ?, 0)`, model, experimentalLocalEmbeddingModels[model], dimensions, time.Now().UTC().Format(time.RFC3339), corpusDocumentCount)
	return err
}

func searchCorpus(arguments []string) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	vectorPath := flags.String("vectors", "", "private vector database")
	model := flags.String("model", "", "Ollama embedding model")
	dimensions := flags.Int("dimensions", 0, "query embedding dimensions")
	limit := flags.Int("limit", 20, "maximum semantic matches")
	source := flags.String("trawler", "", "optional registered trawler filter")
	allowSampledIndex := flags.Bool("allow-sampled-index", false, "allow a deliberately incomplete model-screening index")
	excludedLinks := flags.String("exclude-links", "", "comma-separated OpenTrawl links to exclude")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *corpusPath == "" || *vectorPath == "" || *model == "" || query == "" || *limit <= 0 {
		return errors.New("--corpus, --vectors, --model, a positive --limit and QUERY are required")
	}
	if _, allowed := experimentalLocalEmbeddingModels[*model]; !allowed {
		return fmt.Errorf("model %q is not one of the three pinned local experiment models", *model)
	}
	if err := verifyPinnedLocalOllamaModel(context.Background(), *model); err != nil {
		return err
	}
	response, err := requestOllamaEmbeddings(context.Background(), *model, *dimensions, []string{query})
	if err != nil {
		return err
	}
	if len(response.Embeddings) != 1 {
		return fmt.Errorf("Ollama returned %d query embeddings", len(response.Embeddings))
	}
	excludedLinkSet := make(map[string]struct{})
	for _, excludedLink := range strings.Split(*excludedLinks, ",") {
		if excludedLink = strings.TrimSpace(excludedLink); excludedLink != "" {
			excludedLinkSet[excludedLink] = struct{}{}
		}
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer corpus.Close()
	var maximumChunksPerCanonicalRecord int
	if err := corpus.QueryRow(`
		select coalesce(max(chunk_count), 1)
		from (
			select count(*) as chunk_count
			from archive_documents
			group by canonical_archive_record_reference
		)
	`).Scan(&maximumChunksPerCanonicalRecord); err != nil {
		return err
	}
	vectorDatabase, err := sql.Open("sqlite3", "file:"+*vectorPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer vectorDatabase.Close()
	var corpusDocumentCount, indexedDocumentCount int64
	if err := vectorDatabase.QueryRow(`select corpus_document_count, indexed_document_count from embedding_index_metadata limit 1`).Scan(&corpusDocumentCount, &indexedDocumentCount); err != nil {
		return err
	}
	if !*allowSampledIndex && indexedDocumentCount != corpusDocumentCount {
		return fmt.Errorf("semantic index is incomplete: indexed %d of %d documents", indexedDocumentCount, corpusDocumentCount)
	}
	requestedVectorCandidates := maximumChunksPerCanonicalRecord * (*limit + len(excludedLinkSet))
	if requestedVectorCandidates > int(indexedDocumentCount) {
		requestedVectorCandidates = int(indexedDocumentCount)
	}
	var rows *sql.Rows
	if *source == "" {
		rows, err = vectorDatabase.Query(`select rowid, distance from document_embeddings where embedding match ? and k = ? order by distance`, encodeFloat32Vector(response.Embeddings[0]), requestedVectorCandidates)
	} else {
		rows, err = vectorDatabase.Query(`select rowid, distance from document_embeddings where embedding match ? and k = ? and registered_trawler = ? order by distance`, encodeFloat32Vector(response.Embeddings[0]), requestedVectorCandidates, *source)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	candidates := make([]semanticSearchMatch, 0, *limit*4)
	for rows.Next() {
		var match semanticSearchMatch
		if err := rows.Scan(&match.documentIdentifier, &match.distance); err != nil {
			return err
		}
		candidates = append(candidates, match)
	}
	matches := make([]semanticSearchMatch, 0, *limit)
	seenReferences := make(map[string]struct{}, *limit)
	for _, candidate := range candidates {
		err := corpus.QueryRow(`select canonical_archive_record_reference, local_short_reference, registered_trawler, archive_record_kind, associated_time, searchable_text from archive_documents where document_identifier = ?`, candidate.documentIdentifier).Scan(
			&candidate.reference, &candidate.localShortReference, &candidate.registeredTrawler, &candidate.recordKind, &candidate.associatedTime, &candidate.searchableText,
		)
		if err != nil {
			return err
		}
		if _, alreadyAdded := seenReferences[candidate.reference]; alreadyAdded {
			continue
		}
		candidateLink, err := composeSemanticSearchMatchLink(candidate)
		if err != nil {
			return err
		}
		if _, excluded := excludedLinkSet[trawlkit.GloballyRoutableTrawlLinkText(candidateLink)]; excluded {
			continue
		}
		seenReferences[candidate.reference] = struct{}{}
		matches = append(matches, candidate)
		if len(matches) == *limit {
			break
		}
	}
	return printSemanticSearchMatches(matches)
}

func searchBalancedLexicalSample(arguments []string) error {
	flags := flag.NewFlagSet("screen-lexical", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	documentsPerTrawler := flags.Int64("documents-per-trawler", 1000, "deterministic documents per trawler")
	limit := flags.Int("limit", 5, "maximum lexical matches")
	source := flags.String("trawler", "", "optional registered trawler filter")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *corpusPath == "" || query == "" || *documentsPerTrawler <= 0 || *limit <= 0 {
		return errors.New("--corpus, positive limits, and QUERY are required")
	}
	matchExpression := store.FTS5TokenQuery(query)
	if matchExpression == "" {
		return errors.New("QUERY contains no searchable lexical tokens")
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer corpus.Close()
	queryText := `
		with balanced_sample as (
			select document_identifier
			from (
				select document_identifier, registered_trawler,
				       row_number() over (partition by registered_trawler order by searchable_text_sha256, document_identifier) as source_document_number
				from archive_documents
			)
			where source_document_number <= ?
		)
		select archive_documents.document_identifier, bm25(archive_documents_fts)
		from archive_documents_fts
		join archive_documents on archive_documents.document_identifier = archive_documents_fts.rowid
		join balanced_sample on balanced_sample.document_identifier = archive_documents.document_identifier
		where archive_documents_fts match ?`
	queryArguments := []any{*documentsPerTrawler, matchExpression}
	if *source != "" {
		queryText += ` and archive_documents.registered_trawler = ?`
		queryArguments = append(queryArguments, *source)
	}
	queryText += ` order by bm25(archive_documents_fts), archive_documents.document_identifier limit ?`
	queryArguments = append(queryArguments, *limit*4)
	rows, err := corpus.Query(queryText, queryArguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	matches := make([]semanticSearchMatch, 0, *limit)
	seenReferences := make(map[string]struct{}, *limit)
	for rows.Next() {
		var match semanticSearchMatch
		if err := rows.Scan(&match.documentIdentifier, &match.distance); err != nil {
			return err
		}
		if err := corpus.QueryRow(`select canonical_archive_record_reference, local_short_reference, registered_trawler, archive_record_kind, associated_time, searchable_text from archive_documents where document_identifier = ?`, match.documentIdentifier).Scan(
			&match.reference, &match.localShortReference, &match.registeredTrawler, &match.recordKind, &match.associatedTime, &match.searchableText,
		); err != nil {
			return err
		}
		if _, duplicate := seenReferences[match.reference]; duplicate {
			continue
		}
		seenReferences[match.reference] = struct{}{}
		matches = append(matches, match)
		if len(matches) == *limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return printSemanticSearchMatches(matches)
}

func printSemanticSearchMatches(matches []semanticSearchMatch) error {
	for index, match := range matches {
		link, err := composeSemanticSearchMatchLink(match)
		if err != nil {
			return err
		}
		fmt.Printf("%d. %s %s\n", index+1, match.registeredTrawler, match.recordKind)
		if match.associatedTime != "" {
			fmt.Printf("   time %s\n", match.associatedTime)
		}
		fmt.Printf("   %s\n", singleLineSnippet(match.searchableText, 320))
		fmt.Printf("   link %s\n\n", trawlkit.GloballyRoutableTrawlLinkText(link))
	}
	return nil
}

func composeSemanticSearchMatchLink(match semanticSearchMatch) (*trawlkit.GloballyRoutableTrawlLink, error) {
	return trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
		RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity(match.registeredTrawler),
		LocalShortReference: trawlkit.NewLocalTrawlerShortReference(match.localShortReference),
	})
}

func measureIndex(arguments []string) error {
	flags := flag.NewFlagSet("measure", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	vectorPath := flags.String("vectors", "", "private vector database")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *corpusPath == "" {
		return errors.New("--corpus is required")
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer corpus.Close()
	rows, err := corpus.Query(`select registered_trawler, count(*), sum(length(searchable_text)) from archive_documents group by registered_trawler order by registered_trawler`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var count, textBytes int64
		if err := rows.Scan(&source, &count, &textBytes); err != nil {
			return err
		}
		fmt.Printf("source=%s documents=%d searchable_text_bytes=%d\n", source, count, textBytes)
	}
	fmt.Printf("corpus_database_bytes=%d\n", fileSize(*corpusPath))
	if *vectorPath != "" {
		fmt.Printf("vector_database_bytes=%d\n", fileSize(*vectorPath))
	}
	return rows.Err()
}

func requestOllamaEmbeddings(ctx context.Context, model string, dimensions int, inputs []string) (ollamaEmbeddingResponse, error) {
	payload, err := json.Marshal(ollamaEmbeddingRequest{Model: model, Input: inputs, Dimensions: dimensions, KeepAlive: "30m"})
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:11434/api/embed", bytes.NewReader(payload))
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ollamaEmbeddingResponse{}, fmt.Errorf("Ollama returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded ollamaEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	return decoded, nil
}

func verifyPinnedLocalOllamaModel(ctx context.Context, model string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:11434/api/tags", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Ollama model list returned HTTP %d", response.StatusCode)
	}
	var localModels ollamaLocalModelList
	if err := json.NewDecoder(response.Body).Decode(&localModels); err != nil {
		return err
	}
	expectedDigest := experimentalLocalEmbeddingModels[model]
	for _, localModel := range localModels.Models {
		localName := strings.TrimSuffix(localModel.Name, ":latest")
		if (localModel.Name == model || localName == model) && localModel.Digest == expectedDigest {
			return nil
		}
	}
	return fmt.Errorf("pinned local Ollama model %q with digest %s is not installed", model, expectedDigest)
}

func encodeFloat32Vector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(value))
	}
	return encoded
}

func singleLineSnippet(text string, maximumCharacters int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > maximumCharacters {
		return string(runes[:maximumCharacters]) + "…"
	}
	return text
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
