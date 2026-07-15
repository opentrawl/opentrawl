package cardinput

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func TestEvidenceInputMustMatchEverySourceField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*place.Input)
	}{
		{"asset id", func(input *place.Input) { input.AssetID = "asset:other" }},
		{"taken at", func(input *place.Input) { input.TakenAt = "2030-01-01T00:00:00Z" }},
		{"latitude", func(input *place.Input) { input.Location.Latitude++ }},
		{"longitude", func(input *place.Input) { input.Location.Longitude++ }},
		{"accuracy", func(input *place.Input) { input.AccuracyMeters++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&records[0].Input)
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, ErrEvidenceMismatch) {
				t.Fatalf("error = %v, want evidence mismatch", err)
			}
		})
	}
}

func TestEveryIncompleteEvidenceStateStops(t *testing.T) {
	states := []string{"", "empty", "malformed", "stale", "failed", "no_result", "limit_saturated", "unsafe"}
	for _, state := range states {
		name := state
		if name == "" {
			name = "blank"
		}
		t.Run(name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			records[0].CompletionState = state
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, ErrIncompleteEvidence) {
				t.Fatalf("error = %v, want incomplete evidence", err)
			}
		})
	}
}

func TestLimitSaturatedEvidenceStopsWithoutMutatingCandidates(t *testing.T) {
	source, artifacts, records := fixtureInput()
	records[1].CompletionState = "limit_saturated"
	before, err := json.Marshal(records[1])
	if err != nil {
		t.Fatal(err)
	}

	_, err = Build(source, artifacts, records)
	if !errors.Is(err, ErrIncompleteEvidence) {
		t.Fatalf("error = %v, want incomplete evidence", err)
	}
	after, err := json.Marshal(records[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("limit-saturated evidence changed")
	}
}

func TestBuildRejectsMoreThanTwentyModelVisibleCandidates(t *testing.T) {
	source, artifacts, records := fixtureInput()
	records[1].Candidates = make([]place.EvidenceCandidate, MaxModelCandidatesPerProjection+1)
	for index := range records[1].Candidates {
		records[1].Candidates[index] = place.EvidenceCandidate{
			ProviderIndex: index,
			ProviderID:    fmt.Sprintf("synthetic-%d", index+1),
			Name:          fmt.Sprintf("Synthetic candidate %d", index+1),
			Source:        "synthetic",
		}
	}

	_, err := Build(source, artifacts, records)
	if !errors.Is(err, ErrUnsafeEvidence) {
		t.Fatalf("error = %v, want unsafe evidence", err)
	}
}

func TestEvidenceSetMustBeExactAndOrdered(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceFacts, []place.EvidenceRecord)
		want   error
	}{
		{"missing record", func(_ *SourceFacts, records []place.EvidenceRecord) { records[1] = place.EvidenceRecord{} }, ErrEvidenceMismatch},
		{"too few records", func(_ *SourceFacts, _ []place.EvidenceRecord) {}, ErrMissingEvidence},
		{"operation mismatch", func(_ *SourceFacts, records []place.EvidenceRecord) { records[0].Operation = "other" }, ErrEvidenceMismatch},
		{"order mismatch", func(_ *SourceFacts, records []place.EvidenceRecord) { records[0], records[1] = records[1], records[0] }, ErrEvidenceMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&source, records)
			if test.name == "too few records" {
				records = records[:1]
			}
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUnsafeEvidenceStopsBeforeProjection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*place.EvidenceRecord)
		want   error
	}{
		{"stop reason", func(record *place.EvidenceRecord) { record.StopReason = "failed" }, ErrUnsafeEvidence},
		{"stop detail", func(record *place.EvidenceRecord) { record.StopDetail = "synthetic failure" }, ErrUnsafeEvidence},
		{"provider error", func(record *place.EvidenceRecord) { record.ProviderErrorClass = "timeout" }, ErrUnsafeEvidence},
		{"request digest", func(record *place.EvidenceRecord) { record.PreAuthRequestSHA256 = "bad" }, ErrUnsafeEvidence},
		{"response digest", func(record *place.EvidenceRecord) { record.RawResponseSHA256 = "bad" }, ErrUnsafeEvidence},
		{"headers digest", func(record *place.EvidenceRecord) { record.RawHeadersSHA256 = "bad" }, ErrUnsafeEvidence},
		{"HTTP failure", func(record *place.EvidenceRecord) { record.HTTPStatus = 500 }, ErrUnsafeEvidence},
		{"no result", func(record *place.EvidenceRecord) { record.Address = nil }, ErrIncompleteEvidence},
		{"empty address", func(record *place.EvidenceRecord) { record.Address = &place.Address{} }, ErrIncompleteEvidence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&records[0])
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMalformedEvidenceAndCandidatesStop(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*place.EvidenceRecord)
	}{
		{"provider identity", func(record *place.EvidenceRecord) { record.ProviderIdentity = "" }},
		{"operation", func(record *place.EvidenceRecord) { record.Operation = "" }},
		{"coordinate variant", func(record *place.EvidenceRecord) { record.CoordinateVariant = "" }},
		{"parser version", func(record *place.EvidenceRecord) { record.ParserVersion = "" }},
		{"candidate index", func(record *place.EvidenceRecord) { record.Candidates[0].ProviderIndex = -1 }},
		{"candidate identity", func(record *place.EvidenceRecord) {
			record.Candidates[0].ProviderID, record.Candidates[0].Name = "", ""
		}},
		{"candidate source", func(record *place.EvidenceRecord) { record.Candidates[0].Source = "" }},
		{"candidate distance", func(record *place.EvidenceRecord) { record.Candidates[0].DistanceM = -1 }},
		{"candidate coordinate", func(record *place.EvidenceRecord) { record.Candidates[0].Coordinate.Latitude = 100 }},
		{"candidate address", func(record *place.EvidenceRecord) { record.Candidates[0].Address = &place.Address{} }},
		{"candidate category", func(record *place.EvidenceRecord) { record.Candidates[0].Categories[0] = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&records[1])
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, ErrMalformedEvidence) && !errors.Is(err, ErrEvidenceMismatch) {
				t.Fatalf("error = %v, want malformed evidence", err)
			}
		})
	}
}

