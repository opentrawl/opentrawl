package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	query := normalizeWords(personQuery)
	if query == "" {
		return nil, usageErr(errors.New("who takes a name"))
	}
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	candidates, err := st.ResolveWho(ctx, query)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: whoMatchCandidates(candidates)}, nil
}

func whoMatchCandidates(candidates []store.WhoCandidate) []*person.TrawlerPersonMatchCandidate {
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		personMatchCandidate := &person.TrawlerPersonMatchCandidate{
			PersonDisplayName: candidate.Who,
			PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{{
				RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(appID),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: append([]string(nil), candidate.Identifiers...),
				PersonDisplayNamesObservedByTrawlerArchive:           []string{candidate.Who},
			}},
		}
		if !candidate.LastSeen.IsZero() {
			personMatchCandidate.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LastSeen)
		}
		if candidate.Messages > 0 {
			personMatchCandidate.MessageCountInvolvingPerson = uint64(candidate.Messages)
		}
		out = append(out, personMatchCandidate)
	}
	return out
}

func (r *runtime) ambiguousWhoError(who string) error {
	return commandErr(4, "ambiguous_who", fmt.Errorf("ambiguous --who %q", who))
}

func (r *runtime) unknownWhoError(who string) error {
	return commandErr(5, "unknown_who", fmt.Errorf("unknown --who %q", who))
}
