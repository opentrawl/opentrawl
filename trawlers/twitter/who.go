package twitter

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Who(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	personQuery string,
) (*person.TrawlerPersonMatchResponse, error) {
	query := strings.Join(strings.Fields(personQuery), " ")
	if query == "" {
		return nil, usageErr(render.WhoNeedsName())
	}
	r := c.handler(ctx, req)
	var candidates []store.TwitterWhoCandidate
	err := r.withReadOnlyStore(func(st *store.Store) error {
		var err error
		candidates, err = st.ResolveTwitterWho(r.ctx, query)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		display := twitterProfileDisplayName(candidate.DisplayName, candidate.Handle, candidate.AuthorID)
		match := &person.TrawlerPersonMatchCandidate{
			PersonDisplayName: display,
			PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{{
				RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(appID),
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: trawlkit.NewExactPersonFilterIdentifiers(candidate.Identifiers),
				PersonDisplayNamesObservedByTrawlerArchive:           []string{display},
			}},
		}
		if !candidate.LastSeen.IsZero() {
			match.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LastSeen)
		}
		if candidate.Messages > 0 {
			match.MessageCountInvolvingPerson = uint64(candidate.Messages)
		}
		out = append(out, match)
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func (c *Crawler) CountPersonMessages(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	request *person.ExactPersonMessageCountRequest,
) (*person.ExactPersonMessageCountResponse, error) {
	r := c.handler(ctx, req)
	var count int
	err := r.withReadOnlyStore(func(st *store.Store) error {
		var err error
		count, err = st.CountDirectMessages(r.ctx, store.DirectMessageListFilter{
			PersonFilter: trawlkit.NewResolvedSearchPersonFilter("", request.GetExactPersonFilterIdentifiers()),
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &person.ExactPersonMessageCountResponse{MessageCountInvolvingPerson: uint64(max(count, 0))}, nil
}

func (c *Crawler) PeopleSnapshot(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) (*person.TrawlerPeopleSnapshot, error) {
	r := c.handler(ctx, req)
	var candidates []store.TwitterWhoCandidate
	err := r.withReadOnlyStore(func(st *store.Store) error {
		var err error
		candidates, err = st.ExportTwitterPeopleSnapshot(r.ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonIdentity, 0, len(candidates))
	for _, candidate := range candidates {
		display := twitterProfileDisplayName(candidate.DisplayName, candidate.Handle, "")
		if display == "" || candidate.AuthorID == "" {
			continue
		}
		identity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive:        trawlkit.NewPersonIdentifierWithinTrawlerArchive(candidate.AuthorID),
			PersonDisplayName:                           display,
			MessageCountInvolvingPersonInTrawlerArchive: uint64(max(candidate.Messages, 0)),
			PersonAccountIdentifiersForServices: []*person.TrawlerPersonAccountIdentifiersForService{{
				PersonAccountServiceName:              "twitter",
				PersonAccountIdentifiersWithinService: trawlkit.NewPersonAccountIdentifiersWithinService(candidate.Identifiers),
			}},
		}
		if !candidate.LastSeen.IsZero() {
			identity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive = timestamppb.New(candidate.LastSeen)
		}
		out = append(out, identity)
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: out}, nil
}
