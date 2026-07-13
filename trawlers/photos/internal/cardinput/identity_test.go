package cardinput

import (
	"errors"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func TestCustodyAndOperationFieldsDoNotAffectContentIdentity(t *testing.T) {
	source, artifacts, records := fixtureInput()
	baseline := requireBuild(t, source, artifacts, records)
	validOtherHeadersDigest := digest("other-headers")

	tests := []struct {
		name   string
		mutate func(*SourceFacts, *CheckedArtifacts, []place.EvidenceRecord)
	}{
		{"source id", func(source *SourceFacts, _ *CheckedArtifacts, _ []place.EvidenceRecord) {
			source.SourceID = "source:other"
		}},
		{"original resource id", func(_ *SourceFacts, artifacts *CheckedArtifacts, _ []place.EvidenceRecord) {
			artifacts.ImmutableOriginal.ResourceID = "resource:other"
		}},
		{"metadata record id", func(_ *SourceFacts, artifacts *CheckedArtifacts, _ []place.EvidenceRecord) {
			artifacts.Metadata.RecordID = "metadata-record:other"
		}},
		{"metadata projection id", func(_ *SourceFacts, artifacts *CheckedArtifacts, _ []place.EvidenceRecord) {
			artifacts.Metadata.ProjectionID = "metadata-projection:other"
		}},
		{"full-current proof digest", func(_ *SourceFacts, artifacts *CheckedArtifacts, _ []place.EvidenceRecord) {
			artifacts.FullCurrent.ProofSHA256 = digest("other-current-proof")
		}},
		{"input image path", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].Input.ImagePath = "/other/image"
		}},
		{"request path", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].PreAuthRequestFile = "/other/request"
		}},
		{"response path", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].RawResponseFile = "/other/response"
		}},
		{"headers path", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].RawHeadersFile = "/other/headers"
		}},
		{"headers digest", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].RawHeadersSHA256 = validOtherHeadersDigest
		}},
		{"HTTP status", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) { records[0].HTTPStatus = 201 }},
		{"cache identity", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].CacheIdentity = "cache:other"
		}},
		{"cached flag", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].Cached = !records[0].Cached
		}},
		{"record directory", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].RecordDir = "/other/record"
		}},
		{"credential reference", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].CredentialReference = "OTHER_KEY"
		}},
		{"start time", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].StartedAt = "2030-01-01T00:00:00Z"
		}},
		{"completion time", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].CompletedAt = "2030-01-01T00:00:01Z"
		}},
		{"duration", func(_ *SourceFacts, _ *CheckedArtifacts, records []place.EvidenceRecord) {
			records[0].DurationMilliseconds = 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&source, &artifacts, records)
			result := requireBuild(t, source, artifacts, records)
			if result.ID != baseline.ID || string(result.Bytes) != string(baseline.Bytes) {
				t.Fatalf("custody mutation changed identity: %q, want %q", result.ID, baseline.ID)
			}
		})
	}
}

func TestIdenticalContentAcrossAssetsSharesContentIdentity(t *testing.T) {
	firstSource, artifacts, firstRecords := fixtureInput()
	first := requireBuild(t, firstSource, artifacts, firstRecords)

	secondSource, secondArtifacts, secondRecords := fixtureInput()
	secondSource.AssetID = "asset:synthetic-two"
	secondSource.SourceID = "source:synthetic-other-library"
	secondArtifacts.ImmutableOriginal.ResourceID = "resource:synthetic-two"
	secondArtifacts.Metadata.RecordID = "metadata-record:synthetic-two"
	secondArtifacts.Metadata.ProjectionID = "metadata-projection:synthetic-two"
	secondArtifacts.FullCurrent.ProofSHA256 = digest("synthetic-two-current-proof")
	for index := range secondRecords {
		secondRecords[index].Input.AssetID = secondSource.AssetID
	}
	second := requireBuild(t, secondSource, secondArtifacts, secondRecords)

	if first.ID != second.ID || string(first.Bytes) != string(second.Bytes) {
		t.Fatalf("identical content got %q and %q", first.ID, second.ID)
	}
}

func TestEveryCheckedArtifactFactMustMatchSource(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CheckedArtifacts)
	}{
		{"original resource type", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.ResourceType = "alternate" }},
		{"original UTI", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.UTI = "public.jpeg" }},
		{"original filename", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.Filename = "other.heic" }},
		{"original availability", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.Availability = "remote" }},
		{"original size", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.SizeBytes++ }},
		{"original digest", func(value *CheckedArtifacts) { value.ImmutableOriginal.Fact.SHA256 = digest("other-original") }},
		{"metadata record digest", func(value *CheckedArtifacts) { value.Metadata.Fact.RecordSHA256 = digest("other-record") }},
		{"metadata projection digest", func(value *CheckedArtifacts) { value.Metadata.Fact.ProjectionSHA256 = digest("other-projection") }},
		{"metadata projection", func(value *CheckedArtifacts) { value.Metadata.Fact.ProjectionLines[0] = "Camera: Other" }},
		{"full-current role", func(value *CheckedArtifacts) { value.FullCurrent.Fact.Role = "other_current" }},
		{"full-current media type", func(value *CheckedArtifacts) { value.FullCurrent.Fact.MediaType = "public.png" }},
		{"full-current orientation", func(value *CheckedArtifacts) { value.FullCurrent.Fact.Orientation++ }},
		{"full-current width", func(value *CheckedArtifacts) { value.FullCurrent.Fact.PixelWidth++ }},
		{"full-current height", func(value *CheckedArtifacts) { value.FullCurrent.Fact.PixelHeight++ }},
		{"full-current size", func(value *CheckedArtifacts) { value.FullCurrent.Fact.SizeBytes++ }},
		{"full-current digest", func(value *CheckedArtifacts) { value.FullCurrent.Fact.SHA256 = digest("other-current") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&artifacts)
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, ErrArtifactMismatch) && !errors.Is(err, ErrIncompleteArtifact) {
				t.Fatalf("error = %v, want checked artefact mismatch", err)
			}
		})
	}
}
