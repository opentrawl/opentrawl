package store

import (
	"context"
	"sort"
	"strings"

	"github.com/openclaw/crawlkit/whomatch"
)

func (r WhoResolution) OnlyCloseSpellingMatch() bool {
	return len(r.Candidates) == 1 && r.matchRank == whomatch.RankCloseSpelling
}

func (s *Store) WhoMatches(ctx context.Context, identity string) ([]string, error) {
	resolution, err := s.ResolveWho(ctx, identity)
	if err != nil {
		return nil, err
	}
	return resolution.DisplayNames, nil
}

func (s *Store) ResolveWho(ctx context.Context, identity string) (WhoResolution, error) {
	query := normalizeWhoIdentity(identity)
	if query == "" {
		return WhoResolution{}, nil
	}
	records, err := s.whoCandidateRecords(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	if strings.EqualFold(query, "me") {
		return ownerWhoResolution(records), nil
	}
	type rankedCandidate struct {
		record whoCandidateRecord
		rank   whomatch.Rank
	}
	var matches []rankedCandidate
	for _, record := range records {
		rank, ok := record.matchCandidate().MatchRank(query)
		if !ok {
			continue
		}
		matches = append(matches, rankedCandidate{record: record, rank: rank})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := matches[i]
		right := matches[j]
		if left.rank != right.rank {
			return left.rank.BetterThan(right.rank)
		}
		if !left.record.LastSeen.Equal(right.record.LastSeen) {
			return left.record.LastSeen.After(right.record.LastSeen)
		}
		if left.record.Messages != right.record.Messages {
			return left.record.Messages > right.record.Messages
		}
		return strings.ToLower(left.record.Who) < strings.ToLower(right.record.Who)
	})
	resolution := WhoResolution{
		ParticipantKeys: make([]string, 0, len(matches)),
		DisplayNames:    make([]string, 0, len(matches)),
		Candidates:      make([]WhoCandidate, 0, len(matches)),
	}
	if len(matches) > 0 {
		resolution.matchRank = matches[0].rank
	}
	for _, match := range matches {
		candidate := match.record.WhoCandidate
		resolution.ParticipantKeys = append(resolution.ParticipantKeys, candidate.ParticipantKeys...)
		resolution.DisplayNames = append(resolution.DisplayNames, candidate.Who)
		resolution.Candidates = append(resolution.Candidates, candidate)
	}
	return resolution, nil
}

func ownerWhoResolution(records []whoCandidateRecord) WhoResolution {
	for _, record := range records {
		if !record.hasParticipantKey(ownerWhoKey) {
			continue
		}
		candidate := record.WhoCandidate
		return WhoResolution{
			ParticipantKeys: append([]string(nil), candidate.ParticipantKeys...),
			DisplayNames:    []string{candidate.Who},
			Candidates:      []WhoCandidate{candidate},
			matchRank:       whomatch.RankExact,
		}
	}
	return WhoResolution{}
}

func (s *Store) ResolveWhoIdentifier(ctx context.Context, identifier string) (WhoResolution, error) {
	identifier = normalizeWhoIdentity(identifier)
	if identifier == "" {
		return WhoResolution{}, nil
	}
	records, err := s.whoCandidateRecordsWithoutNameMerge(ctx)
	if err != nil {
		return WhoResolution{}, err
	}
	var matches []WhoCandidate
	for _, record := range records {
		for _, candidateIdentifier := range record.Identifiers {
			rank, ok := whomatch.MatchRank(identifier, []string{candidateIdentifier})
			if ok && rank == whomatch.RankExact {
				matches = append(matches, record.WhoCandidate)
				break
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if !matches[i].LastSeen.Equal(matches[j].LastSeen) {
			return matches[i].LastSeen.After(matches[j].LastSeen)
		}
		if matches[i].Messages != matches[j].Messages {
			return matches[i].Messages > matches[j].Messages
		}
		return strings.ToLower(matches[i].Who) < strings.ToLower(matches[j].Who)
	})
	resolution := WhoResolution{
		ParticipantKeys: make([]string, 0, len(matches)),
		DisplayNames:    make([]string, 0, len(matches)),
		Candidates:      matches,
	}
	if len(matches) > 0 {
		resolution.matchRank = whomatch.RankExact
	}
	for _, match := range matches {
		resolution.ParticipantKeys = append(resolution.ParticipantKeys, match.ParticipantKeys...)
		resolution.DisplayNames = append(resolution.DisplayNames, match.Who)
	}
	return resolution, nil
}

type whoCandidateRecord struct {
	WhoCandidate
	aliases []string
}

func (r whoCandidateRecord) hasParticipantKey(value string) bool {
	for _, key := range r.ParticipantKeys {
		if key == value {
			return true
		}
	}
	return false
}

func (r whoCandidateRecord) matchCandidate() whomatch.Candidate {
	return whomatch.Candidate{
		Who:         r.Who,
		Identifiers: r.Identifiers,
		Aliases:     r.aliases,
		LastSeen:    r.LastSeen,
		Messages:    int64(r.Messages),
	}
}
