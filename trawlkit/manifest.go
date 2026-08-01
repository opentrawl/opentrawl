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
	if err := validateTrawlerCommandDiscoveryPlacements(trawler.TrawlerCommands()); err != nil {
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
			TrawlerCommandHelpDescription:         strings.TrimSpace(commandFacts.helpDescription),
			TrawlerCommandPositionalArgumentNames: append([]string(nil), commandFacts.positionalArgumentNames...),
			TrawlerCommandFlagDeclarations:        flagDeclarations,
			TrawlerCommandDiscoveryPlacement:      registeredTrawlerCommandDiscoveryPlacementForManifest(commandFacts.discoveryPlacement),
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

func registeredTrawlerCommandDiscoveryPlacementForManifest(
	discoveryPlacement TrawlerCommandDiscoveryPlacement,
) federation.RegisteredTrawlerCommandDiscoveryPlacement {
	switch discoveryPlacement {
	case TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp:
		return federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_SHOWN_IN_BARE_TRAWL_OVERVIEW_AND_TRAWLER_NAMESPACE_HELP
	case TrawlerCommandShownOnlyInTrawlerNamespaceHelp:
		return federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_SHOWN_ONLY_IN_TRAWLER_NAMESPACE_HELP
	case TrawlerCommandRoutedOnlyByRootSharedCommand:
		return federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_ROUTED_ONLY_BY_ROOT_SHARED_COMMAND
	default:
		return federation.RegisteredTrawlerCommandDiscoveryPlacement_REGISTERED_TRAWLER_COMMAND_DISCOVERY_PLACEMENT_UNSPECIFIED
	}
}

func validateTrawlerCommandDiscoveryPlacements(trawlerCommands []TrawlerCommand) error {
	shownCommandCount := 0
	for _, trawlerCommand := range trawlerCommands {
		switch trawlerCommand.TrawlerCommandDiscoveryPlacement {
		case TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp:
			shownCommandCount++
			if rootSharedTrawlerOperation(trawlerCommand.SharedTrawlerOperation) {
				return fmt.Errorf("invalid trawler command discovery placement: root shared command %q cannot be shown in a trawler namespace", trawlerCommandDisplayName(trawlerCommand))
			}
		case TrawlerCommandShownOnlyInTrawlerNamespaceHelp:
			if rootSharedTrawlerOperation(trawlerCommand.SharedTrawlerOperation) {
				return fmt.Errorf("invalid trawler command discovery placement: root shared command %q cannot be shown in a trawler namespace", trawlerCommandDisplayName(trawlerCommand))
			}
		case TrawlerCommandRoutedOnlyByRootSharedCommand:
			if !rootSharedTrawlerOperation(trawlerCommand.SharedTrawlerOperation) {
				return fmt.Errorf("invalid trawler command discovery placement: %q is not a root shared command", trawlerCommandDisplayName(trawlerCommand))
			}
		default:
			return fmt.Errorf("invalid trawler command discovery placement: %q is unspecified", trawlerCommandDisplayName(trawlerCommand))
		}
	}
	if shownCommandCount > 4 {
		return fmt.Errorf("invalid trawler command names shown in bare trawl overview: at most four entries are allowed")
	}
	return nil
}

func rootSharedTrawlerOperation(sharedOperation federation.SharedTrawlerOperation) bool {
	switch sharedOperation {
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO:
		return true
	default:
		return false
	}
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
