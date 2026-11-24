package semantic

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/archivetime"
	federationcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	personcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	searchcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	semanticMinimumCosineSimilarity    = 0.42
	semanticInt8DotScaleDenominator    = 127.0 * 127.0
	reciprocalRankFusionSmoothingConst = 60.0
	semanticReciprocalRankWeight       = 0.95
)

var semanticMinimumDotProduct = int32(math.Ceil(semanticMinimumCosineSimilarity * semanticInt8DotScaleDenominator))

// SemanticSearchQuery specifies filters for a pure-Go int8[512] vector scan.
type SemanticSearchQuery struct {
	QueryText         string
	Trawlers          []string
	PersonFilterTerms []string
	AfterUnix         *int64
	BeforeUnix        *int64
	Limit             int
}

// SemanticSearchHit is one semantic match with its cosine similarity and canonical
// FederatedSearchMatch presentation ready for RRF fusion.
type SemanticSearchHit struct {
	Trawler          string
	ConversationKey  string
	ResolvedRef      string
	CosineSimilarity float64
	Match            *federationcontract.FederatedSearchMatch
}

type scoredSemanticChunk struct {
	chunkID          int64
	trawler          string
	trawlerDisplay   string
	recordKind       string
	conversationKey  string
	displayContext   string
	repResolvedRef   string
	occurredUnix     int64
	hasTimeOfDay     int
	utcOffsetSeconds int
	title            string
	peopleText       string
	bodyPreview      string
	messageCount     int
	cosine           float64
}

// SearchSemanticIndex embeds QueryText through the local Apple Silicon MLX
// helper and performs a fast multi-core int8 dot-product scan over the
// memory-mapped disposable semantic.vec cache, hydrating only the top-K
// winning chunks from semantic.db.
func SearchSemanticIndex(
	ctx context.Context,
	stateRoot string,
	indexDB *sql.DB,
	q SemanticSearchQuery,
) ([]SemanticSearchHit, error) {
	queryText := strings.TrimSpace(q.QueryText)
	if queryText == "" {
		return nil, nil
	}
	semStore, err := OpenSemanticIndexReadOnly(ctx, stateRoot)
	if err != nil || semStore == nil {
		return nil, err
	}
	defer func() { _ = semStore.Close() }()

	meta, err := readSemanticMetadataMap(ctx, semStore.DB())
	if err != nil || meta["enabled"] != "1" {
		return nil, err
	}
	dims := DefaultSemanticEmbeddingDimensions
	if rawDims := meta["embedding_dimensions"]; rawDims != "" {
		if parsed, parseErr := strconv.Atoi(rawDims); parseErr == nil && parsed > 0 {
			dims = parsed
		}
	}
	modelDir, err := ResolveDefaultModelCacheDirectory()
	if err != nil {
		return nil, err
	}
	cached, _, err := CheckDefaultModelCachedAndVerified(modelDir, false)
	if err != nil || !cached {
		return nil, err
	}

	// Overlap the local MLX query embedding RPC with opening (or lazily
	// regenerating) the memory-mapped semantic.vec cache.
	var (
		embedWG   sync.WaitGroup
		queryVecs [][]byte
		embedErr  error
	)
	embedWG.Add(1)
	go func() {
		defer embedWG.Done()
		helper := NewSemanticHelperClient(stateRoot)
		queryVecs, _, embedErr = helper.EmbedTexts(
			ctx,
			modelDir,
			DefaultSemanticEmbeddingModelIdentifier,
			[]string{queryText},
			uint32(dims),
			DefaultSemanticMaximumInputTokenCount,
			true,
		)
	}()

	var vecCache *semanticVectorCache
	if len(q.PersonFilterTerms) == 0 {
		if loaded, cacheErr := openOrRebuildSemanticVectorCache(ctx, stateRoot, semStore.DB(), meta["updated_at_utc"], dims); cacheErr == nil {
			vecCache = loaded
			defer func() { _ = vecCache.Close() }()
		}
	}

	embedWG.Wait()
	if embedErr != nil || len(queryVecs) == 0 || len(queryVecs[0]) == 0 {
		return nil, embedErr
	}
	queryVec := queryVecs[0]

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	poolLimit := max(limit*3, 60)

	var scoredIDs []scoredSemanticChunkID
	if vecCache != nil {
		scoredIDs = vecCache.scanTopChunks(queryVec, q, semanticMinimumDotProduct, poolLimit)
	} else {
		scoredIDs, err = scanSemanticChunkIDsFromSQLite(ctx, semStore.DB(), queryVec, q, semanticMinimumDotProduct, poolLimit)
		if err != nil {
			return nil, err
		}
	}
	if len(scoredIDs) == 0 {
		return nil, nil
	}

	scored, err := hydrateSemanticChunksByID(ctx, semStore.DB(), scoredIDs, poolLimit)
	if err != nil || len(scored) == 0 {
		return nil, err
	}

	queryTokens := unifiedindex.TokenizeSearchWords(queryText)
	if indexDB != nil {
		resolveBestSessionMemberRecords(
			ctx,
			semStore.DB(),
			indexDB,
			scored,
			queryTokens,
			archivetime.InclusiveTimeRangeFromUnixBounds(q.AfterUnix, q.BeforeUnix),
		)
	}

	aliasesByTrawlerRef, err := resolveSemanticShortLinks(ctx, indexDB, scored)
	if err != nil {
		return nil, err
	}

	hits := make([]SemanticSearchHit, 0, len(scored))
	for _, s := range scored {
		link := aliasesByTrawlerRef[s.trawler+"\x00"+s.repResolvedRef]
		if link == "" {
			continue
		}
		hits = append(hits, SemanticSearchHit{
			Trawler:          s.trawler,
			ConversationKey:  s.conversationKey,
			ResolvedRef:      s.repResolvedRef,
			CosineSimilarity: s.cosine,
			Match:            buildSemanticFederatedMatch(s, link, queryTokens),
		})
	}
	return hits, nil
}

