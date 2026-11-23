package semantic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

const (
	chatSessionGapSeconds         = int64(2 * 3600)
	chatSessionMaximumMessages    = 24
	chatSessionMaximumChars       = 1800
	singleRecordMaximumChars      = 1600
	embedBatchSizeForIndexBuilder = 32
)

// SemanticBuildOptions controls incremental or bounded semantic index builds.
type SemanticBuildOptions struct {
	WindowDuration  time.Duration // 0 means all history; >0 limits non-notes records to now-WindowDuration.
	Trawlers        []string      // Optional trawler identity filter.
	ForceFullRehash bool
}

// SemanticBuildReport summarizes one completed semantic index build pass.
type SemanticBuildReport struct {
	ModelIdentifier     string
	TotalChunksInIndex  int64
	ChunksNewlyEmbedded int64
	ChunksUnchanged     int64
	ChunksRemoved       int64
	Elapsed             time.Duration
	DatabaseByteCount   uint64
	Trawlers            []SemanticTrawlerCount
}

type sourceRecordForChunking struct {
	trawler          string
	trawlerDisplay   string
	recordKind       string
	sourceRole       string
	canonicalRef     string
	resolvedRef      string
	conversationKey  string
	displayContext   string
	occurredUnix     int64
	hasTimeOfDay     int
	utcOffsetSeconds int
	title            string
	peopleText       string
	bodyText         string
	metaText         string
}

type semanticChunkCandidate struct {
	chunkKey         string
	trawler          string
	trawlerDisplay   string
	recordKind       string
	conversationKey  string
	displayContext   string
	repCanonicalRef  string
	repResolvedRef   string
	occurredUnix     int64
	hasTimeOfDay     int
	utcOffsetSeconds int
	title            string
	peopleText       string
	bodyPreview      string
	embedText        string
	messageCount     int
	sourceRecordHash string
	memberRefs       []string
}

