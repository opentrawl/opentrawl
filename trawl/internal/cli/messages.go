package cli

import (
	"errors"
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
				return nil, false, usageErr{errors.New("--conversation needs a conversation link.")}
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
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(globallyRoutableConversationLink)
	if err != nil {
		return "", usageErr{errors.New("--conversation needs a valid conversation link.")}
	}
	if route.RegisteredTrawlerManifestIdentity != selectedTrawler.RegisteredTrawlerManifestIdentity {
		return "", usageErr{errors.New("The conversation link is for another trawler.")}
	}
	return route.LocalShortReferenceAcceptedByRegisteredTrawler, nil
}

func (c *MessagesCmd) Run(r *Runtime) error {
	if c.ConversationLink == "" {
		return usageErr{errors.New("Messages needs a conversation link.")}
	}
	route, err := trawlkit.ParseGloballyRoutableTrawlLink(c.ConversationLink)
	if err != nil {
		return usageErr{errors.New("--conversation needs a valid conversation link.")}
	}
	trawler, found := findInstalledTrawler(discoverInstalledTrawlers(r.ctx), route.RegisteredTrawlerManifestIdentity)
	if !found {
		return r.writeError("No trawler has that conversation link.")
	}
	trawlerCommandArguments := []string{
		"messages", "--conversation", route.LocalShortReferenceAcceptedByRegisteredTrawler,
	}
	if c.Limit != nil {
		trawlerCommandArguments = append(trawlerCommandArguments, "--limit", strconv.Itoa(*c.Limit))
	}
	return r.runDeclaredTrawlerCommand(
		trawler,
		trawler.RegisteredTrawlerCommandName,
		trawlerCommandArguments,
		[]string{
			"messages", "--conversation", c.ConversationLink,
		},
	)
}
