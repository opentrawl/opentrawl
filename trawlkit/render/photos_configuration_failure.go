package render

func PhotosKnownPlacesFileCannotBeRead(knownPlacesFilePath string, readFailure error) CommandFailure {
	return archiveOrOperationFailed("The known-places file " + knownPlacesFilePath + " could not be read: " + readFailure.Error())
}

func PhotosKnownPlacesFileDoesNotMatchContract(knownPlacesFilePath string, parseFailure error) CommandFailure {
	return archiveOrOperationFailed("The known-places file " + knownPlacesFilePath + " must contain a trawl.photos.knownplaces.KnownPlacesFile in protobuf text format: " + parseFailure.Error())
}

func PhotosPlaceLookupServiceDidNotAnswer(photonBaseURL string, requestFailure error) CommandFailure {
	return archiveOrOperationFailed("The place-lookup service at " + photonBaseURL + " did not answer: " + requestFailure.Error() + ". Places found so far are kept; run the step again when it answers.")
}

func PhotosPlaceLookupServiceIsLimitingRequests(photonBaseURL string) CommandFailure {
	return archiveOrOperationFailed("The place-lookup service at " + photonBaseURL + " is limiting requests. Places found so far are kept; run the step again later, or set photon_base_url to a self-hosted Photon.")
}

func PhotosCardModelIsNotConfigured() CommandFailure {
	return archiveOrOperationFailed("Photos has no card model. Set card_model_provider (antigravity_acp, anthropic_api, claude_command_line, ollama_cloud or codex_command_line) and card_model_name in the Photos config.toml.")
}
func PhotosCardModelProviderIsUnknown(provider string) CommandFailure {
	return archiveOrOperationFailed(quotedForSentence(provider) + " is not a card model provider. Use antigravity_acp, anthropic_api, claude_command_line, ollama_cloud or codex_command_line.")
}
func PhotosCardModelConfigurationIsIncomplete(provider string) CommandFailure {
	switch provider {
	case "ollama_cloud":
		return archiveOrOperationFailed("The ollama_cloud card model needs card_model_name, and an API key in card_model_api_key_file or OLLAMA_API_KEY.")
	case "anthropic_api":
		return archiveOrOperationFailed("The anthropic_api card model needs card_model_name, and an API key in card_model_api_key_file or ANTHROPIC_API_KEY.")
	default:
		return archiveOrOperationFailed("The " + provider + " card model needs card_model_name.")
	}
}
func PhotosCardModelAPIKeyFileCannotBeRead(path string, err error) CommandFailure {
	return archiveOrOperationFailed("The card model API key file " + path + " cannot be read: " + err.Error())
}
func PhotosCardModelRefusedAccess(refusalMessage string) CommandFailure {
	return archiveOrOperationFailed("The card model service refused the call, so no more cards were written: " + refusalMessage)
}
