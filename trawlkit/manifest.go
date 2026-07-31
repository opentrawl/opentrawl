package trawlkit

import (
	"fmt"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

// Manifest returns the protobuf manifest used by API and app surfaces.
func Manifest(trawler Trawler) (*federation.RegisteredTrawlerManifest, error) {
	registeredTrawlerDeclaration := trawler.RegisteredTrawlerDeclaration()
	registeredTrawlerCommandName := strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerCommandName)
	if registeredTrawlerCommandName == "" {
		return nil, fmt.Errorf("registered trawler command name is required")
	}
	trawlerCommandDeclarationFactsByCommandKey, err := trawlerCommandDeclarationFactsByCommandKey(trawler)
	if err != nil {
		return nil, err
	}
	if err := validateTrawlerCommandsShownInBareTrawlOverview(trawler.TrawlerCommands()); err != nil {
		return nil, err
	}
	return &federation.RegisteredTrawlerManifest{
		RegisteredTrawler:            registeredTrawlerDeclaration.RegisteredTrawler,
		RegisteredTrawlerCommandName: registeredTrawlerCommandName,
		RegisteredTrawlerDisplayName: strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerDisplayName),
		RegisteredTrawlerAliases:     trimmedAliases(registeredTrawlerDeclaration.RegisteredTrawlerAliases),
		RegisteredTrawlerCommandDeclarations: registeredTrawlerCommandDeclarationsForManifest(
			trawler.TrawlerCommands(),
			trawlerCommandDeclarationFactsByCommandKey,
		),
		RegisteredTrawlerPrivacyBoundary: &federation.TrawlerPrivacyBoundary{
			ArchiveContentReadByTrawler:     strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.Reads),
			ArchiveContentThatLeavesMachine: strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.LeavesMachine),
			NetworkRequestsMadeByTrawler:    strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.NetworkRequests),
		},
	}, nil
}

func registeredTrawlerCommandDeclarationsForManifest(
	trawlerCommands []TrawlerCommand,
	trawlerCommandDeclarationFactsByCommandKey map[string]trawlerCommandDeclarationFacts,
) []*federation.RegisteredTrawlerCommandDeclaration {
	declarations := make([]*federation.RegisteredTrawlerCommandDeclaration, 0, len(trawlerCommands))
	for _, trawlerCommand := range trawlerCommands {
		commandFacts := trawlerCommandDeclarationFactsByCommandKey[trawlerCommandKey(trawlerCommand)]
		flagFacts := commandFacts.flags
		flagDeclarations := make([]*federation.RegisteredTrawlerCommandFlagDeclaration, 0, len(flagFacts))
		for _, flagFact := range flagFacts {
			flagDeclarations = append(flagDeclarations, &federation.RegisteredTrawlerCommandFlagDeclaration{
				TrawlerCommandFlagName:            strings.TrimSpace(flagFact.name),
				TrawlerCommandFlagHelpDescription: strings.TrimSpace(flagFact.helpDescription),
				TrawlerCommandFlagDefaultValue:    strings.TrimSpace(flagFact.defaultValue),
			})
		}
		manifestDeclaration := &federation.RegisteredTrawlerCommandDeclaration{
			TrawlerCommandHelpDescription:            strings.TrimSpace(commandFacts.helpDescription),
			TrawlerCommandPositionalArgumentNames:    append([]string(nil), commandFacts.positionalArgumentNames...),
			TrawlerCommandFlagDeclarations:           flagDeclarations,
			TrawlerCommandHelpPlacement:              registeredTrawlerCommandHelpPlacementForManifest(commandFacts.trawlerCommandHelpListing),
			TrawlerCommandIsShownInBareTrawlOverview: trawlerCommand.TrawlerCommandShownInBareTrawlOverview,
		}
		if trawlerCommand.SharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			manifestDeclaration.RegisteredTrawlerCommand =
				&federation.RegisteredTrawlerCommandDeclaration_SharedTrawlerOperation{
					SharedTrawlerOperation: trawlerCommand.SharedTrawlerOperation,
				}
		} else {
			manifestDeclaration.RegisteredTrawlerCommand =
				&federation.RegisteredTrawlerCommandDeclaration_BespokeTrawlerCommandName{
					BespokeTrawlerCommandName: strings.Join(strings.Fields(trawlerCommand.TrawlerCommandName), " "),
				}
		}
		declarations = append(declarations, manifestDeclaration)
	}
	return declarations
}

func registeredTrawlerCommandHelpPlacementForManifest(
	helpListing TrawlerCommandHelpListing,
) federation.RegisteredTrawlerCommandHelpPlacement {
	switch helpListing {
	case TrawlerCommandListedInNormalTrawlerHelp:
		return federation.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_LISTED_IN_NORMAL_TRAWLER_HELP
	case TrawlerCommandListedOnlyUnderMoreTrawlerCommands:
		return federation.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_LISTED_ONLY_UNDER_MORE_TRAWLER_COMMANDS
	case TrawlerCommandHiddenFromHumanHelp:
		return federation.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_HIDDEN_FROM_HUMAN_HELP
	default:
		return federation.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_UNSPECIFIED
	}
}

func validateTrawlerCommandsShownInBareTrawlOverview(trawlerCommands []TrawlerCommand) error {
	shownCommandCount := 0
	for _, trawlerCommand := range trawlerCommands {
		if !trawlerCommand.TrawlerCommandShownInBareTrawlOverview {
			continue
		}
		shownCommandCount++
		if trawlerCommand.TrawlerCommandHelpListing == TrawlerCommandHiddenFromHumanHelp {
			return fmt.Errorf(
				"invalid trawler command shown in bare trawl overview: %q is hidden from human help",
				trawlerCommandDisplayName(trawlerCommand),
			)
		}
	}
	if shownCommandCount > 4 {
		return fmt.Errorf("invalid trawler command names shown in bare trawl overview: at most four entries are allowed")
	}
	return nil
}

func commandKey(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), "_")
	return strings.ReplaceAll(name, "-", "_")
}

func trawlerCommandKey(command TrawlerCommand) string {
	if sharedCommandName := sharedTrawlerOperationCommandName(command.SharedTrawlerOperation); sharedCommandName != "" {
		return sharedCommandName
	}
	return commandKey(command.TrawlerCommandName)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func trimmedAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if trimmed := strings.TrimSpace(alias); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
