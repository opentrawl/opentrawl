package render

// Failures and words for a trawler that connects to an account at an online
// provider, such as Gmail at Google. The trawler names itself and the
// provider; the library words the sentence.

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