// scanSemanticChunkIDsFromSQLite is the lean SQLite fallback used when a person
// filter requires SQL text matching or when semantic.vec cannot be created on
// a read-only filesystem. It scans only vector and filter key columns and leaves
// preview/title text hydration to hydrateSemanticChunksByID.
func scanSemanticChunkIDsFromSQLite(
	ctx context.Context,
	semDB *sql.DB,
	queryVec []byte,
	q SemanticSearchQuery,
	minDotProduct int32,
	topK int,
) ([]scoredSemanticChunkID, error) {
	whereParts, args := buildSemanticFilterSQL(q)
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE " + strings.Join(whereParts, " AND ")
	}
	rows, err := semDB.QueryContext(ctx, `
		select chunk_id, trawler, conversation_key, representative_resolved_ref, occurred_unix, embedding_int8
		from semantic_chunks`+whereSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("scan semantic chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []scoredSemanticChunkID
	for rows.Next() {
		var (
			chunkID         int64
			trawler         string
			conversationKey string
			repResolvedRef  string
			occurredUnix    int64
			blob            []byte
		)
		if err := rows.Scan(&chunkID, &trawler, &conversationKey, &repResolvedRef, &occurredUnix, &blob); err != nil {
			return nil, err
		}
		if len(blob) != len(queryVec) {
			continue
		}
		dot := dotProductInt8Bytes(queryVec, blob)
		if dot < minDotProduct {
			continue
		}
		candidates = append(candidates, scoredSemanticChunkID{
			chunkID:          chunkID,
			occurredUnix:     occurredUnix,
			conversationHash: hashSemanticConversationDedupKey(trawler, conversationKey, repResolvedRef),
			dotProduct:       dot,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

	targetCount := topK + 16
	deduped := make([]scoredSemanticChunkID, 0, min(len(candidates), targetCount))
	seenHashes := make(map[uint64]struct{}, targetCount)
	for _, c := range candidates {
		if _, exists := seenHashes[c.conversationHash]; exists {
			continue
		}
		seenHashes[c.conversationHash] = struct{}{}
		deduped = append(deduped, c)
		if len(deduped) >= targetCount {
			break
		}
	}
	return deduped, nil
}

func hydrateSemanticChunksByID(
	ctx context.Context,
	semDB *sql.DB,
	scoredIDs []scoredSemanticChunkID,
	topK int,
) ([]scoredSemanticChunk, error) {
	if len(scoredIDs) == 0 {
		return nil, nil
	}
	byID := make(map[int64]scoredSemanticChunk, len(scoredIDs))
	const batchSize = 450
	for start := 0; start < len(scoredIDs); start += batchSize {
		end := min(start+batchSize, len(scoredIDs))
		batch := scoredIDs[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, item := range batch {
			args = append(args, item.chunkID)
		}
		rows, err := semDB.QueryContext(ctx, `
			select chunk_id, trawler, trawler_display_name, record_kind, conversation_key, display_context,
			       representative_resolved_ref, occurred_unix, has_time_of_day, utc_offset_seconds,
			       title, people_text, body_preview, message_count
			from semantic_chunks
			where chunk_id in (`+ph+`)
		`, args...)
		if err != nil {
			return nil, fmt.Errorf("hydrate semantic chunks: %w", err)
		}
		for rows.Next() {
			var (
				chunkID int64
				c       scoredSemanticChunk
			)
			if err := rows.Scan(
				&chunkID, &c.trawler, &c.trawlerDisplay, &c.recordKind, &c.conversationKey, &c.displayContext,
				&c.repResolvedRef, &c.occurredUnix, &c.hasTimeOfDay, &c.utcOffsetSeconds,
				&c.title, &c.peopleText, &c.bodyPreview, &c.messageCount,
			); err != nil {
				_ = rows.Close()
				return nil, err
			}
			c.chunkID = chunkID
			byID[chunkID] = c
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
	}

	deduped := make([]scoredSemanticChunk, 0, min(len(scoredIDs), topK))
	seenConv := make(map[string]struct{}, topK)
	for _, item := range scoredIDs {
		chunk, ok := byID[item.chunkID]
		if !ok {
			continue
		}
		convKey := chunk.trawler + "\x00" + chunk.conversationKey
		if chunk.conversationKey == "" {
			convKey = chunk.trawler + "\x00" + chunk.repResolvedRef
		}
		if _, exists := seenConv[convKey]; exists {
			continue
		}
		seenConv[convKey] = struct{}{}
		chunk.cosine = float64(item.dotProduct) / semanticInt8DotScaleDenominator
		deduped = append(deduped, chunk)
		if len(deduped) >= topK {
			break
		}
	}
	return deduped, nil
}

func buildSemanticFilterSQL(q SemanticSearchQuery) ([]string, []any) {
	var where []string
	var args []any
	if len(q.Trawlers) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(q.Trawlers)), ",")
		where = append(where, `trawler in (`+ph+`)`)
		for _, t := range q.Trawlers {
			args = append(args, strings.ToLower(strings.TrimSpace(t)))
		}
	}
	archivetime.InclusiveTimeRangeFromUnixBounds(q.AfterUnix, q.BeforeUnix).
		AppendUnixSQLClauses("occurred_unix", &where, &args)
	if len(q.PersonFilterTerms) > 0 {
		var parts []string
		for _, term := range q.PersonFilterTerms {
			trimmed := strings.ToLower(strings.TrimSpace(term))
			if trimmed == "" {
				continue
			}
			like := "%" + trimmed + "%"
			parts = append(parts, `(lower(people_text) LIKE ? OR lower(display_context) LIKE ?)`)
			args = append(args, like, like)
		}
		if len(parts) > 0 {
			where = append(where, "("+strings.Join(parts, " OR ")+")")
		}
	}
	return where, args
}

// dotProductInt8Bytes computes the exact signed int8 dot product over two equal-length
// byte slices using an unrolled 8-way inner loop with four independent accumulators
// and bounds-check elimination.
func dotProductInt8Bytes(a, b []byte) int32 {
	n := len(a)
	if n == 0 || len(b) < n {
		return 0
	}
	_ = a[n-1]
	_ = b[n-1]
	var s0, s1, s2, s3 int32
	i := 0
	for ; i+7 < n; i += 8 {
		s0 += int32(int8(a[i]))*int32(int8(b[i])) + int32(int8(a[i+4]))*int32(int8(b[i+4]))
		s1 += int32(int8(a[i+1]))*int32(int8(b[i+1])) + int32(int8(a[i+5]))*int32(int8(b[i+5]))
		s2 += int32(int8(a[i+2]))*int32(int8(b[i+2])) + int32(int8(a[i+6]))*int32(int8(b[i+6]))
		s3 += int32(int8(a[i+3]))*int32(int8(b[i+3])) + int32(int8(a[i+7]))*int32(int8(b[i+7]))
	}
	sum := s0 + s1 + s2 + s3
	for ; i < n; i++ {
		sum += int32(int8(a[i])) * int32(int8(b[i]))
	}
	return sum
}

func resolveSemanticShortLinks(
	ctx context.Context,
	indexDB *sql.DB,
	chunks []scoredSemanticChunk,
) (map[string]string, error) {
	byTrawler := make(map[string][]string, 8)
	for _, c := range chunks {
		byTrawler[c.trawler] = append(byTrawler[c.trawler], c.repResolvedRef)
	}
	out := make(map[string]string, len(chunks))
	for trawler, refs := range byTrawler {
		aliases, err := shortref.NewScopedSQLiteIndex(indexDB, trawler).Aliases(ctx, refs)
		if err != nil {
			return nil, err
		}
		for ref, alias := range aliases {
			out[trawler+"\x00"+ref] = trawler + ":" + alias
		}
	}
	return out, nil
}

func buildSemanticFederatedMatch(
	s scoredSemanticChunk,
	link string,
	queryTokens []string,
) *federationcontract.FederatedSearchMatch {
	displayName := firstNonEmptySemantic(s.title, s.displayContext, s.peopleText)
	pres := &searchcontract.SearchMatchPresentation{
		RegisteredTrawlerDisplayName:  s.trawlerDisplay,
		MatchingRecordDisplayName:     displayName,
		MatchingRecordKindDisplayName: displayKindForSemanticRecord(s.recordKind),
	}
	if ctxTitle := strings.TrimSpace(s.displayContext); ctxTitle != "" && ctxTitle != displayName {
		pres.DigitalContainerNamesNearestToBroadest = []string{ctxTitle}
	}
	if ppl := strings.TrimSpace(s.peopleText); ppl != "" {
		pres.PeopleRelatedToMatchingRecord = []*personcontract.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         unifiedindex.TruncateRunes(ppl, 120),
			PersonRoleInArchiveRecord: personcontract.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_PARTICIPANT,
		}}
	}
	if s.occurredUnix > 0 {
		t := time.Unix(s.occurredUnix, 0).UTC()
		if s.hasTimeOfDay == 0 {
			pres.MatchingRecordAssociatedTime = unifiedindex.CalendarDateOfRecordWithoutTimeOfDay(s.occurredUnix, s.utcOffsetSeconds)
		} else {
			assoc := &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
					ExactTime: timestamppb.New(t),
				},
			}
			if s.utcOffsetSeconds != 0 {
				assoc.EventUtcOffsetSecondsRecordedBySourceArchive = proto.Int32(int32(s.utcOffsetSeconds))
			}
			pres.MatchingRecordAssociatedTime = assoc
		}
	}
	if s.messageCount > 1 {
		pres.MatchingRecordStatusLabels = append(
			pres.MatchingRecordStatusLabels,
			fmt.Sprintf("%d msgs in session", s.messageCount),
		)
	}
	if preview := strings.TrimSpace(s.bodyPreview); preview != "" {
		fragments := unifiedindex.BuildHighlightedMatchFragments(preview, queryTokens, 220)
		if len(fragments) > 0 {
			pres.SearchMatchTextFieldsInDisplayOrder = []*searchcontract.SearchMatchTextField{{
				SearchMatchTextFieldName:               "",
				SearchMatchTextFragmentsInDisplayOrder: fragments,
			}}
		}
	}
	return &federationcontract.FederatedSearchMatch{
		RecordAnchor: &identity.RecordAnchorIdentifier{
			RecordAnchorIdentifier: unifiedindex.DefaultRecordAnchorForIndexedRecord(
				s.trawler,
				s.recordKind,
				strings.TrimSpace(s.bodyPreview) != "",
			),
		},
		SearchMatchPresentation: pres,
		TrawlLink:               &identity.GloballyRoutableTrawlLink{GloballyRoutableTrawlLink: link},
	}
}

