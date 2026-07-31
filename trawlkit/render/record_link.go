package render

import (
	"strings"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
)

type GloballyRoutableTrawlLinkForCanonicalArchiveRecordReference struct {
	CanonicalArchiveRecordReference *identity.CanonicalArchiveRecordReference
	GloballyRoutableTrawlLink       *identity.GloballyRoutableTrawlLink
}

type GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference []GloballyRoutableTrawlLinkForCanonicalArchiveRecordReference

func (links GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference) globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
	canonicalArchiveRecordReference *identity.CanonicalArchiveRecordReference,
) *identity.GloballyRoutableTrawlLink {
	for _, link := range links {
		if canonicalArchiveRecordReferencesMatch(
			link.CanonicalArchiveRecordReference,
			canonicalArchiveRecordReference,
		) {
			return link.GloballyRoutableTrawlLink
		}
	}
	return nil
}

func canonicalArchiveRecordReferencesMatch(
	firstCanonicalArchiveRecordReference *identity.CanonicalArchiveRecordReference,
	secondCanonicalArchiveRecordReference *identity.CanonicalArchiveRecordReference,
) bool {
	firstReferenceText := canonicalArchiveRecordReferenceText(firstCanonicalArchiveRecordReference)
	return firstReferenceText != "" &&
		firstReferenceText == canonicalArchiveRecordReferenceText(secondCanonicalArchiveRecordReference)
}

func canonicalArchiveRecordReferenceText(
	canonicalArchiveRecordReference *identity.CanonicalArchiveRecordReference,
) string {
	if canonicalArchiveRecordReference == nil {
		return ""
	}
	return strings.TrimSpace(canonicalArchiveRecordReference.GetCanonicalArchiveRecordReference())
}

func globallyRoutableTrawlLinkText(globallyRoutableTrawlLink *identity.GloballyRoutableTrawlLink) string {
	if globallyRoutableTrawlLink == nil {
		return ""
	}
	return strings.TrimSpace(globallyRoutableTrawlLink.GetGloballyRoutableTrawlLink())
}
