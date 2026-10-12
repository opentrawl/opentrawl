package cli

import (
	"context"
	"flag"
	"sort"
	"strings"

	"github.com/alecthomas/kong"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// Help describes commands as typed values; trawlkit/render writes them.

func trawlHelpPrinter(output render.TrawlCommandOutput) kong.HelpPrinter {
	return func(_ kong.HelpOptions, ctx *kong.Context) error {
		if selectedCommand := ctx.Selected(); selectedCommand != nil {
			return output.WriteCommandHelp(commandHelpFromKongNode(selectedCommand))
		}
		return output.WriteTrawlHelp(trawlOverviewHelp(ctx.Model))
	}
}

func trawlOverviewHelp(application *kong.Application) render.TrawlOverviewHelp {
	overview := render.TrawlOverviewHelp{
		GlobalFlagsInDisplayOrder: []render.CommandFlagHelp{
			{FlagLongName: "help", FlagShortName: "h", FlagDescription: "Show help"},
			{FlagLongName: "verbose", FlagShortName: "v", FlagDescription: "Show detailed progress; use -vv for debug detail"},
			{FlagLongName: "version", FlagDescription: "Show the version"},
		},
	}
	for _, commandNode := range application.Children {
		if commandNode == nil || commandNode.Hidden {
			continue
		}
		overview.BuiltInCommandsInDisplayOrder = append(
			overview.BuiltInCommandsInDisplayOrder,
			commandHelpFromKongNode(commandNode),
		)
	}
	for _, trawler := range discoverInstalledTrawlers(context.Background()) {
		overview.InstalledTrawlersInDisplayOrder = append(overview.InstalledTrawlersInDisplayOrder, render.TrawlerOverviewHelp{
			TrawlerCommandName:                 trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName(),
			TrawlerCommandNamesShownInOverview: trawlerCommandNamesShownInBareTrawlOverview(trawler),
			TrawlerCommandsInDisplayOrder:      namespaceCommandHelp(trawler),
		})
	}
	return overview
}

func commandHelpFromKongNode(commandNode *kong.Node) render.CommandHelp {
	commandHelp := render.CommandHelp{
		CommandWordsAfterTrawlInvocation: strings.Fields(commandNode.Path()),
		CommandDescription:               commandNode.Help,
	}
	for _, positionalArgument := range commandNode.Positional {
		commandHelp.CommandPositionalArgumentsInOrder = append(
			commandHelp.CommandPositionalArgumentsInOrder,
			render.CommandPositionalArgumentHelp{
				ArgumentPlaceholder: positionalArgument.Name,
				ArgumentIsOptional:  !positionalArgument.Required,
				ArgumentRepeats:     positionalArgument.IsSlice(),
				ArgumentDescription: positionalArgument.Help,
			},
		)
	}
	for _, flagGroup := range commandNode.AllFlags(true) {
		for _, commandFlag := range flagGroup {
			flagHelp := render.CommandFlagHelp{
				FlagLongName:    commandFlag.Name,
				FlagIsRequired:  commandFlag.Required,
				FlagDescription: commandFlag.Help,
			}
			if commandFlag.Short != 0 {
				flagHelp.FlagShortName = string(commandFlag.Short)
			}
			if !commandFlag.IsBool() && !commandFlag.IsCounter() {
				flagHelp.FlagValuePlaceholder = commandFlag.FormatPlaceHolder()
			}
			commandHelp.CommandFlagsInDisplayOrder = append(commandHelp.CommandFlagsInDisplayOrder, flagHelp)
		}
	}
	return commandHelp
}

// commandHelpFromTrawlerCommandDeclaration describes a trawler's own command
// from its manifest.
func commandHelpFromTrawlerCommandDeclaration(
	trawler InstalledTrawler,
	command *federation.RegisteredTrawlerCommandDeclaration,
) render.CommandHelp {
	commandHelp := render.CommandHelp{
		CommandWordsAfterTrawlInvocation: append(
			[]string{trawlerCommandToken(trawler)},
			strings.Fields(registeredTrawlerCommandName(command))...,
		),
		CommandDescription:         strings.TrimSpace(command.GetTrawlerCommandHelpDescription()),
		CommandFlagsInDisplayOrder: trawlerCommandFlagHelp(command),
	}
	for _, positionalArgumentName := range command.GetTrawlerCommandPositionalArgumentNames() {
		positionalArgumentName = strings.TrimSpace(positionalArgumentName)
		commandHelp.CommandPositionalArgumentsInOrder = append(
			commandHelp.CommandPositionalArgumentsInOrder,
			render.CommandPositionalArgumentHelp{
				ArgumentPlaceholder: strings.Trim(positionalArgumentName, "[]"),
				ArgumentIsOptional:  strings.HasPrefix(positionalArgumentName, "["),
			},
		)
	}
	return commandHelp
}

// trawlerCommandFlagHelp reads Go flag-package declarations, where a
// back-quoted word in the help names the value.
func trawlerCommandFlagHelp(command *federation.RegisteredTrawlerCommandDeclaration) []render.CommandFlagHelp {
	declarations := command.GetTrawlerCommandFlagDeclarations()
	flags := make([]render.CommandFlagHelp, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration == nil {
			continue
		}
		rawHelpDescription := declaration.GetTrawlerCommandFlagHelpDescription()
		valuePlaceholder, helpDescription := flag.UnquoteUsage(&flag.Flag{Usage: rawHelpDescription})
		if helpDescription == rawHelpDescription {
			valuePlaceholder = "value"
		}
		defaultValue := strings.ToLower(strings.TrimSpace(declaration.GetTrawlerCommandFlagDefaultValue()))
		if defaultValue == "true" || defaultValue == "false" {
			valuePlaceholder = ""
		}
		switch declaration.GetTrawlerCommandFlagName() {
		case "limit":
			valuePlaceholder = "count"
		case "after", "before":
			valuePlaceholder = "date"
		}
		flags = append(flags, render.CommandFlagHelp{
			FlagLongName:         declaration.GetTrawlerCommandFlagName(),
			FlagValuePlaceholder: valuePlaceholder,
			FlagDescription:      helpDescription,
		})
	}
	sort.Slice(flags, func(left, right int) bool { return flags[left].FlagLongName < flags[right].FlagLongName })
	return flags
}
