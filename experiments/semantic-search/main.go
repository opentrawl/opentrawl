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
	trawloutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	maximumEmbeddingInputCharacters = 6000
	embeddingInputTokenLimit        = 1024
)

var experimentalLocalEmbeddingModels = map[string]embeddingModelDefinition{
	"embeddinggemma": {
		name:                                "embeddinggemma",
		manifestSHA256:                      "85462619ee721b466c5927d109d4cb765861907d5417b9109caebc4e614679f1",
		documentPrefix:                      "title: none | text: ",
		queryPrefix:                         "task: search result | query: ",
		maximumBatchDocuments:               32,
		maximumBatchCharacters:              96_000,
		maximumOutstandingEmbeddingRequests: 2,
		nativeDimensions:                    768,
	},
	"qwen3-embedding:0.6b": {
		name:                                "qwen3-embedding:0.6b",
		manifestSHA256:                      "ac6da0dfba84a81fdbfbaf330198c33cd77c4cdfc53e8bc50eb581914a15621d",
		queryPrefix:                         "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: ",
		maximumBatchDocuments:               64,
		maximumBatchCharacters:              96_000,
		maximumOutstandingEmbeddingRequests: 1,
		nativeDimensions:                    1024,
	},
	"bge-m3": {
		name:                                "bge-m3",
		manifestSHA256:                      "7907646426070047a77226ac3e684fbbe8410524f7b4a74d02837e43f2146bab",
		maximumBatchDocuments:               256,
		maximumBatchCharacters:              384_000,
		maximumOutstandingEmbeddingRequests: 1,
		nativeDimensions:                    1024,
	},
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
	Truncate   bool     `json:"truncate"`
	Options    struct {
		ContextTokens int `json:"num_ctx"`
	} `json:"options"`
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
	stdout, stderr := trawloutput.StandardWriters()
	if len(os.Args) < 2 {
		fatalf(stderr, "usage: semantic-search-experiment <build-corpus|embed|evaluate|evaluate-bm25|evaluate-hybrid|search-lexical|search-bm25|search-hybrid|screen-lexical|search|measure> [options]")
	}
	sqlitevec.Auto()
	var err error
	switch os.Args[1] {
	case "build-corpus":
		err = buildCorpus(os.Args[2:], stdout)
	case "embed":
		err = embedCorpus(os.Args[2:], stdout, stderr)
	case "evaluate":
		err = evaluateSemanticRetrieval(os.Args[2:], stdout)
	case "evaluate-bm25":
		err = evaluateBM25Retrieval(os.Args[2:], stdout)
	case "evaluate-hybrid":
		err = evaluateHybridRetrieval(os.Args[2:], stdout)
	case "search-lexical":
		err = searchFrozenCorpusLexically(os.Args[2:], stdout)
	case "search-bm25":
		err = searchBM25Corpus(os.Args[2:], stdout)
	case "search-hybrid":
		err = searchHybridCorpus(os.Args[2:], stdout)
	case "screen-lexical":
		err = searchBalancedLexicalSample(os.Args[2:], stdout)
	case "search":
		err = searchCorpus(os.Args[2:], stdout)
	case "measure":
		err = measureIndex(os.Args[2:], stdout)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatalf(stderr, "%v", err)
	}
}

