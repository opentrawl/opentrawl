package trawlkit

import (
	"flag"
	"io"
	"sort"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

type trawlerCommandFlagDeclarationFacts struct {
	name            string
	helpDescription string
	defaultValue    string
}

type trawlerCommandDeclarationFacts struct {
	name                      string
	helpDescription           string
	positionalArgumentNames   []string
	flags                     []trawlerCommandFlagDeclarationFacts
	trawlerCommandHelpListing TrawlerCommandHelpListing
}

func trawlerCommandDeclarationFactsByCommandKey(
	trawler Trawler,
) (map[string]trawlerCommandDeclarationFacts, error) {
	sharedCommandDeclarations, err := sharedTrawlerCommandDeclarations(trawler)
	if err != nil {
		return nil, err
	}
	if err := validateBespokeTrawlerCommands(trawler); err != nil {
		return nil, err
	}
	commandFactsByCommandKey := make(map[string]trawlerCommandDeclarationFacts)
	_, trawlerDeclaresWho := sharedCommandDeclarations[federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO]
	for _, declaration := range trawler.TrawlerCommands() {
		sharedOperation := declaration.SharedTrawlerOperation
		if sharedOperation == federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		commandFacts, found := sharedTrawlerCommandDeclarationFacts(sharedOperation, trawlerDeclaresWho)
		if !found {
			continue
		}
		commandFacts.trawlerCommandHelpListing = declaration.TrawlerCommandHelpListing
		commandFacts.flags = append(
			commandFacts.flags,
			extractTrawlerCommandFlagDeclarationFacts(declaration.RegisterTrawlerCommandFlags)...,
		)
		sort.Slice(commandFacts.flags, func(left, right int) bool {
			return commandFacts.flags[left].name < commandFacts.flags[right].name
		})
		commandFactsByCommandKey[commandKey(commandFacts.name)] = commandFacts
	}
	for _, declaration := range trawler.TrawlerCommands() {
		if declaration.SharedTrawlerOperation != federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UNSPECIFIED {
			continue
		}
		normalizedCommandKey := commandKey(declaration.TrawlerCommandName)
		if normalizedCommandKey == "" {
			continue
		}
		commandFactsByCommandKey[normalizedCommandKey] = trawlerCommandDeclarationFacts{
			name:                      normalizedCommandKey,
			helpDescription:           strings.TrimSpace(declaration.TrawlerCommandHelpDescription),
			positionalArgumentNames:   append([]string(nil), declaration.TrawlerCommandPositionalArgumentNames...),
			flags:                     extractTrawlerCommandFlagDeclarationFacts(declaration.RegisterTrawlerCommandFlags),
			trawlerCommandHelpListing: declaration.TrawlerCommandHelpListing,
		}
	}
	return commandFactsByCommandKey, nil
}

func sharedTrawlerCommandDeclarationFacts(
	sharedOperation federation.SharedTrawlerOperation,
	trawlerDeclaresWho bool,
) (trawlerCommandDeclarationFacts, bool) {
	switch sharedOperation {
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS:
		return trawlerCommandDeclarationFacts{
			name:            sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription: "Show archive status",
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC:
		return trawlerCommandDeclarationFacts{
			name:            sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription: "Get new items from the app",
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH:
		return trawlerCommandDeclarationFacts{
			name:                    sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription:         "Search archive items",
			positionalArgumentNames: []string{"QUERY"},
			flags:                   builtinSearchCommandFlagDeclarationFacts(trawlerDeclaresWho),
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN:
		return trawlerCommandDeclarationFacts{
			name:                    sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription:         "Open an item",
			positionalArgumentNames: []string{"LINK"},
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO:
		return trawlerCommandDeclarationFacts{
			name:                    sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription:         "Resolve person",
			positionalArgumentNames: []string{"NAME"},
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS:
		return trawlerCommandDeclarationFacts{
			name:            sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription: "List conversations",
			flags:           builtinConversationCommandFlagDeclarationFacts(),
		}, true
	case federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES:
		return trawlerCommandDeclarationFacts{
			name:            sharedTrawlerOperationCommandName(sharedOperation),
			helpDescription: "List messages",
			flags:           builtinTrawlerMessageListCommandFlagDeclarationFacts(),
		}, true
	default:
		return trawlerCommandDeclarationFacts{}, false
	}
}

func extractTrawlerCommandFlagDeclarationFacts(
	registerFlags func(*flag.FlagSet),
) []trawlerCommandFlagDeclarationFacts {
	if registerFlags == nil {
		return nil
	}
	flagSet := flag.NewFlagSet("trawler command declaration", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	registerFlags(flagSet)
	return trawlerCommandFlagDeclarationFactsFromFlagSet(flagSet)
}

func builtinSearchCommandFlagDeclarationFacts(
	includeWho bool,
) []trawlerCommandFlagDeclarationFacts {
	flagSet := flag.NewFlagSet("search command declaration", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	defineSearchFlags(flagSet, includeWho)
	return trawlerCommandFlagDeclarationFactsFromFlagSet(flagSet)
}

func builtinConversationCommandFlagDeclarationFacts() []trawlerCommandFlagDeclarationFacts {
	flagSet := flag.NewFlagSet("conversations command declaration", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	defineConversationFlags(flagSet)
	return trawlerCommandFlagDeclarationFactsFromFlagSet(flagSet)
}

func builtinTrawlerMessageListCommandFlagDeclarationFacts() []trawlerCommandFlagDeclarationFacts {
	flagSet := flag.NewFlagSet("messages command declaration", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	defineTrawlerMessageListFlags(flagSet)
	return trawlerCommandFlagDeclarationFactsFromFlagSet(flagSet)
}

func trawlerCommandFlagDeclarationFactsFromFlagSet(
	flagSet *flag.FlagSet,
) []trawlerCommandFlagDeclarationFacts {
	var flagFacts []trawlerCommandFlagDeclarationFacts
	flagSet.VisitAll(func(registeredFlag *flag.Flag) {
		flagFacts = append(flagFacts, trawlerCommandFlagDeclarationFacts{
			name:            registeredFlag.Name,
			helpDescription: registeredFlag.Usage,
			defaultValue:    registeredFlag.DefValue,
		})
	})
	sort.Slice(flagFacts, func(left, right int) bool {
		return flagFacts[left].name < flagFacts[right].name
	})
	return flagFacts
}