// resolveBestSessionMemberRecords aligns multi-message semantic session hits to
// the specific member message that best matches the query tokens so the row
// snippet, timestamp, and opened record anchor all refer to the same message.
// It also reads every hit's people and conversation names from index.db, so a
// name fixed in index.db shows without re-embedding the chunk.
func resolveBestSessionMemberRecords(
	ctx context.Context,
	semDB *sql.DB,
	indexDB *sql.DB,
	chunks []scoredSemanticChunk,
	queryTokens []string,
	timeRange archivetime.InclusiveTimeRange,
) {
	if semDB == nil || indexDB == nil || len(chunks) == 0 {
		return
	}
	chunkIndexByChunkID := make(map[int64]int, len(chunks))
	chunkIDArgs := make([]any, 0, len(chunks))
	for i := range chunks {
		if chunks[i].chunkID > 0 {
			chunkIndexByChunkID[chunks[i].chunkID] = i
			chunkIDArgs = append(chunkIDArgs, chunks[i].chunkID)
		}
	}
	if len(chunkIDArgs) == 0 {
		return
	}

	ph := strings.TrimSuffix(strings.Repeat("?,", len(chunkIDArgs)), ",")
	memberRows, err := semDB.QueryContext(ctx, `
		select chunk_id, trawler, canonical_ref
		from semantic_chunk_members
		where chunk_id in (`+ph+`)
	`, chunkIDArgs...)
	if err != nil {
		return
	}
	type memberRef struct {
		trawler      string
		canonicalRef string
	}
	membersByChunk := make(map[int64][]memberRef, len(chunkIDArgs))
	refsByTrawler := make(map[string]map[string]struct{}, 4)
	for memberRows.Next() {
		var cid int64
		var tr, cref string
		if err := memberRows.Scan(&cid, &tr, &cref); err != nil {
			continue
		}
		membersByChunk[cid] = append(membersByChunk[cid], memberRef{trawler: tr, canonicalRef: cref})
		if refsByTrawler[tr] == nil {
			refsByTrawler[tr] = make(map[string]struct{})
		}
		refsByTrawler[tr][cref] = struct{}{}
	}
	_ = memberRows.Close()
	if len(membersByChunk) == 0 {
		return
	}

	type indexedMember struct {
		resolvedRef      string
		occurredUnix     int64
		hasTimeOfDay     int
		utcOffsetSeconds int
		displayContext   string
		peopleText       string
		bodyText         string
	}
	memberData := make(map[string]indexedMember, 256)
	for tr, refSet := range refsByTrawler {
		refs := make([]string, 0, len(refSet))
		for r := range refSet {
			refs = append(refs, r)
		}
		const batchSize = 400
		for start := 0; start < len(refs); start += batchSize {
			end := min(start+batchSize, len(refs))
			batch := refs[start:end]
			rph := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
			args := make([]any, 0, len(batch)+1)
			args = append(args, tr)
			for _, r := range batch {
				args = append(args, r)
			}
			rows, qErr := indexDB.QueryContext(ctx, `
				select canonical_ref, resolved_ref, coalesce(occurred_unix, 0), has_time_of_day,
				       utc_offset_seconds, display_context, people_text, body_text
				from indexed_archive_records
				where trawler = ? and canonical_ref in (`+rph+`)
			`, args...)
			if qErr != nil {
				continue
			}
			for rows.Next() {
				var cref string
				var m indexedMember
				if err := rows.Scan(&cref, &m.resolvedRef, &m.occurredUnix, &m.hasTimeOfDay, &m.utcOffsetSeconds, &m.displayContext, &m.peopleText, &m.bodyText); err != nil {
					continue
				}
				memberData[tr+"\x00"+cref] = m
			}
			_ = rows.Close()
		}
	}

	lowerTokens := make([]string, 0, len(queryTokens))
	for _, tok := range queryTokens {
		if t := strings.ToLower(strings.TrimSpace(tok)); t != "" {
			lowerTokens = append(lowerTokens, t)
		}
	}

	for cid, idx := range chunkIndexByChunkID {
		members := membersByChunk[cid]
		if len(members) == 0 {
			continue
		}
		namedMembers := make([]indexedMember, 0, len(members))
		for _, mr := range members {
			if m, ok := memberData[mr.trawler+"\x00"+mr.canonicalRef]; ok {
				namedMembers = append(namedMembers, m)
			}
		}
		if len(namedMembers) == 0 {
			// index.db no longer holds the record, so the hit has nothing to open.
			chunks[idx].repResolvedRef = ""
			continue
		}
		sort.SliceStable(namedMembers, func(i, j int) bool { return namedMembers[i].occurredUnix < namedMembers[j].occurredUnix })
		peopleSeen := make(map[string]struct{}, 4)
		var peopleList []string
		for _, m := range namedMembers {
			for _, name := range strings.Split(m.peopleText, ", ") {
				if name = strings.TrimSpace(name); name == "" {
					continue
				}
				if _, seen := peopleSeen[name]; !seen {
					peopleSeen[name] = struct{}{}
					peopleList = append(peopleList, name)
				}
			}
		}
		chunks[idx].peopleText = strings.Join(peopleList, ", ")
		chunks[idx].displayContext = strings.TrimSpace(namedMembers[len(namedMembers)-1].displayContext)
		if !isSessionChunkedTrawlerRecord(chunks[idx].trawler, chunks[idx].recordKind) {
			continue
		}
		chunks[idx].title = firstNonEmptySemantic(chunks[idx].displayContext, chunks[idx].trawlerDisplay)
		bestScore := 0
		var best indexedMember
		hasBest := false
		for _, mr := range members {
			m, ok := memberData[mr.trawler+"\x00"+mr.canonicalRef]
			if !ok || strings.TrimSpace(m.resolvedRef) == "" {
				continue
			}
			if !timeRange.ContainsUnixSeconds(m.occurredUnix) {
				continue
			}
			score := 0
			lowerBody := strings.ToLower(m.bodyText)
			for _, tok := range lowerTokens {
				if strings.Contains(lowerBody, tok) {
					score++
				}
			}
			if score > bestScore || (score == bestScore && score > 0 && hasBest && m.occurredUnix < best.occurredUnix) {
				bestScore = score
				best = m
				hasBest = true
			}
		}
		if !hasBest || bestScore == 0 {
			previewLines := make([]string, 0, len(namedMembers))
			for _, m := range namedMembers {
				body := strings.TrimSpace(m.bodyText)
				if body == "" {
					continue
				}
				if speaker := strings.TrimSpace(m.peopleText); speaker != "" {
					body = speaker + ": " + body
				}
				previewLines = append(previewLines, body)
			}
			if len(previewLines) > 0 {
				chunks[idx].bodyPreview = unifiedindex.TruncateRunes(strings.Join(previewLines, " · "), 240)
			}
		}
		if hasBest && bestScore > 0 {
			chunks[idx].repResolvedRef = best.resolvedRef
			chunks[idx].occurredUnix = best.occurredUnix
			chunks[idx].hasTimeOfDay = best.hasTimeOfDay
			chunks[idx].utcOffsetSeconds = best.utcOffsetSeconds
			speaker := strings.TrimSpace(best.peopleText)
			body := strings.TrimSpace(best.bodyText)
			if speaker != "" && body != "" {
				chunks[idx].bodyPreview = unifiedindex.TruncateRunes(speaker+": "+body, 240)
			} else if body != "" {
				chunks[idx].bodyPreview = unifiedindex.TruncateRunes(body, 240)
			}
		}
	}
}

