package gmail

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/gmail/internal/gog"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	appID         = "gmail"
	displayName   = "Gmail"
	minGogVersion = "0.31.0"
)

type Crawler struct {
	gog            gog.Client
	backupRepoPath string
	syncQuery      string
	syncMax        int
}

var (
	_ trawlkit.Trawler    = (*Crawler)(nil)
	_ trawlkit.Syncer     = (*Crawler)(nil)
	_ trawlkit.Searcher   = (*Crawler)(nil)
	_ trawlkit.WhoMatcher = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{gog: gog.New(gog.DefaultBinary)}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "gmail",
		RegisteredTrawlerDisplayName: displayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Your Gmail messages from Google, the people named in those messages, and the local encrypted backup created for your Google account.",
			LeavesMachine:   "OpenTrawl does not upload its archive. During an update, OpenTrawl gets your Gmail messages through your Google account.",
			NetworkRequests: "Updates get Gmail messages from Google. Search and other archive commands are local.",
		},
	}
}

func (*Crawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			SharedTrawlerOperation:    federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
			TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp,
			RegisterTrawlerCommandFlags: func(fs *flag.FlagSet) {
				fs.StringVar(&c.backupRepoPath, "backup-repo", "", "backup repository")
				fs.StringVar(&c.syncQuery, "query", "", "Gmail search query")
				fs.IntVar(&c.syncMax, "max", 0, "maximum Gmail messages")
			},
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.Messages)},
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, archiveStatus.LastSyncAt); err == nil {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(completedAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	archiveSearchOptions := archive.SearchOptions{
		Query:         strings.TrimSpace(query.Text),
		Limit:         query.Limit,
		BoundedTotals: query.SearchTotalIsLowerBoundWhenResultLimitIsReached,
		Who:           strings.Join(strings.Fields(query.Who), " "),
	}
	if !query.After.IsZero() {
		archiveSearchOptions.After = &query.After
	}
	if !query.Before.IsZero() {
		archiveSearchOptions.Before = &query.Before
	}
	archiveSearchResponse, err := archiveStore.Search(ctx, archiveSearchOptions)
	if err != nil {
		return nil, err
	}
	trawlerSearchMatches := make([]*search.TrawlerSearchMatch, 0, len(archiveSearchResponse.Results))
	for _, archiveSearchHit := range archiveSearchResponse.Results {
		trawlerSearchMatch, err := gmailTrawlerSearchMatch(archiveSearchHit)
		if err != nil {
			return nil, err
		}
		trawlerSearchMatches = append(trawlerSearchMatches, trawlerSearchMatch)
	}
	trawlerSearchResponse := &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
		TotalSearchMatches:                 uint64(archiveSearchResponse.TotalMatches),
		TotalSearchMatchesIsLowerBound:     archiveSearchResponse.TotalIsLowerBound,
		MoreSearchMatchesExist:             archiveSearchResponse.Truncated,
	}
	_ = logInfo(req, "search_complete", fmt.Sprintf("returned=%d total=%d", len(archiveSearchResponse.Results), archiveSearchResponse.TotalMatches))
	return trawlerSearchResponse, nil
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	result, err := st.ResolveWho(ctx, strings.Join(strings.Fields(personQuery), " "))
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		out = append(out, whoCandidate(candidate))
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}
