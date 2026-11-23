package semantic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const SemanticDatabaseFilename = "semantic.db"

var semanticWriteMutex sync.Mutex

// ResolveSemanticDatabasePath returns <stateRoot>/semantic.db.
func ResolveSemanticDatabasePath(stateRoot string) string {
	return filepath.Join(stateRoot, SemanticDatabaseFilename)
}

const semanticSchemaSQL = `
CREATE TABLE IF NOT EXISTS semantic_metadata (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS semantic_chunks (
	chunk_id                     INTEGER PRIMARY KEY,
	chunk_key                    TEXT NOT NULL UNIQUE,
	trawler                      TEXT NOT NULL,
	trawler_display_name         TEXT NOT NULL,
	record_kind                  TEXT NOT NULL,
	conversation_key             TEXT NOT NULL DEFAULT '',
	display_context              TEXT NOT NULL DEFAULT '',
	representative_canonical_ref TEXT NOT NULL,
	representative_resolved_ref  TEXT NOT NULL,
	occurred_unix                INTEGER NOT NULL DEFAULT 0,
	has_time_of_day              INTEGER NOT NULL DEFAULT 1,
	utc_offset_seconds           INTEGER NOT NULL DEFAULT 0,
	title                        TEXT NOT NULL DEFAULT '',
	people_text                  TEXT NOT NULL DEFAULT '',
	body_preview                 TEXT NOT NULL DEFAULT '',
	message_count                INTEGER NOT NULL DEFAULT 1,
	source_record_hash           TEXT NOT NULL,
	embedding_int8               BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS semantic_chunk_members (
	chunk_id      INTEGER NOT NULL,
	trawler       TEXT NOT NULL,
	canonical_ref TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_semantic_chunks_trawler_time ON semantic_chunks(trawler, occurred_unix DESC);
CREATE INDEX IF NOT EXISTS idx_semantic_chunks_time ON semantic_chunks(occurred_unix DESC);
CREATE INDEX IF NOT EXISTS idx_semantic_members_chunk ON semantic_chunk_members(chunk_id);
`

// OpenSemanticIndexReadWrite opens <stateRoot>/semantic.db creating its schema if needed.
func OpenSemanticIndexReadWrite(ctx context.Context, stateRoot string) (*store.Store, error) {
	path := ResolveSemanticDatabasePath(stateRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create semantic index directory: %w", err)
	}
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("open semantic index read-write: %w", err)
	}
	if _, err := st.DB().ExecContext(ctx, semanticSchemaSQL); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("ensure semantic index schema: %w", err)
	}
	return st, nil
}

// OpenSemanticIndexReadOnly opens <stateRoot>/semantic.db read-only if present.
// Returns (nil, nil) when semantic.db does not exist yet.
func OpenSemanticIndexReadOnly(ctx context.Context, stateRoot string) (*store.Store, error) {
	path := ResolveSemanticDatabasePath(stateRoot)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat semantic index: %w", err)
	}
	st, err := store.OpenReadOnly(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return st, err
}

// SemanticTrawlerCount summarizes embedded chunk counts for one trawler.
type SemanticTrawlerCount struct {
	TrawlerIdentity    string
	TrawlerDisplayName string
	ChunkCount         int64
}

// SemanticIndexStatus summarizes the disposable semantic search index state.
type SemanticIndexStatus struct {
	SemanticDatabasePath  string
	DatabaseExists        bool
	SemanticSearchEnabled bool
	ModelIdentifier       string
	ModelDirectoryPath    string
	ModelWeightsCached    bool
	ModelWeightsByteCount uint64
	HelperBinaryFound     bool
	HelperRunning         bool
	HelperModelLoaded     bool
	HelperActiveMetalByte uint64
	ChunkerSchema         string
	EmbeddingDimensions   int
	TotalChunkCount       int64
	DatabaseByteCount     uint64
	UpdatedAtUTC          time.Time
	Trawlers              []SemanticTrawlerCount
}