func displayKindForSemanticRecord(kind string) string {
	switch kind {
	case "email":
		return "Email"
	case "message":
		return "Message"
	case "post":
		return "Post"
	case "note":
		return "Note"
	case "event":
		return "Event"
	default:
		return ""
	}
}

// FuseLexicalAndSemanticSearchMatches combines lexical BM25 hits and local
// semantic vector hits using Reciprocal Rank Fusion (RRF), balancing lexical
// precision with high-confidence cross-lingual / conceptual recall.
func FuseLexicalAndSemanticSearchMatches(
	lexicalMatches []*federationcontract.FederatedSearchMatch,
	semanticHits []SemanticSearchHit,
	limit int,
) []*federationcontract.FederatedSearchMatch {
	if len(semanticHits) == 0 {
		if len(lexicalMatches) > limit {
			return lexicalMatches[:limit]
		}
		return lexicalMatches
	}
	if len(lexicalMatches) == 0 {
		out := make([]*federationcontract.FederatedSearchMatch, 0, min(len(semanticHits), limit))
		for i := 0; i < len(semanticHits) && i < limit; i++ {
			out = append(out, semanticHits[i].Match)
		}
		return out
	}

	type fusedEntry struct {
		link      string
		match     *federationcontract.FederatedSearchMatch
		score     float64
		semCosine float64
		inLexical bool
	}

	byLink := make(map[string]*fusedEntry, len(lexicalMatches)+len(semanticHits))
	order := make([]*fusedEntry, 0, len(lexicalMatches)+len(semanticHits))

	for rank, m := range lexicalMatches {
		link := m.GetTrawlLink().GetGloballyRoutableTrawlLink()
		if link == "" {
			continue
		}
		entry := &fusedEntry{
			link:      link,
			match:     m,
			score:     1.0 / (reciprocalRankFusionSmoothingConst + float64(rank+1)),
			inLexical: true,
		}
		byLink[link] = entry
		order = append(order, entry)
	}

	for rank, hit := range semanticHits {
		link := hit.Match.GetTrawlLink().GetGloballyRoutableTrawlLink()
		if link == "" {
			continue
		}
		rrfTerm := semanticReciprocalRankWeight / (reciprocalRankFusionSmoothingConst + float64(rank+1))
		if existing, ok := byLink[link]; ok {
			existing.score += rrfTerm
			existing.semCosine = hit.CosineSimilarity
			continue
		}
		entry := &fusedEntry{
			link:      link,
			match:     hit.Match,
			score:     rrfTerm,
			semCosine: hit.CosineSimilarity,
		}
		byLink[link] = entry
		order = append(order, entry)
	}

	sort.SliceStable(order, func(i, j int) bool {
		if order[i].score != order[j].score {
			return order[i].score > order[j].score
		}
		return order[i].semCosine > order[j].semCosine
	})

	out := make([]*federationcontract.FederatedSearchMatch, 0, min(len(order), limit))
	for i := 0; i < len(order) && i < limit; i++ {
		out = append(out, order[i].match)
	}
	return out
}
