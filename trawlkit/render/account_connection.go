package render

import (
	"fmt"
	"io"
	"strings"

	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
)

// Failures and words for a trawler that connects to an account at an online
// provider, such as Gmail at Google. The trawler names itself and the
// provider; the library words the sentence.

func writeAccountConnectionChangeResponse(writer io.Writer, response *command.AccountConnectionChangeResponse) error {
	if response == nil {
		return fmt.Errorf("account connection change response is missing")
	}
	trawlerDisplayName := strings.TrimSpace(response.GetTrawlerDisplayName())
	accountProviderDisplayName := strings.TrimSpace(response.GetAccountProviderDisplayName())
	connectedAccountIdentifier := strings.TrimSpace(response.GetConnectedAccountIdentifier())
	var title string
	switch response.GetAccountConnectionChangeKind() {
	case command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED:
		title = trawlerDisplayName + " connected"
	case command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED:
		title = trawlerDisplayName + " disconnected"
	default:
		return fmt.Errorf("account connection change kind is missing")
	}
	var fields []cardField
	if connectedAccountIdentifier != "" {
		accountLabel := "Account"
		if accountProviderDisplayName != "" {
			accountLabel = accountProviderDisplayName + " account"
		}
		fields = append(fields, cardField{label: accountLabel, value: connectedAccountIdentifier})
	}
	if response.GetLocalArchiveKeptOnMac() {
		fields = append(fields, cardField{label: "Archive", value: "kept on this Mac"})
	}
	return writeCard(writer, card{
		title:  strings.TrimSpace(title),
		fields: fields,
	})
}

func nextCommandForAccountConnectionChange(response *command.AccountConnectionChangeResponse) NextCommand {
	trawlerCommandName := strings.TrimSpace(response.GetTrawlerCommandName())
	if trawlerCommandName == "" {
		return NextCommand{}
	}
	switch response.GetAccountConnectionChangeKind() {
	case command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_CONNECTED:
		return nextCommandWithLabel(nextCommandLabelUpdateArchive, "update", trawlerCommandName)
	case command.AccountConnectionChangeKind_ACCOUNT_CONNECTION_CHANGE_KIND_DISCONNECTED:
		return nextCommandWithLabel(nextCommandLabelConnectAccount, trawlerCommandName, "connect")
	default:
		return NextCommand{}
	}
}

func TrawlerIsNotConnected(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed(trawlerDisplayName + " is not connected.")
}

func TrawlerIsAlreadyConnected(trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed(trawlerDisplayName + " is already connected.")
}

func AccountProviderSignInWasCancelled(accountProviderDisplayName string) CommandFailure {
	return archiveOrOperationFailed(accountProviderDisplayName + " sign-in was cancelled.")
}

func AccountProviderSignInTimedOut(accountProviderDisplayName string) CommandFailure {
	return archiveOrOperationFailed(accountProviderDisplayName + " sign-in timed out.")
}

func AccountProviderDidNotGrantTrawlerAccess(accountProviderDisplayName string, trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed(accountProviderDisplayName + " sign-in did not give OpenTrawl access to " + trawlerDisplayName + ".")
}

func AccountProviderNoLongerAcceptsConnection(accountProviderDisplayName string, trawlerDisplayName string) CommandFailure {
	return archiveOrOperationFailed(accountProviderDisplayName + " no longer accepts the " + trawlerDisplayName + " connection.")
}

func TrawlerArchiveBelongsToAnotherAccount(trawlerDisplayName string, accountProviderDisplayName string) CommandFailure {
	return archiveOrOperationFailed("This " + trawlerDisplayName + " archive belongs to another " + accountProviderDisplayName + " account.")
}

func OpenTrawlBuildCannotConnectToAccountProvider(accountProviderDisplayName string) CommandFailure {
	return archiveOrOperationFailed("This OpenTrawl build cannot connect to " + accountProviderDisplayName + ".")
}

func OpenTrawlBuildCannotUseKeychain() CommandFailure {
	return archiveOrOperationFailed("This OpenTrawl build cannot use the macOS Keychain.")
}

// AccountProviderSignInReceivedBrowserPageText is the page the browser
// shows when the provider sends the reader back to OpenTrawl.
func AccountProviderSignInReceivedBrowserPageText(trawlerDisplayName string) string {
	return "OpenTrawl received your " + trawlerDisplayName + " sign-in. You can close this window.\n"
}

// AccountProviderSignInStateMismatchBrowserPageText is the page the browser
// shows when an OAuth callback arrives with an unexpected state token.
func AccountProviderSignInStateMismatchBrowserPageText(accountProviderDisplayName string) string {
	return accountProviderDisplayName + " sign-in state did not match."
}