// ReadSemanticIndexStatus inspects semantic.db, model weight cache, and helper state.
func ReadSemanticIndexStatus(ctx context.Context, stateRoot string) (SemanticIndexStatus, error) {
	dbPath := ResolveSemanticDatabasePath(stateRoot)
	modelDir, _ := ResolveDefaultModelCacheDirectory()
	cached, weightBytes, _ := CheckDefaultModelCachedAndVerified(modelDir, false)
	helperClient := NewSemanticHelperClient(stateRoot)
	health, helperRunning, _ := helperClient.CheckRunningHealth(ctx)

	status := SemanticIndexStatus{
		SemanticDatabasePath:  dbPath,
		ModelIdentifier:       DefaultSemanticEmbeddingModelIdentifier,
		ModelDirectoryPath:    modelDir,
		ModelWeightsCached:    cached,
		ModelWeightsByteCount: weightBytes,
		HelperBinaryFound:     helperClient.HelperBinaryPath() != "",
		HelperRunning:         helperRunning,
		ChunkerSchema:         DefaultSemanticChunkerSchemaName,
		EmbeddingDimensions:   DefaultSemanticEmbeddingDimensions,
	}
	if health != nil {
		status.HelperModelLoaded = health.GetModelCurrentlyLoaded()
		status.HelperActiveMetalByte = health.GetActiveMetalMemoryBytes()
	}

	info, err := os.Stat(dbPath)
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.DatabaseExists = true
	status.DatabaseByteCount = uint64(info.Size())

	st, err := OpenSemanticIndexReadOnly(ctx, stateRoot)
	if err != nil || st == nil {
		return status, err
	}
	defer func() { _ = st.Close() }()

	meta, err := readSemanticMetadataMap(ctx, st.DB())
	if err != nil {
		return status, err
	}
	status.SemanticSearchEnabled = meta["enabled"] == "1"
	if v := meta["model_identifier"]; v != "" {
		status.ModelIdentifier = v
	}
	if v := meta["chunker_schema"]; v != "" {
		status.ChunkerSchema = v
	}
	if v := meta["embedding_dimensions"]; v != "" {
		if parsed, parseErr := strconv.Atoi(v); parseErr == nil && parsed > 0 {
			status.EmbeddingDimensions = parsed
		}
	}
	if ts := meta["updated_at_utc"]; ts != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
			status.UpdatedAtUTC = parsed
		}
	}

	rows, err := st.DB().QueryContext(ctx, `
		select trawler, max(trawler_display_name), count(*)
		from semantic_chunks
		group by trawler
		order by count(*) desc, trawler asc
	`)
	if err != nil {
		return status, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row SemanticTrawlerCount
		if err := rows.Scan(&row.TrawlerIdentity, &row.TrawlerDisplayName, &row.ChunkCount); err != nil {
			return status, err
		}
		status.TotalChunkCount += row.ChunkCount
		status.Trawlers = append(status.Trawlers, row)
	}
	return status, rows.Err()
}

// IsSemanticSearchEnabledAndReady returns true only when semantic.db exists,
// semantic search is enabled, and at least one embedded chunk is present.
func IsSemanticSearchEnabledAndReady(ctx context.Context, stateRoot string) (bool, error) {
	st, err := OpenSemanticIndexReadOnly(ctx, stateRoot)
	if err != nil || st == nil {
		return false, err
	}
	defer func() { _ = st.Close() }()
	var enabled string
	if err := st.DB().QueryRowContext(ctx, `select value from semantic_metadata where key = 'enabled'`).Scan(&enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if enabled != "1" {
		return false, nil
	}
	var hasChunk int
	if err := st.DB().QueryRowContext(ctx, `select 1 from semantic_chunks limit 1`).Scan(&hasChunk); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SetSemanticSearchEnabled updates the enabled flag inside semantic.db.
func SetSemanticSearchEnabled(ctx context.Context, stateRoot string, enabled bool) error {
	semanticWriteMutex.Lock()
	defer semanticWriteMutex.Unlock()

	st, err := OpenSemanticIndexReadWrite(ctx, stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	val := "0"
	if enabled {
		val = "1"
	}
	_, err = st.DB().ExecContext(ctx, `
		insert into semantic_metadata(key, value) values ('enabled', ?)
		on conflict(key) do update set value = excluded.value
	`, val)
	return err
}

// DeleteSemanticDatabase removes disposable semantic.db, its WAL/SHM sidecars,
// and its derived semantic.vec cache.
func DeleteSemanticDatabase(stateRoot string) error {
	base := ResolveSemanticDatabasePath(stateRoot)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		target := base + suffix
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", target, err)
		}
	}
	return removeSemanticVectorCacheFiles(stateRoot)
}

func readSemanticMetadataMap(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `select key, value from semantic_metadata`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string, 8)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, rows.Err()
}
