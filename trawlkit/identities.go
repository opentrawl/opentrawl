package trawlkit

import (
	"strings"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
)

type RegisteredTrawlerIdentity = identity.RegisteredTrawlerIdentity
type CanonicalArchiveRecordReference = identity.CanonicalArchiveRecordReference
type LocalTrawlerShortReference = identity.LocalTrawlerShortReference
type GloballyRoutableTrawlLink = identity.GloballyRoutableTrawlLink
type RecordAnchorIdentifier = identity.RecordAnchorIdentifier

func NewRegisteredTrawlerIdentity(registeredTrawlerIdentity string) *RegisteredTrawlerIdentity {
	return &RegisteredTrawlerIdentity{RegisteredTrawlerIdentity: strings.TrimSpace(registeredTrawlerIdentity)}
}

func RegisteredTrawlerIdentityText(identity *RegisteredTrawlerIdentity) string {
	if identity == nil {
		return ""
	}
	return strings.TrimSpace(identity.GetRegisteredTrawlerIdentity())
}

func NewCanonicalArchiveRecordReference(canonicalArchiveRecordReference string) *CanonicalArchiveRecordReference {
	return &CanonicalArchiveRecordReference{CanonicalArchiveRecordReference: strings.TrimSpace(canonicalArchiveRecordReference)}
}

func CanonicalArchiveRecordReferenceText(reference *CanonicalArchiveRecordReference) string {
	if reference == nil {
		return ""
	}
	return strings.TrimSpace(reference.GetCanonicalArchiveRecordReference())
}

func NewLocalTrawlerShortReference(localTrawlerShortReference string) *LocalTrawlerShortReference {
	return &LocalTrawlerShortReference{LocalTrawlerShortReference: strings.TrimSpace(localTrawlerShortReference)}
}

func LocalTrawlerShortReferenceText(reference *LocalTrawlerShortReference) string {
	if reference == nil {
		return ""
	}
	return strings.TrimSpace(reference.GetLocalTrawlerShortReference())
}

func NewGloballyRoutableTrawlLink(globallyRoutableTrawlLink string) *GloballyRoutableTrawlLink {
	return &GloballyRoutableTrawlLink{GloballyRoutableTrawlLink: strings.TrimSpace(globallyRoutableTrawlLink)}
}

func GloballyRoutableTrawlLinkText(link *GloballyRoutableTrawlLink) string {
	if link == nil {
		return ""
	}
	return strings.TrimSpace(link.GetGloballyRoutableTrawlLink())
}

func NewRecordAnchorIdentifier(recordAnchorIdentifier string) *RecordAnchorIdentifier {
	return &RecordAnchorIdentifier{RecordAnchorIdentifier: strings.TrimSpace(recordAnchorIdentifier)}
}

func RecordAnchorIdentifierText(anchor *RecordAnchorIdentifier) string {
	if anchor == nil {
		return ""
	}
	return strings.TrimSpace(anchor.GetRecordAnchorIdentifier())
}