func TestInvalidSourceAndArtefactFactsStop(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceFacts, *CheckedArtifacts)
		want   error
	}{
		{"asset id", func(source *SourceFacts, _ *CheckedArtifacts) { source.AssetID = "" }, ErrInvalidSource},
		{"capture time", func(source *SourceFacts, _ *CheckedArtifacts) { source.CaptureTime = "" }, ErrInvalidSource},
		{"timezone", func(source *SourceFacts, _ *CheckedArtifacts) { empty := ""; source.Timezone = &empty }, ErrInvalidSource},
		{"media type", func(source *SourceFacts, _ *CheckedArtifacts) { source.MediaType = "" }, ErrInvalidSource},
		{"media subtype", func(source *SourceFacts, _ *CheckedArtifacts) { source.MediaSubtypes[0] = "" }, ErrInvalidSource},
		{"width", func(source *SourceFacts, _ *CheckedArtifacts) { source.PixelWidth = 0 }, ErrInvalidSource},
		{"height", func(source *SourceFacts, _ *CheckedArtifacts) { source.PixelHeight = 0 }, ErrInvalidSource},
		{"duration", func(source *SourceFacts, _ *CheckedArtifacts) { source.DurationSeconds = math.NaN() }, ErrInvalidSource},
		{"album", func(source *SourceFacts, _ *CheckedArtifacts) { source.Albums[0].Title = "" }, ErrInvalidSource},
		{"location", func(source *SourceFacts, _ *CheckedArtifacts) { source.Location.Latitude = 100 }, ErrInvalidSource},
		{"accuracy", func(source *SourceFacts, _ *CheckedArtifacts) {
			negative := -1.0
			source.Location.HorizontalAccuracyMeters = &negative
		}, ErrInvalidSource},
		{"known place", func(source *SourceFacts, _ *CheckedArtifacts) { source.KnownPlace.Relationship = "" }, ErrInvalidSource},
		{"camera", func(source *SourceFacts, _ *CheckedArtifacts) {
			invalid := math.NaN()
			source.Camera.Aperture = &invalid
		}, ErrInvalidSource},
		{"duplicate operation", func(source *SourceFacts, _ *CheckedArtifacts) {
			source.RequiredPlaceOperations[1] = source.RequiredPlaceOperations[0]
		}, ErrInvalidSource},
		{"original resource id", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.ImmutableOriginal.ResourceID = "" }, ErrIncompleteArtifact},
		{"metadata record id", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.Metadata.RecordID = "" }, ErrIncompleteArtifact},
		{"metadata projection id", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.Metadata.ProjectionID = "" }, ErrIncompleteArtifact},
		{"full-current proof digest", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.FullCurrent.ProofSHA256 = "" }, ErrIncompleteArtifact},
		{"original digest", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.ImmutableOriginal.Fact.SHA256 = "bad" }, ErrIncompleteArtifact},
		{"metadata projection", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.Metadata.Fact.ProjectionLines = nil }, ErrIncompleteArtifact},
		{"full-current role", func(_ *SourceFacts, artifacts *CheckedArtifacts) { artifacts.FullCurrent.Fact.Role = "" }, ErrIncompleteArtifact},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			test.mutate(&source, &artifacts)
			_, err := Build(source, artifacts, records)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
