package trawlkit

import (
	"fmt"
	"os"
	"strings"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

// Manifest returns the protobuf manifest used by API and app surfaces.
func Manifest(trawler Trawler) (*federationv1.RegisteredTrawlerManifest, error) {
	registeredTrawlerDeclaration := trawler.RegisteredTrawlerDeclaration()
	trawlerCommandDeclarationFactsByCommandKey, err := trawlerCommandDeclarationFactsByCommandKey(trawler)
	if err != nil {
		return nil, err
	}
	if err := validateTrawlerCommandNamesShownInBareTrawlOverview(
		registeredTrawlerDeclaration.TrawlerCommandNamesShownInBareTrawlOverview,
		trawlerCommandDeclarationFactsByCommandKey,
	); err != nil {
		return nil, err
	}
	return &federationv1.RegisteredTrawlerManifest{
		RegisteredTrawlerManifestIdentity:           strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerManifestIdentity),
		RegisteredTrawlerCommandName:                strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerCommandName),
		RegisteredTrawlerDisplayName:                strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerDisplayName),
		TrawlerCommandNamesShownInBareTrawlOverview: append([]string(nil), registeredTrawlerDeclaration.TrawlerCommandNamesShownInBareTrawlOverview...),
		TrawlerCapabilities:                         capabilitiesFor(trawler),
		RegisteredTrawlerAliases:                    trimmedAliases(registeredTrawlerDeclaration.RegisteredTrawlerAliases),
		RegisteredTrawlerCommandDeclarations: registeredTrawlerCommandDeclarationsForManifest(
			trawler.TrawlerCommands(),
			trawlerCommandDeclarationFactsByCommandKey,
		),
		RegisteredTrawlerPrivacyBoundary: &federationv1.TrawlerPrivacyBoundary{
			ArchiveContentReadByTrawler:     strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.Reads),
			ArchiveContentThatLeavesMachine: strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.LeavesMachine),
			NetworkRequestsMadeByTrawler:    strings.TrimSpace(registeredTrawlerDeclaration.RegisteredTrawlerPrivacyBoundary.NetworkRequests),
		},
	}, nil
}

func registeredTrawlerCommandDeclarationsForManifest(
	trawlerCommands []TrawlerCommand,
	trawlerCommandDeclarationFactsByCommandKey map[string]trawlerCommandDeclarationFacts,
) []*federationv1.RegisteredTrawlerCommandDeclaration {
	declarations := make([]*federationv1.RegisteredTrawlerCommandDeclaration, 0, len(trawlerCommands))
	for _, trawlerCommand := range trawlerCommands {
		commandFacts := trawlerCommandDeclarationFactsByCommandKey[commandKey(trawlerCommand.TrawlerCommandName)]
		flagFacts := commandFacts.flags
		flagDeclarations := make([]*federationv1.RegisteredTrawlerCommandFlagDeclaration, 0, len(flagFacts))
		for _, flagFact := range flagFacts {
			flagDeclarations = append(flagDeclarations, &federationv1.RegisteredTrawlerCommandFlagDeclaration{
				TrawlerCommandFlagName:            strings.TrimSpace(flagFact.name),
				TrawlerCommandFlagHelpDescription: strings.TrimSpace(flagFact.helpDescription),
				TrawlerCommandFlagDefaultValue:    strings.TrimSpace(flagFact.defaultValue),
			})
		}
		declarations = append(declarations, &federationv1.RegisteredTrawlerCommandDeclaration{
			TrawlerCommandName:                    strings.Join(strings.Fields(trawlerCommand.TrawlerCommandName), " "),
			TrawlerCommandHelpDescription:         strings.TrimSpace(commandFacts.helpDescription),
			TrawlerCommandPositionalArgumentNames: append([]string(nil), commandFacts.positionalArgumentNames...),
			TrawlerCommandFlagDeclarations:        flagDeclarations,
			TrawlerCommandHelpPlacement:           registeredTrawlerCommandHelpPlacementForManifest(trawlerCommand.TrawlerCommandHelpListing),
		})
	}
	return declarations
}

