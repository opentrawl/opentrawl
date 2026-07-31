package render

import (
	"strings"

	identityv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity/v1"
)

type GloballyRoutableTrawlLinkForCanonicalArchiveRecordReference struct {
	CanonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference
	GloballyRoutableTrawlLink       *identityv1.GloballyRoutableTrawlLink
}

type GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference []GloballyRoutableTrawlLinkForCanonicalArchiveRecordReference

func (links GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference) globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
	canonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference,
) *identityv1.GloballyRoutableTrawlLink {
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
	firstCanonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference,
	secondCanonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference,
) bool {
	firstReferenceText := canonicalArchiveRecordReferenceText(firstCanonicalArchiveRecordReference)
	return firstReferenceText != "" &&
		firstReferenceText == canonicalArchiveRecordReferenceText(secondCanonicalArchiveRecordReference)
}

func canonicalArchiveRecordReferenceText(
	canonicalArchiveRecordReference *identityv1.CanonicalArchiveRecordReference,
) string {
	if canonicalArchiveRecordReference == nil {
		return ""
	}
	return strings.TrimSpace(canonicalArchiveRecordReference.GetCanonicalArchiveRecordReference())
}

func globallyRoutableTrawlLinkText(globallyRoutableTrawlLink *identityv1.GloballyRoutableTrawlLink) string {
	if globallyRoutableTrawlLink == nil {
		return ""
	}
	return strings.TrimSpace(globallyRoutableTrawlLink.GetGloballyRoutableTrawlLink())
}
