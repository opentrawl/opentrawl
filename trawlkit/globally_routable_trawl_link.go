package trawlkit

import (
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

const globallyRoutableTrawlLinkSeparator = ":"

type GloballyRoutableTrawlLinkRoute struct {
	RegisteredTrawler   *RegisteredTrawlerIdentity
	LocalShortReference *LocalTrawlerShortReference
}

func ComposeGloballyRoutableTrawlLink(route GloballyRoutableTrawlLinkRoute) (*GloballyRoutableTrawlLink, error) {
	registeredTrawlerIdentity := RegisteredTrawlerIdentityText(route.RegisteredTrawler)
	localShortReference := LocalTrawlerShortReferenceText(route.LocalShortReference)
	if registeredTrawlerIdentity == "" || strings.Contains(registeredTrawlerIdentity, globallyRoutableTrawlLinkSeparator) {
		return nil, fmt.Errorf("registered trawler identity is not valid for a globally routable trawl link")
	}
	if !shortref.ValidAlias(localShortReference) {
		return nil, fmt.Errorf("local trawler short reference is not valid for a globally routable trawl link")
	}
	return NewGloballyRoutableTrawlLink(
		registeredTrawlerIdentity + globallyRoutableTrawlLinkSeparator + localShortReference,
	), nil
}

func ParseGloballyRoutableTrawlLink(globallyRoutableTrawlLink *GloballyRoutableTrawlLink) (GloballyRoutableTrawlLinkRoute, error) {
	globallyRoutableTrawlLinkText := GloballyRoutableTrawlLinkText(globallyRoutableTrawlLink)
	registeredTrawlerIdentity, localShortReference, foundSeparator := strings.Cut(
		globallyRoutableTrawlLinkText,
		globallyRoutableTrawlLinkSeparator,
	)
	if !foundSeparator {
		return GloballyRoutableTrawlLinkRoute{}, fmt.Errorf("globally routable trawl link has no registered trawler manifest identity")
	}
	route := GloballyRoutableTrawlLinkRoute{
		RegisteredTrawler:   NewRegisteredTrawlerIdentity(registeredTrawlerIdentity),
		LocalShortReference: NewLocalTrawlerShortReference(localShortReference),
	}
	composedGloballyRoutableTrawlLink, err := ComposeGloballyRoutableTrawlLink(route)
	if err != nil || GloballyRoutableTrawlLinkText(composedGloballyRoutableTrawlLink) != globallyRoutableTrawlLinkText {
		return GloballyRoutableTrawlLinkRoute{}, fmt.Errorf("globally routable trawl link is not valid")
	}
	return route, nil
}

func ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(
	globallyRoutableTrawlLinkOrFreeFormArgument string,
	selectedTrawlerManifestIdentity string,
) (
	localShortReferenceOrUnchangedFreeFormArgument string,
	argumentWasGloballyRoutableTrawlLink bool,
	err error,
) {
	trimmedArgument := strings.TrimSpace(globallyRoutableTrawlLinkOrFreeFormArgument)
	globallyRoutableTrawlLinkRoute, parseError := ParseGloballyRoutableTrawlLink(
		NewGloballyRoutableTrawlLink(trimmedArgument),
	)
	if parseError != nil {
		return trimmedArgument, false, nil
	}
	if RegisteredTrawlerIdentityText(globallyRoutableTrawlLinkRoute.RegisteredTrawler) != strings.TrimSpace(selectedTrawlerManifestIdentity) {
		return "", true, output.UsageError{Err: output.HumanFacingErrorMessage("The link is for another trawler.")}
	}
	return LocalTrawlerShortReferenceText(globallyRoutableTrawlLinkRoute.LocalShortReference), true, nil
}