func registeredTrawlerCommandHelpPlacementForManifest(
	helpListing TrawlerCommandHelpListing,
) federationv1.RegisteredTrawlerCommandHelpPlacement {
	switch helpListing {
	case TrawlerCommandListedInNormalTrawlerHelp:
		return federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_LISTED_IN_NORMAL_TRAWLER_HELP
	case TrawlerCommandListedOnlyUnderMoreTrawlerCommands:
		return federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_LISTED_ONLY_UNDER_MORE_TRAWLER_COMMANDS
	case TrawlerCommandHiddenFromHumanHelp:
		return federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_HIDDEN_FROM_HUMAN_HELP
	default:
		return federationv1.RegisteredTrawlerCommandHelpPlacement_REGISTERED_TRAWLER_COMMAND_HELP_PLACEMENT_UNSPECIFIED
	}
}

func capabilitiesFor(trawler Trawler) []string {
	capabilities := []string{"metadata", "status", "short_refs"}
	if _, ok := trawler.(Syncer); ok {
		capabilities = append(capabilities, "sync")
	}
	if _, ok := trawler.(Searcher); ok {
		capabilities = append(capabilities, "search")
	}
	if _, ok := trawler.(RecordOpener); ok {
		capabilities = append(capabilities, "open")
	}
	if _, ok := trawler.(WhoMatcher); ok {
		capabilities = append(capabilities, "who")
	}
	if _, ok := trawler.(ConversationLister); ok {
		capabilities = append(capabilities, "conversations")
	}
	if _, ok := trawler.(TrawlerMessageLister); ok {
		capabilities = append(capabilities, "messages")
	}
	for _, command := range trawler.TrawlerCommands() {
		if command.TrawlerCommandHelpListing == TrawlerCommandHiddenFromHumanHelp {
			continue
		}
		if _, ok := sharedTrawlerCommandName(command.TrawlerCommandName); ok {
			continue
		}
		if name := commandKey(command.TrawlerCommandName); name != "" {
			capabilities = append(capabilities, name)
		}
	}
	return uniqueStrings(capabilities)
}

func validateTrawlerCommandNamesShownInBareTrawlOverview(
	trawlerCommandNames []string,
	trawlerCommandDeclarationFactsByCommandKey map[string]trawlerCommandDeclarationFacts,
) error {
	if len(trawlerCommandNames) > 4 {
		return fmt.Errorf("invalid trawler command names shown in bare trawl overview: at most four entries are allowed")
	}
	seen := make(map[string]struct{}, len(trawlerCommandNames))
	for _, trawlerCommandName := range trawlerCommandNames {
		if trawlerCommandName == "" {
			return fmt.Errorf("invalid trawler command names shown in bare trawl overview: entries must not be empty")
		}
		if strings.TrimSpace(trawlerCommandName) != trawlerCommandName {
			return fmt.Errorf("invalid trawler command names shown in bare trawl overview: entries must already be trimmed")
		}
		if _, ok := seen[trawlerCommandName]; ok {
			return fmt.Errorf("invalid trawler command names shown in bare trawl overview: entries must be distinct")
		}
		commandFacts, commandIsRegistered := trawlerCommandDeclarationFactsByCommandKey[commandKey(trawlerCommandName)]
		if !commandIsRegistered || commandFacts.trawlerCommandHelpListing == TrawlerCommandHiddenFromHumanHelp {
			return fmt.Errorf(
				"invalid trawler command names shown in bare trawl overview: %q is not a public command registered by the trawler",
				trawlerCommandName,
			)
		}
		seen[trawlerCommandName] = struct{}{}
	}
	return nil
}

func commandKey(name string) string {
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), "_")
	return strings.ReplaceAll(name, "-", "_")
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

func filepathBase(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), string(os.PathSeparator))
	if path == "" {
		return "trawl"
	}
	if separatorIndex := strings.LastIndexByte(path, os.PathSeparator); separatorIndex >= 0 {
		return path[separatorIndex+1:]
	}
	return path
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
