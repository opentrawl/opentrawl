package trawlkit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

const globallyRoutableTrawlLinkSeparator = ":"

type GloballyRoutableTrawlLinkRoute struct {
	RegisteredTrawlerManifestIdentity              string
	LocalShortReferenceAcceptedByRegisteredTrawler string
}

func ComposeGloballyRoutableTrawlLink(route GloballyRoutableTrawlLinkRoute) (string, error) {
	registeredTrawlerManifestIdentity := strings.TrimSpace(route.RegisteredTrawlerManifestIdentity)
	localShortReferenceAcceptedByRegisteredTrawler := strings.TrimSpace(route.LocalShortReferenceAcceptedByRegisteredTrawler)
	if registeredTrawlerManifestIdentity == "" || strings.Contains(registeredTrawlerManifestIdentity, globallyRoutableTrawlLinkSeparator) {
		return "", fmt.Errorf("registered trawler manifest identity is not valid for a globally routable trawl link")
	}
	if !shortref.ValidAlias(localShortReferenceAcceptedByRegisteredTrawler) {
		return "", fmt.Errorf("registered trawler short reference is not valid for a globally routable trawl link")
	}
	return registeredTrawlerManifestIdentity + globallyRoutableTrawlLinkSeparator + localShortReferenceAcceptedByRegisteredTrawler, nil
}

func ComposeGloballyRoutableTrawlLinksByCanonicalRecordReference(
	registeredTrawlerManifestIdentity string,
	localShortReferenceAliasesByCanonicalRecordReference map[string]string,
) (map[string]string, error) {
	globallyRoutableTrawlLinksByCanonicalRecordReference := make(map[string]string, len(localShortReferenceAliasesByCanonicalRecordReference))
	for canonicalRecordReference, localShortReferenceAlias := range localShortReferenceAliasesByCanonicalRecordReference {
		globallyRoutableTrawlLink, err := ComposeGloballyRoutableTrawlLink(GloballyRoutableTrawlLinkRoute{
			RegisteredTrawlerManifestIdentity:              registeredTrawlerManifestIdentity,
			LocalShortReferenceAcceptedByRegisteredTrawler: localShortReferenceAlias,
		})
		if err != nil {
			return nil, fmt.Errorf("trawler command link: %w", err)
		}
		globallyRoutableTrawlLinksByCanonicalRecordReference[canonicalRecordReference] = globallyRoutableTrawlLink
	}
	return globallyRoutableTrawlLinksByCanonicalRecordReference, nil
}

func ParseGloballyRoutableTrawlLink(globallyRoutableTrawlLink string) (GloballyRoutableTrawlLinkRoute, error) {
	registeredTrawlerManifestIdentity, localShortReferenceAcceptedByRegisteredTrawler, foundSeparator := strings.Cut(
		strings.TrimSpace(globallyRoutableTrawlLink),
		globallyRoutableTrawlLinkSeparator,
	)
	if !foundSeparator {
		return GloballyRoutableTrawlLinkRoute{}, fmt.Errorf("globally routable trawl link has no registered trawler manifest identity")
	}
	route := GloballyRoutableTrawlLinkRoute{
		RegisteredTrawlerManifestIdentity:              registeredTrawlerManifestIdentity,
		LocalShortReferenceAcceptedByRegisteredTrawler: localShortReferenceAcceptedByRegisteredTrawler,
	}
	composedGloballyRoutableTrawlLink, err := ComposeGloballyRoutableTrawlLink(route)
	if err != nil || composedGloballyRoutableTrawlLink != strings.TrimSpace(globallyRoutableTrawlLink) {
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
	globallyRoutableTrawlLinkRoute, parseError := ParseGloballyRoutableTrawlLink(trimmedArgument)
	if parseError != nil {
		return trimmedArgument, false, nil
	}
	if globallyRoutableTrawlLinkRoute.RegisteredTrawlerManifestIdentity != strings.TrimSpace(selectedTrawlerManifestIdentity) {
		return "", true, output.UsageError{Err: errors.New("The link is for another trawler.")}
	}
	return globallyRoutableTrawlLinkRoute.LocalShortReferenceAcceptedByRegisteredTrawler, true, nil
}
