package wacrawl

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	filter := store.MessageFilter{
		Query: strings.TrimSpace(query.Text),
		Limit: query.Limit,
	}
	if !query.After.IsZero() {
		filter.After = &query.After
	}
	if !query.Before.IsZero() {
		filter.Before = &query.Before
	}
	if strings.TrimSpace(query.Who) != "" {
		keys, err := resolveWhoKeys(ctx, st, query.Who)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		filter.Who = query.Who
		filter.WhoKeys = keys
	}
	total, err := st.SearchCount(ctx, filter)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message)
		who := outputField(messageWhoForFormat(message, req.Format))
		where := outputField(messageWhereForFormat(message, req.Format))
		if where == "" {
			where = "WhatsApp conversation"
		}
		if who == "" {
			who = "Unknown sender"
		}
		hits = append(hits, trawlkit.Hit{
			Ref: ref, Time: message.Timestamp, AnchorID: trawlkit.MatchAnchorID,
			Summary:  trawlkit.ResultSummary{Title: where, Subtitle: who},
			Evidence: []trawlkit.EvidenceFragment{trawlkit.TextMatch("Message from "+who, outputField(messageSnippet(message)))},
		})
	}
	return trawlkit.SearchResult{
		WhoResolved:  query.WhoResolved,
		Results:      hits,
		TotalMatches: total,
		Truncated:    query.Limit > 0 && len(messages) < total,
	}, nil
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolution, err := st.ResolveWho(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		out = append(out, whoCandidateForFormat(candidate, req.Format))
	}
	return out, nil
}

func resolveWhoKeys(ctx context.Context, st *store.Store, value string) ([]string, error) {
	resolution, err := st.ResolveWhoIdentifier(ctx, value)
	if err != nil {
		return nil, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = st.ResolveWho(ctx, value)
		if err != nil {
			return nil, err
		}
	}
	if len(resolution.Candidates) != 1 || resolution.OnlyCloseSpellingMatch() {
		return []string{}, nil
	}
	return append([]string(nil), resolution.ParticipantKeys...), nil
}

func whoCandidate(candidate store.WhoCandidate) whomatch.Candidate {
	return whomatch.Candidate{
		Who:         outputField(candidate.Who),
		Identifiers: append([]string(nil), candidate.Identifiers...),
		LastSeen:    candidate.LastSeen,
		Messages:    int64(candidate.Messages),
	}
}

func whoCandidateForFormat(candidate store.WhoCandidate, format output.Format) whomatch.Candidate {
	out := whoCandidate(candidate)
	if format == output.JSON {
		return out
	}
	out.Who = humanParticipantLabel(out.Who)
	out.Identifiers = humanParticipantIdentifiers(out.Identifiers)
	return out
}