// BuildOrUpdateSemanticIndex reads observed source records exclusively from
// <stateRoot>/index.db, forms 2-hour chat sessions and document chunks, embeds
// only new or changed chunks through the native Swift/MLX helper, and commits
// the disposable results into <stateRoot>/semantic.db.
func BuildOrUpdateSemanticIndex(
	ctx context.Context,
	stateRoot string,
	opts SemanticBuildOptions,
) (SemanticBuildReport, error) {
	started := time.Now()
	indexStore, err := unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil {
		return SemanticBuildReport{}, err
	}
	if indexStore == nil {
		return SemanticBuildReport{}, errors.New("unified index (index.db) does not exist yet; run `trawl index rebuild` first")
	}
	defer func() { _ = indexStore.Close() }()

	modelDir, err := ResolveDefaultModelCacheDirectory()
	if err != nil {
		return SemanticBuildReport{}, err
	}
	cached, _, err := CheckDefaultModelCachedAndVerified(modelDir, false)
	if err != nil {
		return SemanticBuildReport{}, err
	}
	if !cached {
		return SemanticBuildReport{}, errors.New("semantic model weights are not cached yet; run `trawl semantic enable` first")
	}

	candidates, err := collectSemanticChunksFromUnifiedIndex(ctx, indexStore.DB(), opts)
	if err != nil {
		return SemanticBuildReport{}, err
	}

	semanticWriteMutex.Lock()
	defer semanticWriteMutex.Unlock()

	semStore, err := OpenSemanticIndexReadWrite(ctx, stateRoot)
	if err != nil {
		return SemanticBuildReport{}, err
	}
	defer func() { _ = semStore.Close() }()

	existingMeta, err := readSemanticMetadataMap(ctx, semStore.DB())
	if err != nil {
		return SemanticBuildReport{}, err
	}
	modelChanged := existingMeta["model_identifier"] != DefaultSemanticEmbeddingModelIdentifier ||
		existingMeta["chunker_schema"] != DefaultSemanticChunkerSchemaName ||
		existingMeta["embedding_dimensions"] != strconv.Itoa(DefaultSemanticEmbeddingDimensions)
	if opts.ForceFullRehash || modelChanged {
		if err := clearAllSemanticChunks(ctx, semStore.DB()); err != nil {
			return SemanticBuildReport{}, err
		}
	}

	existingHashes, err := loadExistingChunkHashes(ctx, semStore.DB())
	if err != nil {
		return SemanticBuildReport{}, err
	}

	desiredKeys := make(map[string]struct{}, len(candidates))
	var toEmbed []semanticChunkCandidate
	var unchanged int64
	for _, cand := range candidates {
		desiredKeys[cand.chunkKey] = struct{}{}
		if prevHash, ok := existingHashes[cand.chunkKey]; ok && prevHash == cand.sourceRecordHash {
			unchanged++
			continue
		}
		toEmbed = append(toEmbed, cand)
	}

	var staleKeys []string
	if len(opts.Trawlers) == 0 {
		for existingKey := range existingHashes {
			if _, keep := desiredKeys[existingKey]; !keep {
				staleKeys = append(staleKeys, existingKey)
			}
		}
	}

	helper := NewSemanticHelperClient(stateRoot)
	embeddedVectors := make([][]byte, len(toEmbed))
	for start := 0; start < len(toEmbed); start += embedBatchSizeForIndexBuilder {
		end := min(start+embedBatchSizeForIndexBuilder, len(toEmbed))
		batchTexts := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			batchTexts = append(batchTexts, toEmbed[i].embedText)
		}
		vecs, _, embedErr := helper.EmbedTexts(
			ctx,
			modelDir,
			DefaultSemanticEmbeddingModelIdentifier,
			batchTexts,
			DefaultSemanticEmbeddingDimensions,
			DefaultSemanticMaximumInputTokenCount,
			false,
		)
		if embedErr != nil {
			return SemanticBuildReport{}, embedErr
		}
		for i := range vecs {
			embeddedVectors[start+i] = vecs[i]
		}
	}

	if err := commitSemanticChunkChanges(ctx, semStore.DB(), toEmbed, embeddedVectors, staleKeys, opts.WindowDuration); err != nil {
		return SemanticBuildReport{}, err
	}
	_ = semStore.Close()

	status, err := ReadSemanticIndexStatus(ctx, stateRoot)
	if err != nil {
		return SemanticBuildReport{}, err
	}
	return SemanticBuildReport{
		ModelIdentifier:     status.ModelIdentifier,
		TotalChunksInIndex:  status.TotalChunkCount,
		ChunksNewlyEmbedded: int64(len(toEmbed)),
		ChunksUnchanged:     unchanged,
		ChunksRemoved:       int64(len(staleKeys)),
		Elapsed:             time.Since(started),
		DatabaseByteCount:   status.DatabaseByteCount,
		Trawlers:            status.Trawlers,
	}, nil
}

