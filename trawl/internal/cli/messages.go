package cli

import (
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
)

type MessagesCmd struct {
	ConversationLink string `name:"conversation" required:"" placeholder:"LINK" help:"Link from conversations"`
	Limit            *int   `name:"limit" help:"Maximum number of messages"`
}

func replaceGloballyRoutableConversationLinkWithLocalShortReferenceForSelectedTrawler(
	arguments []string,
	selectedTrawler InstalledTrawler,
) ([]string, bool, error) {
	rewrittenArguments := append([]string(nil), arguments...)
	globallyRoutableConversationLinkWasReplaced := false
	for argumentIndex := 0; argumentIndex < len(rewrittenArguments); argumentIndex++ {
		argument := rewrittenArguments[argumentIndex]
		if argument == "--" {
			break
		}
		if argument == "--conversation" || argument == "-conversation" {
			if argumentIndex+1 >= len(rewrittenArguments) {
				return nil, false, usageErr{humanFacingUsageErrorMessage("--conversation needs a conversation link.")}
			}
			localConversationShortReferenceAcceptedBySelectedTrawler, err := localConversationShortReferenceFromGloballyRoutableLinkForSelectedTrawler(
				rewrittenArguments[argumentIndex+1],
				selectedTrawler,
			)
			if err != nil {
				return nil, false, err
			}
			rewrittenArguments[argumentIndex+1] = localConversationShortReferenceAcceptedBySelectedTrawler
			globallyRoutableConversationLinkWasReplaced = true
			argumentIndex++
			continue
		}
		if strings.HasPrefix(argument, "--conversation=") || strings.HasPrefix(argument, "-conversation=") {
			conversationFlagName, globallyRoutableConversationLink, _ := strings.Cut(argument, "=")
			localConversationShortReferenceAcceptedBySelectedTrawler, err := localConversationShortReferenceFromGloballyRoutableLinkForSelectedTrawler(
				globallyRoutableConversationLink,
				selectedTrawler,
			)
			if err != nil {
				return nil, false, err
			}
			rewrittenArguments[argumentIndex] = conversationFlagName + "=" + localConversationShortReferenceAcceptedBySelectedTrawler
			globallyRoutableConversationLinkWasReplaced = true
		}
	}
	return rewrittenArguments, globallyRoutableConversationLinkWasReplaced, nil
}

func localConversationShortReferenceFromGloballyRoutableLinkForSelectedTrawler(
	globallyRoutableConversationLink string,
	selectedTrawler InstalledTrawler,
) (string, error) {
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(
		trawlkit.NewGloballyRoutableTrawlLink(globallyRoutableConversationLink),
	)
	if err != nil {
		return "", usageErr{humanFacingUsageErrorMessage("--conversation needs a valid conversation link.")}
	}
	if trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler) != installedTrawlerIdentityText(selectedTrawler) {
		return "", usageErr{humanFacingUsageErrorMessage("The conversation link is for another trawler.")}
	}
	return trawlkit.LocalTrawlerShortReferenceText(route.LocalShortReference), nil
}

func (c *MessagesCmd) Run(r *Runtime) error {
	if c.ConversationLink == "" {
		return usageErr{humanFacingUsageErrorMessage("Messages needs a conversation link.")}
	}
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(
		trawlkit.NewGloballyRoutableTrawlLink(c.ConversationLink),
	)
	if err != nil {
		return usageErr{humanFacingUsageErrorMessage("--conversation needs a valid conversation link.")}
	}
	trawler, found := findInstalledTrawler(
		discoverInstalledTrawlers(r.ctx),
		trawlkit.RegisteredTrawlerIdentityText(route.RegisteredTrawler),
	)
	if !found {
		return r.writeError("No trawler has that conversation link.")
	}
	trawlerCommandArguments := []string{
		"messages", "--conversation", trawlkit.LocalTrawlerShortReferenceText(route.LocalShortReference),
	}
	if c.Limit != nil {
		trawlerCommandArguments = append(trawlerCommandArguments, "--limit", strconv.Itoa(*c.Limit))
	}
	return r.runDeclaredTrawlerCommand(
		trawler,
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandName(),
		trawlerCommandArguments,
		[]string{
			"messages", "--conversation", c.ConversationLink,
		},
	)
}
