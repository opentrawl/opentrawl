package archive

import (
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card"
)

func executionCustody(source cardinput.SourceFacts, artifacts cardinput.CheckedArtifacts, records []place.EvidenceRecord) *cardwire.CardExecutionCustody {
	custody := &cardwire.CardExecutionCustody{SourceId: source.SourceID, AssetId: source.AssetID, ImmutableOriginalResourceId: artifacts.ImmutableOriginal.ResourceID, MetadataRecordId: artifacts.Metadata.RecordID, MetadataProjectionId: artifacts.Metadata.ProjectionID, FullCurrentProofSha256: artifacts.FullCurrent.ProofSHA256}
	for _, record := range records {
		custody.Evidence = append(custody.Evidence, &cardwire.EvidenceLink{ProviderIdentity: record.ProviderIdentity, Operation: record.Operation, RawResponseSha256: record.RawResponseSHA256})
	}
	return custody
}
