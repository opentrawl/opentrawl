package semantic

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

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
	reciprocalRankFusionSmoothingConst = 60.0
	semanticReciprocalRankWeight       = 0.95
)

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
// helper and performs a fast pure-Go int8 dot-product scan over semantic.db.
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
	modelDir, err := ResolveDefaultModelCacheDirectory()
	if err != nil {
		return nil, err
	}
	cached, _, err := CheckDefaultModelCachedAndVerified(modelDir, false)
	if err != nil || !cached {
		return nil, err
	}

	helper := NewSemanticHelperClient(stateRoot)
	vecs, _, err := helper.EmbedTexts(
		ctx,
		modelDir,
		DefaultSemanticEmbeddingModelIdentifier,
		[]string{queryText},
		DefaultSemanticEmbeddingDimensions,
		DefaultSemanticMaximumInputTokenCount,
		true,
	)
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, err
	}
	queryVec := vecs[0]

	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	poolLimit := max(limit*3, 60)

	scored, err := scanSemanticChunks(ctx, semStore.DB(), queryVec, q, poolLimit)
	if err != nil || len(scored) == 0 {
		return nil, err
	}

	aliasesByTrawlerRef, err := resolveSemanticShortLinks(ctx, indexDB, scored)
	if err != nil {
		return nil, err
	}

	queryTokens := unifiedindex.TokenizeSearchWords(queryText)
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

func scanSemanticChunks(
	ctx context.Context,
	semDB *sql.DB,
	queryVec []byte,
	q SemanticSearchQuery,
	topK int,
) ([]scoredSemanticChunk, error) {
	whereParts, args := buildSemanticFilterSQL(q)
	whereSQL := ""
	if len(whereParts) > 0 {
		whereSQL = " WHERE " + strings.Join(whereParts, " AND ")
	}
	rows, err := semDB.QueryContext(ctx, `
		select trawler, trawler_display_name, record_kind, conversation_key, display_context,
		       representative_resolved_ref, occurred_unix, has_time_of_day, utc_offset_seconds,
		       title, people_text, body_preview, message_count, embedding_int8
		from semantic_chunks`+whereSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("scan semantic chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	scaleDenom := 127.0 * 127.0
	var candidates []scoredSemanticChunk
	for rows.Next() {
		var c scoredSemanticChunk
		var blob []byte
		if err := rows.Scan(
			&c.trawler, &c.trawlerDisplay, &c.recordKind, &c.conversationKey, &c.displayContext,
			&c.repResolvedRef, &c.occurredUnix, &c.hasTimeOfDay, &c.utcOffsetSeconds,
			&c.title, &c.peopleText, &c.bodyPreview, &c.messageCount, &blob,
		); err != nil {
			return nil, err
		}
		if len(blob) != len(queryVec) {
			continue
		}
		cos := float64(dotProductInt8Bytes(queryVec, blob)) / scaleDenom
		if cos < semanticMinimumCosineSimilarity {
			continue
		}
		c.cosine = cos
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].cosine != candidates[j].cosine {
			return candidates[i].cosine > candidates[j].cosine
		}
		return candidates[i].occurredUnix > candidates[j].occurredUnix
	})

	// Keep the best-scoring chunk per conversation/thread.
	deduped := make([]scoredSemanticChunk, 0, min(len(candidates), topK))
	seenConv := make(map[string]struct{}, topK)
	for _, c := range candidates {
		convKey := c.trawler + "\x00" + c.conversationKey
		if c.conversationKey == "" {
			convKey = c.trawler + "\x00" + c.repResolvedRef
		}
		if _, exists := seenConv[convKey]; exists {
			continue
		}
		seenConv[convKey] = struct{}{}
		deduped = append(deduped, c)
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
	if q.AfterUnix != nil {
		where = append(where, `occurred_unix >= ?`)
		args = append(args, *q.AfterUnix)
	}
	if q.BeforeUnix != nil {
		where = append(where, `occurred_unix <= ?`)
		args = append(args, *q.BeforeUnix)
	}
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
// byte slices using an unrolled 8-way inner loop.
func dotProductInt8Bytes(a, b []byte) int32 {
	var sum int32
	n := len(a)
	i := 0
	for ; i+7 < n; i += 8 {
		sum += int32(int8(a[i]))*int32(int8(b[i])) +
			int32(int8(a[i+1]))*int32(int8(b[i+1])) +
			int32(int8(a[i+2]))*int32(int8(b[i+2])) +
			int32(int8(a[i+3]))*int32(int8(b[i+3])) +
			int32(int8(a[i+4]))*int32(int8(b[i+4])) +
			int32(int8(a[i+5]))*int32(int8(b[i+5])) +
			int32(int8(a[i+6]))*int32(int8(b[i+6])) +
			int32(int8(a[i+7]))*int32(int8(b[i+7]))
	}
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
			pres.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate{
					CalendarDate: &presentation.CalendarDate{
						CalendarYear:        int32(t.Year()),
						CalendarMonthNumber: int32(t.Month()),
						CalendarDayOfMonth:  int32(t.Day()),
					},
				},
			}
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
		SearchMatchPresentation: pres,
		TrawlLink:               &identity.GloballyRoutableTrawlLink{GloballyRoutableTrawlLink: link},
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
