package semantic

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	// SemanticVectorCacheFilename is the disposable flat memory-mappable int8
	// vector cache derived from semantic.db for low-latency hybrid search.
	SemanticVectorCacheFilename = "semantic.vec"

	semanticVectorCacheMagic               = "OTSVEC\x00\x00"
	semanticVectorCacheHeaderByteLength    = 128
	semanticVectorCacheEntryByteLength     = 32
	semanticVectorCacheUpdatedAtByteLength = 32
	semanticVectorCacheMaxTrawlerCount     = 1024
	semanticVectorCacheMinChunksPerWorker  = 4096
)

// ResolveSemanticVectorCachePath returns <stateRoot>/semantic.vec.
func ResolveSemanticVectorCachePath(stateRoot string) string {
	return filepath.Join(stateRoot, SemanticVectorCacheFilename)
}

type semanticDatabaseFileStamp struct {
	databaseSizeBytes       int64
	databaseModTimeUnixNano int64
	walSizeBytes            int64
	walModTimeUnixNano      int64
}

func readSemanticDatabaseFileStamp(stateRoot string) (semanticDatabaseFileStamp, error) {
	dbPath := ResolveSemanticDatabasePath(stateRoot)
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		return semanticDatabaseFileStamp{}, err
	}
	stamp := semanticDatabaseFileStamp{
		databaseSizeBytes:       dbInfo.Size(),
		databaseModTimeUnixNano: dbInfo.ModTime().UnixNano(),
		walSizeBytes:            -1,
		walModTimeUnixNano:      0,
	}
	if walInfo, walErr := os.Stat(dbPath + "-wal"); walErr == nil {
		stamp.walSizeBytes = walInfo.Size()
		stamp.walModTimeUnixNano = walInfo.ModTime().UnixNano()
	} else if !errors.Is(walErr, os.ErrNotExist) {
		return semanticDatabaseFileStamp{}, walErr
	}
	return stamp, nil
}

type semanticVectorCache struct {
	mapped              *mappedSemanticVectorFile
	embeddingDimensions int
	chunkCount          int
	entriesByteOffset   int
	trawlerIdentities   []string
}

func (c *semanticVectorCache) Close() error {
	if c == nil || c.mapped == nil {
		return nil
	}
	return c.mapped.Close()
}

type scoredSemanticChunkID struct {
	chunkID          int64
	occurredUnix     int64
	conversationHash uint64
	dotProduct       int32
}

// RebuildSemanticVectorCache regenerates disposable <stateRoot>/semantic.vec
// from <stateRoot>/semantic.db using an atomic file swap.
func RebuildSemanticVectorCache(ctx context.Context, stateRoot string) error {
	semStore, err := OpenSemanticIndexReadOnly(ctx, stateRoot)
	if err != nil || semStore == nil {
		return err
	}
	defer func() { _ = semStore.Close() }()

	meta, err := readSemanticMetadataMap(ctx, semStore.DB())
	if err != nil {
		return err
	}
	dims := DefaultSemanticEmbeddingDimensions
	if raw := meta["embedding_dimensions"]; raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil && parsed > 0 {
			dims = parsed
		}
	}
	_, err = rebuildSemanticVectorCacheFromOpenDB(ctx, stateRoot, semStore.DB(), meta["updated_at_utc"], dims)
	return err
}

func openOrRebuildSemanticVectorCache(
	ctx context.Context,
	stateRoot string,
	semDB *sql.DB,
	updatedAtUTC string,
	embeddingDimensions int,
) (*semanticVectorCache, error) {
	stamp, err := readSemanticDatabaseFileStamp(stateRoot)
	if err != nil {
		return nil, err
	}
	vecPath := ResolveSemanticVectorCachePath(stateRoot)
	if cache, ok := openValidatedSemanticVectorCache(vecPath, stamp, updatedAtUTC, embeddingDimensions); ok {
		return cache, nil
	}
	rebuiltStamp, err := rebuildSemanticVectorCacheFromOpenDB(ctx, stateRoot, semDB, updatedAtUTC, embeddingDimensions)
	if err != nil {
		return nil, err
	}
	if cache, ok := openValidatedSemanticVectorCache(vecPath, rebuiltStamp, updatedAtUTC, embeddingDimensions); ok {
		return cache, nil
	}
	return nil, errors.New("rebuilt semantic vector cache failed validation")
}