func searchFrozenCorpusLexically(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("search-lexical", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
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
	matchExpression := store.FTS5TokenQuery(query)
	if matchExpression == "" {
		return errors.New("QUERY contains no searchable lexical tokens")
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { _ = corpus.Close() }()
	corpus.SetMaxOpenConns(1)
	if _, err := corpus.Exec(`attach database ? as frozen_manifest`, "file:"+*manifestPath+"?mode=ro&immutable=1"); err != nil {
		return fmt.Errorf("attach frozen manifest: %w", err)
	}
	var maximumChunksPerCanonicalRecord int
	if err := corpus.QueryRow(`select coalesce(max(chunk_count), 1) from frozen_manifest.selected_records`).Scan(&maximumChunksPerCanonicalRecord); err != nil {
		return err
	}
	queryText := `
		select document.document_identifier, bm25(archive_documents_fts),
		       document.canonical_archive_record_reference, document.local_short_reference,
		       document.registered_trawler, document.archive_record_kind,
		       document.associated_time, document.searchable_text
		from archive_documents_fts
		join archive_documents document on document.document_identifier = archive_documents_fts.rowid
		join frozen_manifest.selected_documents selected on selected.document_identifier = document.document_identifier
		where archive_documents_fts match ?`
	queryArguments := []any{matchExpression}
	if *source != "" {
		queryText += ` and document.registered_trawler = ?`
		queryArguments = append(queryArguments, *source)
	}
	queryText += ` order by bm25(archive_documents_fts), document.document_identifier limit ?`
	queryArguments = append(queryArguments, maximumChunksPerCanonicalRecord*(*limit))
	rows, err := corpus.Query(queryText, queryArguments...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	matches := make([]semanticSearchMatch, 0, *limit)
	seenReferences := make(map[string]struct{}, *limit)
	for rows.Next() {
		var match semanticSearchMatch
		if err := rows.Scan(
			&match.documentIdentifier, &match.distance,
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
	return printSemanticSearchMatches(output, matches)
}

func buildCorpus(arguments []string, output io.Writer) error {
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
	defer func() { _ = corpus.Close() }()
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
	_, _ = fmt.Fprintf(output, "documents=%d searchable_text_bytes=%d database_bytes=%d\n", documentCount, searchableTextBytes, fileSize(*databasePath))
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
	defer func() { _ = archive.Close() }()
	rows, err := archive.Query(source.query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = insert.Close() }()
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

func embedCorpus(arguments []string, output io.Writer, progressOutput io.Writer) error {
	flags := flag.NewFlagSet("embed", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	vectorPath := flags.String("vectors", "", "private vector database")
	model := flags.String("model", "", "Ollama embedding model")
	dimensions := flags.Int("dimensions", 0, "embedding dimensions; zero uses the model default")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *corpusPath == "" || *vectorPath == "" || *model == "" {
		return errors.New("--corpus, --vectors and --model are required")
	}
	modelDefinition, allowed := experimentalLocalEmbeddingModels[*model]
	if !allowed {
		return fmt.Errorf("model %q is not one of the three pinned local experiment models", *model)
	}
	if err := verifyPinnedLocalOllamaModel(context.Background(), *model); err != nil {
		return err
	}
	modelDefinition.storedDimensions = *dimensions
	measurement, err := buildEmbeddingIndex(context.Background(), *corpusPath, *vectorPath, modelDefinition, progressOutput)
	if err != nil {
		return err
	}
	processedDocuments := measurement.indexedDocumentCount - measurement.startingDocumentCount
	documentsPerSecond := float64(0)
	if measurement.totalElapsed > 0 {
		documentsPerSecond = float64(processedDocuments) / measurement.totalElapsed.Seconds()
	}
	_, _ = fmt.Fprintf(output, "model=%s documents=%d prompt_tokens=%d elapsed=%s documents_per_second=%.2f model_embedding=%s vector_transactions=%s database_bytes=%d resumed=%t\n",
		modelDefinition.name,
		measurement.indexedDocumentCount,
		measurement.promptTokenCount-measurement.startingPromptTokens,
		measurement.totalElapsed.Round(time.Millisecond),
		documentsPerSecond,
		(measurement.modelEmbeddingElapsed - measurement.startingModelEmbeddingElapsed).Round(time.Millisecond),
		(measurement.vectorTransactionElapsed - measurement.startingVectorTransactionElapsed).Round(time.Millisecond),
		fileSize(*vectorPath),
		measurement.resumed,
	)
	return nil
}

func searchCorpus(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	corpusPath := flags.String("corpus", "", "private derived corpus database")
	vectorPath := flags.String("vectors", "", "private vector database")
	limit := flags.Int("limit", 20, "maximum semantic matches")
	source := flags.String("trawler", "", "optional registered trawler filter")
	allowSampledIndex := flags.Bool("allow-sampled-index", false, "allow a deliberately incomplete model-screening index")
	excludedLinks := flags.String("exclude-links", "", "comma-separated OpenTrawl links to exclude")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *corpusPath == "" || *vectorPath == "" || query == "" || *limit <= 0 {
		return errors.New("--corpus, --vectors, a positive --limit and QUERY are required")
	}
	excludedLinkSet := make(map[string]struct{})
	for _, excludedLink := range strings.Split(*excludedLinks, ",") {
		if excludedLink = strings.TrimSpace(excludedLink); excludedLink != "" {
			excludedLinkSet[excludedLink] = struct{}{}
		}
	}
	corpus, err := sql.Open("sqlite3", "file:"+*corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return err
	}
	defer func() { _ = corpus.Close() }()
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
		return err
	}
	modelDefinition, allowed := experimentalLocalEmbeddingModels[indexedModel]
	if !allowed {
		return fmt.Errorf("semantic index uses unsupported model %q", indexedModel)
	}
	currentCorpusSHA256, err := sha256File(*corpusPath)
	if err != nil {
		return err
	}
	if indexedModel != modelDefinition.name || indexedManifestSHA256 != modelDefinition.manifestSHA256 || indexedDocumentPrefix != modelDefinition.documentPrefix || indexedQueryPrefix != modelDefinition.queryPrefix || indexedMaximumInputTokens != embeddingInputTokenLimit || indexedCorpusSHA256 != currentCorpusSHA256 {
		return errors.New("semantic index does not match the requested corpus and embedding model contract")
	}
	if err := verifyPinnedLocalOllamaModel(context.Background(), indexedModel); err != nil {
		return err
	}
	response, err := requestOllamaEmbeddings(context.Background(), indexedModel, indexedDimensions, []string{indexedQueryPrefix + query})
	if err != nil {
		return err
	}
	if len(response.Embeddings) != 1 || len(response.Embeddings[0]) != indexedDimensions {
		return fmt.Errorf("ollama returned an invalid query embedding shape")
	}
	if err := validateEmbeddingVector(response.Embeddings[0]); err != nil {
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
	defer func() { _ = rows.Close() }()
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
	return printSemanticSearchMatches(output, matches)
}

func searchBalancedLexicalSample(arguments []string, output io.Writer) error {
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
	defer func() { _ = corpus.Close() }()
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
	defer func() { _ = rows.Close() }()
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
	return printSemanticSearchMatches(output, matches)
}

func printSemanticSearchMatches(output io.Writer, matches []semanticSearchMatch) error {
	for index, match := range matches {
		link, err := composeSemanticSearchMatchLink(match)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "%d. %s %s\n", index+1, match.registeredTrawler, match.recordKind)
		if match.associatedTime != "" {
			_, _ = fmt.Fprintf(output, "   time %s\n", match.associatedTime)
		}
		_, _ = fmt.Fprintf(output, "   %s\n", singleLineSnippet(match.searchableText, 320))
		_, _ = fmt.Fprintf(output, "   link %s\n\n", trawlkit.GloballyRoutableTrawlLinkText(link))
	}
	return nil
}

func composeSemanticSearchMatchLink(match semanticSearchMatch) (*trawlkit.GloballyRoutableTrawlLink, error) {
	return trawlkit.ComposeGloballyRoutableTrawlLink(trawlkit.GloballyRoutableTrawlLinkRoute{
		RegisteredTrawler:   trawlkit.NewRegisteredTrawlerIdentity(match.registeredTrawler),
		LocalShortReference: trawlkit.NewLocalTrawlerShortReference(match.localShortReference),
	})
}

func measureIndex(arguments []string, output io.Writer) error {
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
	defer func() { _ = corpus.Close() }()
	rows, err := corpus.Query(`select registered_trawler, count(*), sum(length(searchable_text)) from archive_documents group by registered_trawler order by registered_trawler`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var source string
		var count, textBytes int64
		if err := rows.Scan(&source, &count, &textBytes); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(output, "source=%s documents=%d searchable_text_bytes=%d\n", source, count, textBytes)
	}
	_, _ = fmt.Fprintf(output, "corpus_database_bytes=%d\n", fileSize(*corpusPath))
	if *vectorPath != "" {
		_, _ = fmt.Fprintf(output, "vector_database_bytes=%d\n", fileSize(*vectorPath))
	}
	return rows.Err()
}

func requestOllamaEmbeddings(ctx context.Context, model string, dimensions int, inputs []string) (ollamaEmbeddingResponse, error) {
	embeddingRequest := ollamaEmbeddingRequest{Model: model, Input: inputs, Dimensions: dimensions, KeepAlive: "30m", Truncate: true}
	embeddingRequest.Options.ContextTokens = embeddingInputTokenLimit
	payload, err := json.Marshal(embeddingRequest)
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ollamaEndpoint()+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return ollamaEmbeddingResponse{}, fmt.Errorf("ollama returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded ollamaEmbeddingResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return ollamaEmbeddingResponse{}, err
	}
	return decoded, nil
}

func verifyPinnedLocalOllamaModel(ctx context.Context, model string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaEndpoint()+"/api/tags", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ollama model list returned HTTP %d", response.StatusCode)
	}
	var localModels ollamaLocalModelList
	if err := json.NewDecoder(response.Body).Decode(&localModels); err != nil {
		return err
	}
	expectedDigest := experimentalLocalEmbeddingModels[model].manifestSHA256
	for _, localModel := range localModels.Models {
		localName := strings.TrimSuffix(localModel.Name, ":latest")
		if (localModel.Name == model || localName == model) && localModel.Digest == expectedDigest {
			return nil
		}
	}
	return fmt.Errorf("pinned local Ollama model %q with digest %s is not installed", model, expectedDigest)
}

func ollamaEndpoint() string {
	if endpoint := strings.TrimSuffix(os.Getenv("OPENTRAWL_OLLAMA_ENDPOINT"), "/"); endpoint != "" {
		return endpoint
	}
	return "http://127.0.0.1:11434"
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

func fatalf(output io.Writer, format string, arguments ...any) {
	_, _ = fmt.Fprintf(output, format+"\n", arguments...)
	os.Exit(1)
}
