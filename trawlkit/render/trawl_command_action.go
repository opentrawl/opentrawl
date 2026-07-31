package render

import (
	"io"
	"strings"

	identityv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity/v1"
)

type TrawlCommandArgument interface {
	isTrawlCommandArgument()
}

type TrawlCommandTextArgument struct {
	Text string
}

func (TrawlCommandTextArgument) isTrawlCommandArgument() {}

type TrawlCommandCanonicalArchiveRecordReferenceArgument struct {
	CanonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference
}

func (TrawlCommandCanonicalArchiveRecordReferenceArgument) isTrawlCommandArgument() {}

type TrawlCommandAction struct {
	TrawlCommandActionDisplayName               string
	CommandArgumentsAfterTrawlInvocationInOrder []TrawlCommandArgument
}

type TrawlerSpecificCommandActions struct {
	ListRowActionsInDisplayOrder []*TrawlCommandAction
	DetailActionsInDisplayOrder  []*TrawlCommandAction
}

func trawlCommandActionLineForDisplay(
	writer io.Writer,
	action *TrawlCommandAction,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) string {
	if action == nil {
		return ""
	}
	argumentsAfterTrawlInvocation := make(
		[]string,
		0,
		len(action.CommandArgumentsAfterTrawlInvocationInOrder),
	)
	for _, commandArgument := range action.CommandArgumentsAfterTrawlInvocationInOrder {
		switch typedCommandArgument := commandArgument.(type) {
		case TrawlCommandTextArgument:
			argumentsAfterTrawlInvocation = append(
				argumentsAfterTrawlInvocation,
				strings.TrimSpace(typedCommandArgument.Text),
			)
		case TrawlCommandCanonicalArchiveRecordReferenceArgument:
			argumentsAfterTrawlInvocation = append(
				argumentsAfterTrawlInvocation,
				globallyRoutableTrawlLinkText(
					globallyRoutableTrawlLinksByCanonicalRecordReference.
						globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
							typedCommandArgument.CanonicalArchiveRecordReference,
						),
				),
			)
		}
	}
	return trawlCommandLineForDisplay(writer, argumentsAfterTrawlInvocation)
}