func openValidatedSemanticVectorCache(
	vecPath string,
	expectedStamp semanticDatabaseFileStamp,
	expectedUpdatedAtUTC string,
	expectedDimensions int,
) (*semanticVectorCache, bool) {
	file, err := os.Open(vecPath) // #nosec G304 -- derived path inside stateRoot.
	if err != nil {
		return nil, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < semanticVectorCacheHeaderByteLength {
		_ = file.Close()
		return nil, false
	}
	mapped, err := mapSemanticVectorFileReadOnly(file, info.Size())
	_ = file.Close()
	if err != nil {
		return nil, false
	}

	cache, ok := parseAndValidateMappedSemanticVectorCache(mapped, expectedStamp, expectedUpdatedAtUTC, expectedDimensions)
	if !ok {
		_ = mapped.Close()
		return nil, false
	}
	return cache, true
}

func parseAndValidateMappedSemanticVectorCache(
	mapped *mappedSemanticVectorFile,
	expectedStamp semanticDatabaseFileStamp,
	expectedUpdatedAtUTC string,
	expectedDimensions int,
) (*semanticVectorCache, bool) {
	data := mapped.data
	if len(data) < semanticVectorCacheHeaderByteLength {
		return nil, false
	}
	if string(data[0:8]) != semanticVectorCacheMagic {
		return nil, false
	}
	dims := int(binary.LittleEndian.Uint32(data[8:12]))
	trawlerCount := int(binary.LittleEndian.Uint32(data[12:16]))
	chunkCountU64 := binary.LittleEndian.Uint64(data[16:24])
	entriesOffU64 := binary.LittleEndian.Uint64(data[24:32])
	trawlerOffU64 := binary.LittleEndian.Uint64(data[32:40])
	trawlerLenU64 := binary.LittleEndian.Uint64(data[40:48])
	dbSize := int64(binary.LittleEndian.Uint64(data[48:56]))
	dbMod := int64(binary.LittleEndian.Uint64(data[56:64]))
	walSize := int64(binary.LittleEndian.Uint64(data[64:72]))
	walMod := int64(binary.LittleEndian.Uint64(data[72:80]))
	headerUpdated := strings.TrimRight(string(data[80:80+semanticVectorCacheUpdatedAtByteLength]), "\x00")

	if dims != expectedDimensions || dims <= 0 {
		return nil, false
	}
	if trawlerCount < 0 || trawlerCount > semanticVectorCacheMaxTrawlerCount {
		return nil, false
	}
	if dbSize != expectedStamp.databaseSizeBytes ||
		dbMod != expectedStamp.databaseModTimeUnixNano ||
		walSize != expectedStamp.walSizeBytes ||
		walMod != expectedStamp.walModTimeUnixNano {
		return nil, false
	}
	if headerUpdated != truncateASCIIString(expectedUpdatedAtUTC, semanticVectorCacheUpdatedAtByteLength) {
		return nil, false
	}

	vectorsEnd := uint64(semanticVectorCacheHeaderByteLength) + chunkCountU64*uint64(dims)
	expectedEntriesOff := alignUp8Uint64(vectorsEnd)
	expectedTrawlerOff := expectedEntriesOff + chunkCountU64*semanticVectorCacheEntryByteLength
	if entriesOffU64 != expectedEntriesOff || trawlerOffU64 != expectedTrawlerOff {
		return nil, false
	}
	if trawlerOffU64+trawlerLenU64 != uint64(len(data)) {
		return nil, false
	}

	trawlers, ok := parseTrawlerIdentityTable(data[trawlerOffU64:], trawlerCount)
	if !ok {
		return nil, false
	}

	return &semanticVectorCache{
		mapped:              mapped,
		embeddingDimensions: dims,
		chunkCount:          int(chunkCountU64),
		entriesByteOffset:   int(entriesOffU64),
		trawlerIdentities:   trawlers,
	}, true
}

func parseTrawlerIdentityTable(tableBytes []byte, count int) ([]string, bool) {
	trawlers := make([]string, 0, count)
	offset := 0
	for range count {
		if offset+2 > len(tableBytes) {
			return nil, false
		}
		nameLen := int(binary.LittleEndian.Uint16(tableBytes[offset : offset+2]))
		offset += 2
		if offset+nameLen > len(tableBytes) {
			return nil, false
		}
		trawlers = append(trawlers, string(tableBytes[offset:offset+nameLen]))
		offset += nameLen
	}
	if offset != len(tableBytes) {
		return nil, false
	}
	return trawlers, true
}

func rebuildSemanticVectorCacheFromOpenDB(
	ctx context.Context,
	stateRoot string,
	semDB *sql.DB,
	updatedAtUTC string,
	embeddingDimensions int,
) (semanticDatabaseFileStamp, error) {
	// Capture source file stamp before scanning so any concurrent database write
	// invalidates the rebuilt file on the next read rather than masking newer data.
	stamp, err := readSemanticDatabaseFileStamp(stateRoot)
	if err != nil {
		return semanticDatabaseFileStamp{}, err
	}

	rows, err := semDB.QueryContext(ctx, `
		select chunk_id, trawler, conversation_key, representative_resolved_ref, occurred_unix, embedding_int8
		from semantic_chunks
		order by chunk_id asc
	`)
	if err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("read semantic chunks for vector cache: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tmpFile, err := os.CreateTemp(stateRoot, SemanticVectorCacheFilename+".*.tmp")
	if err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("create temp semantic vector cache: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	writer := bufio.NewWriterSize(tmpFile, 256*1024)
	var zeroHeader [semanticVectorCacheHeaderByteLength]byte
	if _, err := writer.Write(zeroHeader[:]); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("write placeholder semantic vector cache header: %w", err)
	}

	trawlerIndexByIdentity := make(map[string]uint16, 16)
	var trawlerIdentities []string
	entriesBuf := make([]byte, 0, 1024*semanticVectorCacheEntryByteLength)
	var entryScratch [semanticVectorCacheEntryByteLength]byte
	var chunkCount uint64

	for rows.Next() {
		var (
			chunkID         int64
			trawler         string
			conversationKey string
			repResolvedRef  string
			occurredUnix    int64
			embeddingBlob   []byte
		)
		if err := rows.Scan(&chunkID, &trawler, &conversationKey, &repResolvedRef, &occurredUnix, &embeddingBlob); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("scan semantic chunk for vector cache: %w", err)
		}
		if len(embeddingBlob) != embeddingDimensions {
			continue
		}
		trawlerIdx, exists := trawlerIndexByIdentity[trawler]
		if !exists {
			if len(trawlerIdentities) >= semanticVectorCacheMaxTrawlerCount {
				return semanticDatabaseFileStamp{}, errors.New("semantic vector cache trawler table exceeded maximum capacity")
			}
			trawlerIdx = uint16(len(trawlerIdentities))
			trawlerIndexByIdentity[trawler] = trawlerIdx
			trawlerIdentities = append(trawlerIdentities, trawler)
		}
		if _, err := writer.Write(embeddingBlob); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("write semantic vector bytes: %w", err)
		}

		convHash := hashSemanticConversationDedupKey(trawler, conversationKey, repResolvedRef)
		binary.LittleEndian.PutUint64(entryScratch[0:8], uint64(chunkID))
		binary.LittleEndian.PutUint64(entryScratch[8:16], uint64(occurredUnix))
		binary.LittleEndian.PutUint64(entryScratch[16:24], convHash)
		binary.LittleEndian.PutUint16(entryScratch[24:26], trawlerIdx)
		entriesBuf = append(entriesBuf, entryScratch[:]...)
		chunkCount++
	}
	if err := rows.Err(); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("iterate semantic chunks for vector cache: %w", err)
	}

	vectorsEnd := uint64(semanticVectorCacheHeaderByteLength) + chunkCount*uint64(embeddingDimensions)
	entriesOff := alignUp8Uint64(vectorsEnd)
	if pad := entriesOff - vectorsEnd; pad > 0 {
		var padZeros [8]byte
		if _, err := writer.Write(padZeros[:pad]); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("pad semantic vector cache: %w", err)
		}
	}
	if len(entriesBuf) > 0 {
		if _, err := writer.Write(entriesBuf); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("write semantic vector cache metadata entries: %w", err)
		}
	}

	trawlerTableOff := entriesOff + chunkCount*semanticVectorCacheEntryByteLength
	var trawlerTableLen uint64
	var lenScratch [2]byte
	for _, trawler := range trawlerIdentities {
		binary.LittleEndian.PutUint16(lenScratch[:], uint16(len(trawler)))
		if _, err := writer.Write(lenScratch[:]); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("write trawler identity length: %w", err)
		}
		if _, err := writer.WriteString(trawler); err != nil {
			return semanticDatabaseFileStamp{}, fmt.Errorf("write trawler identity: %w", err)
		}
		trawlerTableLen += 2 + uint64(len(trawler))
	}
	if err := writer.Flush(); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("flush semantic vector cache: %w", err)
	}

	var header [semanticVectorCacheHeaderByteLength]byte
	copy(header[0:8], semanticVectorCacheMagic)
	binary.LittleEndian.PutUint32(header[8:12], uint32(embeddingDimensions))
	binary.LittleEndian.PutUint32(header[12:16], uint32(len(trawlerIdentities)))
	binary.LittleEndian.PutUint64(header[16:24], chunkCount)
	binary.LittleEndian.PutUint64(header[24:32], entriesOff)
	binary.LittleEndian.PutUint64(header[32:40], trawlerTableOff)
	binary.LittleEndian.PutUint64(header[40:48], trawlerTableLen)
	binary.LittleEndian.PutUint64(header[48:56], uint64(stamp.databaseSizeBytes))
	binary.LittleEndian.PutUint64(header[56:64], uint64(stamp.databaseModTimeUnixNano))
	binary.LittleEndian.PutUint64(header[64:72], uint64(stamp.walSizeBytes))
	binary.LittleEndian.PutUint64(header[72:80], uint64(stamp.walModTimeUnixNano))
	copy(header[80:80+semanticVectorCacheUpdatedAtByteLength], truncateASCIIString(updatedAtUTC, semanticVectorCacheUpdatedAtByteLength))

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("seek semantic vector cache header: %w", err)
	}
	if _, err := tmpFile.Write(header[:]); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("write semantic vector cache header: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return semanticDatabaseFileStamp{}, fmt.Errorf("close semantic vector cache temp file: %w", err)
	}

	vecPath := ResolveSemanticVectorCachePath(stateRoot)
	if err := os.Rename(tmpPath, vecPath); err != nil {
		_ = os.Remove(tmpPath)
		cleanupTemp = false
		return semanticDatabaseFileStamp{}, fmt.Errorf("install semantic vector cache: %w", err)
	}
	cleanupTemp = false
	return stamp, nil
}

