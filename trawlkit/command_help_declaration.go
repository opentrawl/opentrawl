package trawlkit

import (
	"flag"
	"io"
	"sort"
	"strings"
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
	commandFactsByCommandKey := map[string]trawlerCommandDeclarationFacts{
		"metadata": {name: "metadata", helpDescription: "Show trawler metadata"},
		"status":   {name: "status", helpDescription: "Show archive status"},
	}
	if _, ok := trawler.(Syncer); ok {
		commandFactsByCommandKey["sync"] = trawlerCommandDeclarationFacts{
			name:            "sync",
			helpDescription: "Sync the archive",
		}
	}
	if _, ok := trawler.(Searcher); ok {
		_, supportsWho := trawler.(WhoMatcher)
		commandFactsByCommandKey["search"] = trawlerCommandDeclarationFacts{
			name:                    "search",
			helpDescription:         "Search archive items",
			positionalArgumentNames: []string{"QUERY"},
			flags:                   builtinSearchCommandFlagDeclarationFacts(supportsWho),
		}
	}
	if _, ok := trawler.(RecordOpener); ok {
		commandFactsByCommandKey["open"] = trawlerCommandDeclarationFacts{
			name:                    "open",
			helpDescription:         "Open an item",
			positionalArgumentNames: []string{"LINK"},
		}
	}
	if _, ok := trawler.(WhoMatcher); ok {
		commandFactsByCommandKey["who"] = trawlerCommandDeclarationFacts{
			name:                    "who",
			helpDescription:         "Resolve person",
			positionalArgumentNames: []string{"NAME"},
		}
	}
	if _, ok := trawler.(ConversationLister); ok {
		commandFactsByCommandKey["conversations"] = trawlerCommandDeclarationFacts{
			name:            "conversations",
			helpDescription: "List conversations",
			flags:           builtinConversationCommandFlagDeclarationFacts(),
		}
	}
	if _, ok := trawler.(TrawlerMessageLister); ok {
		commandFactsByCommandKey["messages"] = trawlerCommandDeclarationFacts{
			name:            "messages",
			helpDescription: "List messages",
			flags:           builtinTrawlerMessageListCommandFlagDeclarationFacts(),
		}
	}
	for commandKey, declaration := range sharedCommandDeclarations {
		commandFacts, ok := commandFactsByCommandKey[commandKey]
		if !ok {
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
		commandFactsByCommandKey[commandKey] = commandFacts
	}
	for _, declaration := range trawler.TrawlerCommands() {
		if _, shared := sharedTrawlerCommandName(declaration.TrawlerCommandName); shared {
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
