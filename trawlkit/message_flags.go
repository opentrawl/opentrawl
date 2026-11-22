package trawlkit

import (
	"flag"
	"io"
	"strings"
	"time"

	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type trawlerMessageListFlagSpec struct {
	name  string
	usage string
}

var trawlerMessageListFlagSpecs = []trawlerMessageListFlagSpec{
	{name: "conversation", usage: "Show messages in conversation `LINK`"},
	{name: "after", usage: render.OnOrAfterDateOptionDescription},
	{name: "before", usage: render.OnOrBeforeDateOptionDescription},
	{name: "limit", usage: "Maximum number of messages"},
}

type trawlerMessageListFlagValues struct {
	optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation *string
	after                                                                          *string
	before                                                                         *string
	maximumReturnedMessageCount                                                    *int
}

func defineTrawlerMessageListFlags(flagSet *flag.FlagSet) trawlerMessageListFlagValues {
	var values trawlerMessageListFlagValues
	for _, specification := range trawlerMessageListFlagSpecs {
		switch specification.name {
		case "conversation":
			values.optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation =
				flagSet.String(specification.name, "", specification.usage)
		case "after":
			values.after = flagSet.String(specification.name, "", specification.usage)
		case "before":
			values.before = flagSet.String(specification.name, "", specification.usage)
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
		return TrawlerMessageListQuery{}, output.UsageError{Err: commandFailureFromGoFlagParseError(err)}
	}
	if flagSet.NArg() > 0 {
		return TrawlerMessageListQuery{}, output.UsageError{Err: render.CommandHasTooManyArguments()}
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
	var afterTime, beforeTime time.Time
	if afterText := strings.TrimSpace(*values.after); afterText != "" {
		afterTime, err = ckflags.Date(afterText)
		if err != nil {
			return TrawlerMessageListQuery{}, output.UsageError{Err: render.DateOptionNeedsTime("after")}
		}
	}
	if beforeText := strings.TrimSpace(*values.before); beforeText != "" {
		beforeTime, err = ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(beforeText)
		if err != nil {
			return TrawlerMessageListQuery{}, output.UsageError{Err: render.DateOptionNeedsTime("before")}
		}
	}
	if !afterTime.IsZero() && !beforeTime.IsZero() && afterTime.After(beforeTime) {
		return TrawlerMessageListQuery{}, output.UsageError{Err: render.AfterMustNotBeLaterThanBefore()}
	}
	return TrawlerMessageListQuery{
		OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation: *values.optionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation,
		After:                       afterTime,
		Before:                      beforeTime,
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