func (c *semanticVectorCache) scanTopChunks(
	queryVec []byte,
	q SemanticSearchQuery,
	minDotProduct int32,
	poolLimit int,
) []scoredSemanticChunkID {
	if c == nil || c.chunkCount == 0 || len(queryVec) != c.embeddingDimensions {
		return nil
	}

	hasTrawlerFilter := len(q.Trawlers) > 0
	var allowedTrawlers []bool
	if hasTrawlerFilter {
		requested := make(map[string]struct{}, len(q.Trawlers))
		for _, t := range q.Trawlers {
			if normalized := strings.ToLower(strings.TrimSpace(t)); normalized != "" {
				requested[normalized] = struct{}{}
			}
		}
		allowedTrawlers = make([]bool, len(c.trawlerIdentities))
		matchedCount := 0
		for i, identity := range c.trawlerIdentities {
			if _, ok := requested[identity]; ok {
				allowedTrawlers[i] = true
				matchedCount++
			}
		}
		if matchedCount == 0 {
			return nil
		}
		if matchedCount == len(c.trawlerIdentities) {
			hasTrawlerFilter = false
		}
	}

	hasAfter := q.AfterUnix != nil
	var afterUnix int64
	if hasAfter {
		afterUnix = *q.AfterUnix
	}
	hasBefore := q.BeforeUnix != nil
	var beforeUnix int64
	if hasBefore {
		beforeUnix = *q.BeforeUnix
	}
	hasFilter := hasTrawlerFilter || hasAfter || hasBefore

	data := c.mapped.data
	dims := c.embeddingDimensions
	entriesOff := c.entriesByteOffset
	vectors := data[semanticVectorCacheHeaderByteLength : semanticVectorCacheHeaderByteLength+c.chunkCount*dims]
	entries := data[entriesOff : entriesOff+c.chunkCount*semanticVectorCacheEntryByteLength]

	scanRange := func(start, end int) []scoredSemanticChunkID {
		var localHits []scoredSemanticChunkID
		if !hasFilter {
			vecOff := start * dims
			for idx := start; idx < end; idx++ {
				dot := dotProductInt8Bytes(queryVec, vectors[vecOff:vecOff+dims])
				vecOff += dims
				if dot < minDotProduct {
					continue
				}
				eOff := idx * semanticVectorCacheEntryByteLength
				localHits = append(localHits, scoredSemanticChunkID{
					chunkID:          int64(binary.LittleEndian.Uint64(entries[eOff : eOff+8])),
					occurredUnix:     int64(binary.LittleEndian.Uint64(entries[eOff+8 : eOff+16])),
					conversationHash: binary.LittleEndian.Uint64(entries[eOff+16 : eOff+24]),
					dotProduct:       dot,
				})
			}
			return localHits
		}

		for idx := start; idx < end; idx++ {
			eOff := idx * semanticVectorCacheEntryByteLength
			if hasTrawlerFilter {
				trawlerIdx := int(binary.LittleEndian.Uint16(entries[eOff+24 : eOff+26]))
				if trawlerIdx >= len(allowedTrawlers) || !allowedTrawlers[trawlerIdx] {
					continue
				}
			}
			occurredUnix := int64(binary.LittleEndian.Uint64(entries[eOff+8 : eOff+16]))
			if hasAfter && occurredUnix < afterUnix {
				continue
			}
			if hasBefore && occurredUnix > beforeUnix {
				continue
			}
			vecOff := idx * dims
			dot := dotProductInt8Bytes(queryVec, vectors[vecOff:vecOff+dims])
			if dot < minDotProduct {
				continue
			}
			localHits = append(localHits, scoredSemanticChunkID{
				chunkID:          int64(binary.LittleEndian.Uint64(entries[eOff : eOff+8])),
				occurredUnix:     occurredUnix,
				conversationHash: binary.LittleEndian.Uint64(entries[eOff+16 : eOff+24]),
				dotProduct:       dot,
			})
		}
		return localHits
	}

	workerCount := min(runtime.GOMAXPROCS(0), max(1, c.chunkCount/semanticVectorCacheMinChunksPerWorker))
	var candidates []scoredSemanticChunkID
	if workerCount <= 1 {
		candidates = scanRange(0, c.chunkCount)
	} else {
		perWorker := make([][]scoredSemanticChunkID, workerCount)
		stride := (c.chunkCount + workerCount - 1) / workerCount
		var wg sync.WaitGroup
		for workerIdx := range workerCount {
			start := workerIdx * stride
			if start >= c.chunkCount {
				break
			}
			end := min(start+stride, c.chunkCount)
			wg.Add(1)
			go func(w, s, e int) {
				defer wg.Done()
				perWorker[w] = scanRange(s, e)
			}(workerIdx, start, end)
		}
		wg.Wait()
		totalHits := 0
		for _, hits := range perWorker {
			totalHits += len(hits)
		}
		candidates = make([]scoredSemanticChunkID, 0, totalHits)
		for _, hits := range perWorker {
			candidates = append(candidates, hits...)
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dotProduct != candidates[j].dotProduct {
			return candidates[i].dotProduct > candidates[j].dotProduct
		}
		if candidates[i].occurredUnix != candidates[j].occurredUnix {
			return candidates[i].occurredUnix > candidates[j].occurredUnix
		}
		return candidates[i].chunkID < candidates[j].chunkID
	})

	// Keep one top chunk per conversation hash with a small safety margin for
	// exact post-hydration conversation deduplication and link resolution.
	targetCount := poolLimit + 16
	deduped := make([]scoredSemanticChunkID, 0, min(len(candidates), targetCount))
	seenHashes := make(map[uint64]struct{}, targetCount)
	for _, cand := range candidates {
		if _, dup := seenHashes[cand.conversationHash]; dup {
			continue
		}
		seenHashes[cand.conversationHash] = struct{}{}
		deduped = append(deduped, cand)
		if len(deduped) >= targetCount {
			break
		}
	}
	return deduped
}

func hashSemanticConversationDedupKey(trawler, conversationKey, representativeResolvedRef string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(trawler))
	_, _ = h.Write([]byte{0})
	if conversationKey != "" {
		_, _ = h.Write([]byte(conversationKey))
	} else {
		_, _ = h.Write([]byte(representativeResolvedRef))
	}
	return h.Sum64()
}

func alignUp8Uint64(value uint64) uint64 {
	return (value + 7) &^ 7
}

func truncateASCIIString(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func removeSemanticVectorCacheFiles(stateRoot string) error {
	vecPath := ResolveSemanticVectorCachePath(stateRoot)
	if err := os.Remove(vecPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", vecPath, err)
	}
	tmpMatches, err := filepath.Glob(vecPath + ".*.tmp")
	if err == nil {
		for _, tmpPath := range tmpMatches {
			_ = os.Remove(tmpPath)
		}
	}
	return nil
}
