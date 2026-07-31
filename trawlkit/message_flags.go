package trawlkit

import (
	"flag"
	"fmt"
	"io"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type trawlerMessageListFlagSpec struct {
	name  string
	usage string
}

var trawlerMessageListFlagSpecs = []trawlerMessageListFlagSpec{
	{name: "conversation", usage: "Show messages in conversation `LINK`"},
	{name: "limit", usage: "Maximum number of messages"},
}

type trawlerMessageListFlagValues struct {
	optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation *string
	maximumReturnedMessageCount                                                    *int
}

func defineTrawlerMessageListFlags(flagSet *flag.FlagSet) trawlerMessageListFlagValues {
	var values trawlerMessageListFlagValues
	for _, specification := range trawlerMessageListFlagSpecs {
		switch specification.name {
		case "conversation":
			values.optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation =
				flagSet.String(specification.name, "", specification.usage)
		case "limit":
			values.maximumReturnedMessageCount = flagSet.Int(
				specification.name,
				defaultTrawlerMessageListMaximumReturnedMessageCount,
				specification.usage,
			)
		}
	}
	return values
}

func parseTrawlerMessageListQuery(arguments []string) (TrawlerMessageListQuery, error) {
	flagSet := flag.NewFlagSet("messages", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	values := defineTrawlerMessageListFlags(flagSet)
	if err := flagSet.Parse(arguments); err != nil {
		return TrawlerMessageListQuery{}, output.UsageError{Err: err}
	}
	if flagSet.NArg() > 0 {
		return TrawlerMessageListQuery{}, output.UsageError{Err: output.HumanFacingErrorMessage(fmt.Sprintf(
			"Messages takes flags only, not %q.",
			flagSet.Arg(0),
		))}
	}
	limitWasEntered := false
	flagSet.Visit(func(enteredFlag *flag.Flag) {
		if enteredFlag.Name == "limit" {
			limitWasEntered = true
		}
	})
	maximumReturnedMessageCount, err := ckflags.Limit(
		*values.maximumReturnedMessageCount,
		limitWasEntered,
	)
	if err != nil {
		return TrawlerMessageListQuery{}, output.UsageError{Err: err}
	}
	return TrawlerMessageListQuery{
		OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation: *values.optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		MaximumReturnedMessageCount: maximumReturnedMessageCount,
	}, nil
}

func runnerOwnedTrawlerMessageListFlagNames() map[string]struct{} {
	names := make(map[string]struct{}, len(trawlerMessageListFlagSpecs))
	for _, specification := range trawlerMessageListFlagSpecs {
		names[specification.name] = struct{}{}
	}
	return names
}
