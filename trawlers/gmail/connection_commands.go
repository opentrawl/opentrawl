package gmail

import (
	"context"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/gmail/internal/auth"
	"github.com/opentrawl/opentrawl/gmail/internal/gmailapi"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/mailparse"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// connectGmail signs in to Google, then checks which account signed in
// before it keeps anything. An archive is bound to one Google account for
// its lifetime; a different account is refused and its grant revoked.
func (c *Crawler) connectGmail(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	archiveStore, err := archive.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, commandErr("archive_open_failed", "cannot open the archive database", err)
	}
	grant, err := auth.SignInToGoogleForGmail(
		ctx,
		googleOAuthClient(),
		auth.GoogleSignInBrowserPages{
			SignInReceivedPageText: render.AccountProviderSignInReceivedBrowserPageText(displayName),
			StateMismatchPageText:  render.AccountProviderSignInStateMismatchBrowserPageText(accountProviderDisplayName),
		},
	)
	if err != nil {
		return nil, gmailConnectionCommandFailure(req, err)
	}
	profile, err := gmailapi.NewGmailAPIClient(func(context.Context) (string, error) {
		return grant.GmailAPIAccessToken, nil
	}).Profile(ctx)
	if err != nil {
		grant.Discard(ctx)
		return nil, err
	}
	bound, err := archiveStore.BoundGoogleAccountEmailAddress(ctx)
	if err != nil {
		grant.Discard(ctx)
		return nil, err
	}
	if bound != "" && bound != mailparse.NormalizeEmailAddress(profile.GoogleAccountEmailAddress) {
		grant.Discard(ctx)
		return nil, gmailConnectionCommandFailure(req, archive.ErrArchiveBelongsToAnotherGoogleAccount)
	}
	if err := grant.StoreInKeychain(ctx); err != nil {
		return nil, gmailConnectionCommandFailure(req, err)
	}
	if err := archiveStore.BindGoogleAccountEmailAddress(ctx, profile.GoogleAccountEmailAddress); err != nil {
		return nil, err
	}
	logGmailEvent(req, "gmail_connected", "account_bound=true")
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED,
				TrawlerCommandName:          appID,
				TrawlerDisplayName:          displayName,
				AccountProviderDisplayName:  accountProviderDisplayName,
				ConnectedAccountIdentifier:  string(mailparse.NormalizeEmailAddress(profile.GoogleAccountEmailAddress)),
			},
		},
	}, nil
}

// disconnectGmail removes the grant at Google and on this Mac. The archive
// and its account binding stay.
func (c *Crawler) disconnectGmail(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if err := auth.DisconnectGmail(ctx); err != nil {
		return nil, gmailConnectionCommandFailure(req, err)
	}
	var connectedAccountIdentifier string
	if req.OpenedTrawlerArchiveStore != nil {
		archiveStore, err := archive.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
		if err != nil {
			return nil, err
		}
		bound, err := archiveStore.BoundGoogleAccountEmailAddress(ctx)
		if err != nil {
			return nil, err
		}
		connectedAccountIdentifier = string(bound)
	}
	logGmailEvent(req, "gmail_disconnected", "archive_kept=true")
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_AccountConnectionChangeResponse{
			AccountConnectionChangeResponse: &command.AccountConnectionChangeResponse{
				AccountConnectionChangeKind: command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED,
				TrawlerCommandName:          appID,
				TrawlerDisplayName:          displayName,
				AccountProviderDisplayName:  accountProviderDisplayName,
				ConnectedAccountIdentifier:  connectedAccountIdentifier,
				LocalArchiveKeptOnMac:       true,
			},
		},
	}, nil
}
