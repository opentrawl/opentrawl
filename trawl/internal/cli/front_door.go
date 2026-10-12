package cli

import (
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

// trawlerCommandNamesShownInBareTrawlOverview are the commands a trawler
// declares for the overview that bare trawl and trawl --help show.
func trawlerCommandNamesShownInBareTrawlOverview(trawler InstalledTrawler) []string {
	var commandNames []string
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command == nil || command.GetTrawlerCommandDiscoveryPlacement() != federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_SHOWN_IN_BARE_TRAWL_OVERVIEW_AND_TRAWLER_NAMESPACE_HELP {
			continue
		}
		if commandName := registeredTrawlerCommandName(command); commandName != "" {
			commandNames = append(commandNames, commandName)
		}
	}
	return commandNames
}
