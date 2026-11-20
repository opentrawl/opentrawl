package gmail

import (
	"errors"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/gmail/internal/auth"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type commandError struct {
	name    string
	message string
	err     error
}

func (e commandError) Error() string {
	return e.message
}

func (e commandError) Unwrap() error {
	return e.err
}

func (e commandError) ExitCode() int {
	return 1
}

func (e commandError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    e.name,
		Message: e.message,
	}
}

func commandErr(kind, message string, err error) error {
	if err == nil {
		err = errors.New(message)
	}
	return commandError{name: kind, message: message, err: err}
}

func archiveErr(err error) error {
	return commandErr("archive_missing", "archive database is not ready", err)
}

// gmailConnectionCommandFailure words a connection failure the reader can
// act on. Any other failure is returned unchanged; the CLI reports that the
// command did not complete, and the run log keeps the failed step.
func gmailConnectionCommandFailure(req *trawlkit.TrawlerCommandExecutionRequest, err error) error {
	if errors.Is(err, archive.ErrArchiveBelongsToAnotherGoogleAccount) {
		return render.TrawlerArchiveBelongsToAnotherAccount(displayName, accountProviderDisplayName)
	}
	var failure *auth.GmailConnectionFailure
	if !errors.As(err, &failure) {
		return err
	}
	logGmailConnectionFailure(req, err)
	switch failure.GmailConnectionFailureKind {
	case auth.GmailIsNotConnected:
		return render.TrawlerIsNotConnected(displayName)
	case auth.GmailIsAlreadyConnected:
		return render.TrawlerIsAlreadyConnected(displayName)
	case auth.GoogleSignInWasCancelled:
		return render.AccountProviderSignInWasCancelled(accountProviderDisplayName)
	case auth.GoogleSignInTimedOut:
		return render.AccountProviderSignInTimedOut(accountProviderDisplayName)
	case auth.GoogleSignInDidNotGrantGmailReadAccess:
		return render.AccountProviderDidNotGrantTrawlerAccess(accountProviderDisplayName, displayName)
	case auth.GoogleNoLongerAcceptsGmailGrant:
		return render.AccountProviderNoLongerAcceptsConnection(accountProviderDisplayName, displayName)
	case auth.OpenTrawlBuildHasNoGoogleOAuthClient:
		return render.OpenTrawlBuildCannotConnectToAccountProvider(accountProviderDisplayName)
	case auth.OpenTrawlBuildCannotUseDataProtectionKeychain:
		return render.OpenTrawlBuildCannotUseKeychain()
	default:
		return err
	}
}

func logGmailConnectionFailure(req *trawlkit.TrawlerCommandExecutionRequest, err error) {
	if req != nil && req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Warn("gmail_connection_failed", err.Error()+": "+causeText(err))
	}
}

func causeText(err error) string {
	if cause := errors.Unwrap(err); cause != nil {
		return cause.Error()
	}
	return "no further detail"
}