func collectSemanticChunksFromUnifiedIndex(
	ctx context.Context,
	indexDB *sql.DB,
	opts SemanticBuildOptions,
) ([]semanticChunkCandidate, error) {
	cutoffUnix := int64(0)
	if opts.WindowDuration > 0 {
		cutoffUnix = time.Now().Add(-opts.WindowDuration).Unix()
	}

	whereParts := []string{
		`trawler <> 'money'`,
		`source_role not in ('like', 'other')`,
	}
	var args []any
	if len(opts.Trawlers) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(opts.Trawlers)), ",")
		whereParts = append(whereParts, `trawler in (`+ph+`)`)
		for _, t := range opts.Trawlers {
			args = append(args, strings.ToLower(strings.TrimSpace(t)))
		}
	}
	if cutoffUnix > 0 {
		// Always retain all Notes alongside the requested recent window.
		whereParts = append(whereParts, `(trawler = 'notes' OR coalesce(occurred_unix, 0) >= ?)`)
		args = append(args, cutoffUnix)
	}

	rows, err := indexDB.QueryContext(ctx, `
		select trawler, trawler_display_name, record_kind, source_role,
		       canonical_ref, resolved_ref, conversation_key, display_context,
		       coalesce(occurred_unix, 0), has_time_of_day, utc_offset_seconds,
		       title, people_text, body_text, meta_text
		from indexed_archive_records
		where `+strings.Join(whereParts, " AND ")+`
		order by trawler, conversation_key, coalesce(occurred_unix, 0) asc, canonical_ref asc
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("read records from index.db for semantic chunking: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var chunks []semanticChunkCandidate
	var activeSession []sourceRecordForChunking
	flushSession := func() {
		if len(activeSession) == 0 {
			return
		}
		if chunk, ok := buildChatSessionChunk(activeSession); ok {
			chunks = append(chunks, chunk)
		}
		activeSession = activeSession[:0]
	}

	for rows.Next() {
		var r sourceRecordForChunking
		if err := rows.Scan(
			&r.trawler, &r.trawlerDisplay, &r.recordKind, &r.sourceRole,
			&r.canonicalRef, &r.resolvedRef, &r.conversationKey, &r.displayContext,
			&r.occurredUnix, &r.hasTimeOfDay, &r.utcOffsetSeconds,
			&r.title, &r.peopleText, &r.bodyText, &r.metaText,
		); err != nil {
			return nil, fmt.Errorf("scan record for semantic chunking: %w", err)
		}
		if isSessionChunkedRecord(r) {
			if shouldBreakChatSession(activeSession, r) {
				flushSession()
			}
			activeSession = append(activeSession, r)
			continue
		}
		flushSession()
		if chunk, ok := buildDocumentChunk(r); ok {
			chunks = append(chunks, chunk)
		}
	}
	flushSession()
	return chunks, rows.Err()
}

func isSessionChunkedRecord(r sourceRecordForChunking) bool {
	if r.recordKind == "message" {
		return true
	}
	return r.trawler == "imessage" || r.trawler == "whatsapp" || r.trawler == "telegram"
}

func shouldBreakChatSession(session []sourceRecordForChunking, next sourceRecordForChunking) bool {
	if len(session) == 0 {
		return false
	}
	last := session[len(session)-1]
	if last.trawler != next.trawler || last.conversationKey != next.conversationKey {
		return true
	}
	if len(session) >= chatSessionMaximumMessages {
		return true
	}
	if next.occurredUnix-last.occurredUnix > chatSessionGapSeconds {
		return true
	}
	totalChars := 0
	for _, m := range session {
		totalChars += len(m.bodyText)
	}
	return totalChars+len(next.bodyText) > chatSessionMaximumChars
}

func buildChatSessionChunk(messages []sourceRecordForChunking) (semanticChunkCandidate, bool) {
	first := messages[0]
	last := messages[len(messages)-1]
	var lines []string
	memberRefs := make([]string, 0, len(messages))
	peopleSeen := make(map[string]struct{}, 4)
	var peopleList []string
	for _, m := range messages {
		memberRefs = append(memberRefs, m.canonicalRef)
		text := strings.TrimSpace(m.bodyText)
		if text == "" {
			continue
		}
		speaker := firstNonEmptySemantic(m.peopleText, m.displayContext)
		if speaker != "" {
			if _, ok := peopleSeen[speaker]; !ok {
				peopleSeen[speaker] = struct{}{}
				peopleList = append(peopleList, speaker)
			}
			lines = append(lines, speaker+": "+text)
		} else {
			lines = append(lines, text)
		}
	}
	if len(lines) == 0 {
		return semanticChunkCandidate{}, false
	}
	header := firstNonEmptySemantic(last.displayContext, last.conversationKey, last.trawlerDisplay)
	joined := unifiedindex.TruncateRunes(strings.Join(lines, "\n"), chatSessionMaximumChars)
	embedText := header + "\n" + joined
	preview := unifiedindex.TruncateRunes(strings.Join(lines, " · "), 240)
	chunkKey := fmt.Sprintf("session:%s:%s:%s", first.trawler, first.conversationKey, first.canonicalRef)
	return semanticChunkCandidate{
		chunkKey:         chunkKey,
		trawler:          last.trawler,
		trawlerDisplay:   last.trawlerDisplay,
		recordKind:       last.recordKind,
		conversationKey:  last.conversationKey,
		displayContext:   header,
		repCanonicalRef:  last.canonicalRef,
		repResolvedRef:   last.resolvedRef,
		occurredUnix:     last.occurredUnix,
		hasTimeOfDay:     last.hasTimeOfDay,
		utcOffsetSeconds: last.utcOffsetSeconds,
		title:            header,
		peopleText:       strings.Join(peopleList, ", "),
		bodyPreview:      preview,
		embedText:        embedText,
		messageCount:     len(messages),
		sourceRecordHash: hashChunkInput(chunkKey, embedText, last.occurredUnix),
		memberRefs:       memberRefs,
	}, true
}

func buildDocumentChunk(r sourceRecordForChunking) (semanticChunkCandidate, bool) {
	title := strings.TrimSpace(r.title)
	body := strings.TrimSpace(r.bodyText)
	if title == "" && body == "" {
		return semanticChunkCandidate{}, false
	}
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if p := strings.TrimSpace(r.peopleText); p != "" {
		parts = append(parts, unifiedindex.TruncateRunes(p, 160))
	}
	if body != "" {
		parts = append(parts, unifiedindex.TruncateRunes(body, singleRecordMaximumChars))
	}
	embedText := strings.Join(parts, "\n")
	preview := body
	if preview == "" {
		preview = title
	}
	chunkKey := "doc:" + r.trawler + ":" + r.canonicalRef
	return semanticChunkCandidate{
		chunkKey:         chunkKey,
		trawler:          r.trawler,
		trawlerDisplay:   r.trawlerDisplay,
		recordKind:       r.recordKind,
		conversationKey:  r.conversationKey,
		displayContext:   r.displayContext,
		repCanonicalRef:  r.canonicalRef,
		repResolvedRef:   r.resolvedRef,
		occurredUnix:     r.occurredUnix,
		hasTimeOfDay:     r.hasTimeOfDay,
		utcOffsetSeconds: r.utcOffsetSeconds,
		title:            title,
		peopleText:       r.peopleText,
		bodyPreview:      unifiedindex.TruncateRunes(preview, 240),
		embedText:        embedText,
		messageCount:     1,
		sourceRecordHash: hashChunkInput(chunkKey, embedText, r.occurredUnix),
		memberRefs:       []string{r.canonicalRef},
	}, true
}

func hashChunkInput(chunkKey, embedText string, occurredUnix int64) string {
	h := sha256.New()
	_, _ = h.Write([]byte(chunkKey))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strconv.FormatInt(occurredUnix, 10)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(embedText))
	return hex.EncodeToString(h.Sum(nil))
}

func firstNonEmptySemantic(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clearAllSemanticChunks(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `delete from semantic_chunk_members`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `delete from semantic_chunks`)
	return err
}

func loadExistingChunkHashes(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `select chunk_key, source_record_hash from semantic_chunks`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]string, 1024)
	for rows.Next() {
		var key, hash string
		if err := rows.Scan(&key, &hash); err != nil {
			return nil, err
		}
		out[key] = hash
	}
	return out, rows.Err()
}

// BuildOrUpdateSemanticIndexWithStoredWindow refreshes semantic.db using the
// history window persisted from the user's last explicit enable/build call.
func BuildOrUpdateSemanticIndexWithStoredWindow(
	ctx context.Context,
	stateRoot string,
) (SemanticBuildReport, error) {
	windowDuration := 60 * 24 * time.Hour
	if semStore, err := OpenSemanticIndexReadOnly(ctx, stateRoot); err == nil && semStore != nil {
		if meta, metaErr := readSemanticMetadataMap(ctx, semStore.DB()); metaErr == nil {
			if raw, ok := meta["window_duration_seconds"]; ok {
				if secs, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil && secs >= 0 {
					windowDuration = time.Duration(secs) * time.Second
				}
			}
		}
		_ = semStore.Close()
	}
	return BuildOrUpdateSemanticIndex(ctx, stateRoot, SemanticBuildOptions{
		WindowDuration: windowDuration,
	})
}

func commitSemanticChunkChanges(
	ctx context.Context,
	db *sql.DB,
	upserts []semanticChunkCandidate,
	vectors [][]byte,
	staleKeys []string,
	windowDuration time.Duration,
) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if len(staleKeys) > 0 {
		if err := deleteStaleChunksTx(ctx, tx, staleKeys); err != nil {
			return err
		}
	}
	if len(upserts) > 0 {
		if err := upsertChunksTx(ctx, tx, upserts, vectors); err != nil {
			return err
		}
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	metaPairs := [][2]string{
		{"enabled", "1"},
		{"model_identifier", DefaultSemanticEmbeddingModelIdentifier},
		{"chunker_schema", DefaultSemanticChunkerSchemaName},
		{"embedding_dimensions", strconv.Itoa(DefaultSemanticEmbeddingDimensions)},
		{"window_duration_seconds", strconv.FormatInt(int64(windowDuration/time.Second), 10)},
		{"updated_at_utc", nowUTC},
	}
	for _, pair := range metaPairs {
		if _, err := tx.ExecContext(ctx, `
			insert into semantic_metadata(key, value) values (?, ?)
			on conflict(key) do update set value = excluded.value
		`, pair[0], pair[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func deleteStaleChunksTx(ctx context.Context, tx *sql.Tx, staleKeys []string) error {
	for start := 0; start < len(staleKeys); start += 450 {
		end := min(start+450, len(staleKeys))
		chunk := staleKeys[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk))
		for _, k := range chunk {
			args = append(args, k)
		}
		if _, err := tx.ExecContext(ctx, `
			delete from semantic_chunk_members
			where chunk_id in (select chunk_id from semantic_chunks where chunk_key in (`+ph+`))
		`, args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `delete from semantic_chunks where chunk_key in (`+ph+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

func upsertChunksTx(
	ctx context.Context,
	tx *sql.Tx,
	upserts []semanticChunkCandidate,
	vectors [][]byte,
) error {
	upsertStmt, err := tx.PrepareContext(ctx, `
		insert into semantic_chunks(
			chunk_key, trawler, trawler_display_name, record_kind,
			conversation_key, display_context, representative_canonical_ref, representative_resolved_ref,
			occurred_unix, has_time_of_day, utc_offset_seconds,
			title, people_text, body_preview, message_count, source_record_hash, embedding_int8
		) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(chunk_key) do update set
			trawler_display_name = excluded.trawler_display_name,
			record_kind = excluded.record_kind,
			conversation_key = excluded.conversation_key,
			display_context = excluded.display_context,
			representative_canonical_ref = excluded.representative_canonical_ref,
			representative_resolved_ref = excluded.representative_resolved_ref,
			occurred_unix = excluded.occurred_unix,
			has_time_of_day = excluded.has_time_of_day,
			utc_offset_seconds = excluded.utc_offset_seconds,
			title = excluded.title,
			people_text = excluded.people_text,
			body_preview = excluded.body_preview,
			message_count = excluded.message_count,
			source_record_hash = excluded.source_record_hash,
			embedding_int8 = excluded.embedding_int8
		returning chunk_id
	`)
	if err != nil {
		return err
	}
	defer func() { _ = upsertStmt.Close() }()

	delMembersStmt, err := tx.PrepareContext(ctx, `delete from semantic_chunk_members where chunk_id = ?`)
	if err != nil {
		return err
	}
	defer func() { _ = delMembersStmt.Close() }()

	insMemberStmt, err := tx.PrepareContext(ctx, `insert into semantic_chunk_members(chunk_id, trawler, canonical_ref) values (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = insMemberStmt.Close() }()

	for i, c := range upserts {
		var chunkID int64
		if err := upsertStmt.QueryRowContext(
			ctx,
			c.chunkKey, c.trawler, c.trawlerDisplay, c.recordKind,
			c.conversationKey, c.displayContext, c.repCanonicalRef, c.repResolvedRef,
			c.occurredUnix, c.hasTimeOfDay, c.utcOffsetSeconds,
			c.title, c.peopleText, c.bodyPreview, c.messageCount, c.sourceRecordHash, vectors[i],
		).Scan(&chunkID); err != nil {
			return err
		}
		if _, err := delMembersStmt.ExecContext(ctx, chunkID); err != nil {
			return err
		}
		for _, ref := range c.memberRefs {
			if _, err := insMemberStmt.ExecContext(ctx, chunkID, c.trawler, ref); err != nil {
				return err
			}
		}
	}
	return nil
}
