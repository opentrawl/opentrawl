// Package gmail archives one Gmail mailbox through the Gmail REST API with
// read-only access. The Google grant is kept in the macOS Data Protection
// Keychain and bound to this Mac by a Secure Enclave DPoP key.
package gmail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/gmail/internal/auth"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	appID       = "gmail"
	displayName = "Gmail"
	// accountProviderDisplayName is who the Gmail account belongs to, as the
	// reader knows it.
	accountProviderDisplayName = "Google"
)

// DefaultGoogleOAuthClientID is OpenTrawl's Google desktop OAuth client.
var DefaultGoogleOAuthClientID = "686309076817-1f0lkdn2podgmh2goqd0teht9uejqmsc.apps.googleusercontent.com"

// DefaultGoogleOAuthClientSecret is set at release build time with
// -ldflags "-X github.com/opentrawl/opentrawl/gmail.DefaultGoogleOAuthClientSecret=...".
// A build without it cannot connect Gmail.
var DefaultGoogleOAuthClientSecret string

type Crawler struct{}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Updater                = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.RecordOpener           = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "gmail",
		RegisteredTrawlerDisplayName: displayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "The Gmail messages and labels of the one Google account you connect, with read-only access. It skips Spam and Trash.",
			LeavesMachine:   "Nothing. Your archive and your Google credential stay on this Mac.",
			NetworkRequests: "Connect, update and disconnect talk directly to Google. Search and other archive commands are local.",
		},
	}
}

func (*Crawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			TrawlerCommandName:               "connect",
			TrawlerCommandHelpDescription:    "Sign in to Google and give OpenTrawl read-only Gmail access",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand:            c.connectGmail,
		},
		{
			TrawlerCommandName:               "disconnect",
			TrawlerCommandHelpDescription:    "Revoke Google access and remove the credential; keep the archive",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand:            c.disconnectGmail,
		},
	}
}

func googleOAuthClient() auth.GoogleOAuthClient {
	return auth.GoogleOAuthClient{
		GoogleOAuthClientIdentifier: DefaultGoogleOAuthClientID,
		GoogleOAuthClientSecret:     DefaultGoogleOAuthClientSecret,
	}
}

// Status says Gmail works only when the archive can answer and this Mac
// holds the Google grant that updates need.
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
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.Messages)},
	}
	if completedAt, err := time.Parse(time.RFC3339, archiveStatus.LastUpdateAt); err == nil {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedAt)
	}
	if !archiveStatus.NewestMessageAt.IsZero() {
		trawlerArchiveStatus.NewestArchivedRecordTime = timestamppb.New(archiveStatus.NewestMessageAt)
	}
	connected, err := auth.GmailCredentialIsStored()
	if err != nil {
		logGmailConnectionFailure(req, err)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = connected
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
	}
	if resolvedPersonFilter := query.PersonFilter.ResolvedPersonFilter(); resolvedPersonFilter != nil {
		archiveSearchOptions.PersonFilter = trawlkit.NewResolvedSearchPersonFilter(
			resolvedPersonFilter.PersonFilterText,
			resolvedPersonFilter.ExactPersonFilterIdentifiers,
		)
	} else {
		archiveSearchOptions.PersonFilter = trawlkit.NewUnresolvedSearchPersonFilter(
			query.PersonFilter.UnresolvedPersonFilterText(),
		)
	}
	if !query.After.IsZero() {
		archiveSearchOptions.After = &query.After
	}
	if !query.Before.IsZero() {
		archiveSearchOptions.Before = &query.Before
	}
	archiveSearchResponse, err := archiveStore.Search(ctx, archiveSearchOptions)
	if err != nil {
		return nil, archiveErr(err)
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
	logGmailEvent(req, "search_complete", fmt.Sprintf("returned=%d total=%d", len(archiveSearchResponse.Results), archiveSearchResponse.TotalMatches))
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

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(err)
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: contacts}, nil
}

func logGmailEvent(req *trawlkit.TrawlerCommandExecutionRequest, event, message string) {
	if req != nil && req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info(event, message)
	}
}
