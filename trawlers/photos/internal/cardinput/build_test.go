package cardinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestBuildProjectsEveryModelConsumedFieldInCallerOrder(t *testing.T) {
	source, artifacts, records := fixtureInput()
	first := requireBuild(t, source, artifacts, records)
	second := requireBuild(t, source, artifacts, records)

	if !bytes.Equal(first.Bytes, second.Bytes) || first.ID != second.ID {
		t.Fatalf("repeated build changed identity: %q and %q", first.ID, second.ID)
	}
	if !proto.Equal(first.Input, expectedInput(source, records)) {
		t.Fatalf("CardInput lost or changed a checked field:\n got: %s\nwant: %s", first.Input, expectedInput(source, records))
	}
	digest := sha256.Sum256(first.Bytes)
	wantID := "card_input:" + hex.EncodeToString(digest[:])
	if first.ID != wantID {
		t.Fatalf("id = %q, want %q", first.ID, wantID)
	}
	const stableFixtureID = "card_input:ef097069ae8fee935423738b39d4f19feadfc92a63a4cc49cc4ce9342c35a9e3"
	if first.ID != stableFixtureID {
		t.Fatalf("fixture identity = %q, want %q", first.ID, stableFixtureID)
	}
	if got := first.Input.Places[1].Candidates; got[0].Name != "Zulu synthetic venue" || got[1].Name != "Alpha synthetic venue" {
		t.Fatalf("provider order changed: %#v", got)
	}
}

func TestBuildPreservesProviderNativeCandidateIndices(t *testing.T) {
	source, artifacts, records := fixtureInput()
	records[1].Candidates = append(records[1].Candidates, records[1].Candidates[0], records[1].Candidates[1])
	records[1].Candidates[0].ProviderIndex = 0
	records[1].Candidates[1].ProviderIndex = 1
	records[1].Candidates[2].ProviderIndex = 0
	records[1].Candidates[3].ProviderIndex = 1

	first := requireBuild(t, source, artifacts, records)
	second := requireBuild(t, source, artifacts, records)
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatal("repeated build changed provider-native candidate indices")
	}
	for index, want := range []int32{0, 1, 0, 1} {
		if got := first.Input.Places[1].Candidates[index].ProviderIndex; got != want {
			t.Fatalf("candidate %d provider index = %d, want %d", index, got, want)
		}
	}
}

func TestBuildPreservesOptionalPresenceAndBooleanValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SourceFacts, *CheckedArtifacts)
		check  func(*testing.T, Result)
	}{
		{
			name: "timezone and accuracy present",
			check: func(t *testing.T, result Result) {
				if result.Input.Timezone == nil || *result.Input.Timezone != "Europe/Amsterdam" || result.Input.Location.HorizontalAccuracyMeters == nil || *result.Input.Location.HorizontalAccuracyMeters != 8.5 {
					t.Fatalf("optional fields = %#v, %#v", result.Input.Timezone, result.Input.Location.HorizontalAccuracyMeters)
				}
			},
		},
		{
			name: "timezone and accuracy absent",
			mutate: func(source *SourceFacts, _ *CheckedArtifacts) {
				source.Timezone = nil
				source.Location.HorizontalAccuracyMeters = nil
			},
			check: func(t *testing.T, result Result) {
				if result.Input.Timezone != nil || result.Input.Location.HorizontalAccuracyMeters != nil {
					t.Fatalf("absent fields became present: %#v, %#v", result.Input.Timezone, result.Input.Location.HorizontalAccuracyMeters)
				}
			},
		},
		{
			name: "albums absent and booleans false",
			mutate: func(source *SourceFacts, _ *CheckedArtifacts) {
				source.Albums = nil
				source.Favorite = false
				source.BurstMember = false
			},
			check: func(t *testing.T, result Result) {
				if len(result.Input.Albums) != 0 || result.Input.Favorite || result.Input.Hidden || result.Input.BurstMember {
					t.Fatalf("albums or booleans changed: %#v", result.Input)
				}
			},
		},
		{
			name:   "hidden true",
			mutate: func(source *SourceFacts, _ *CheckedArtifacts) { source.Hidden = true },
			check: func(t *testing.T, result Result) {
				if !result.Input.Hidden {
					t.Fatal("hidden did not survive projection")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, artifacts, records := fixtureInput()
			if test.mutate != nil {
				test.mutate(&source, &artifacts)
			}
			for index := range records {
				records[index].Input = evidenceInput(source)
			}
			test.check(t, requireBuild(t, source, artifacts, records))
		})
	}
}

func TestBuildDoesNotProjectUnknownAccuracyAsNegativeMetres(t *testing.T) {
	source, artifacts, records := fixtureInput()
	source.Location.HorizontalAccuracyMeters = nil
	for index := range records {
		records[index].Input.AccuracyMeters = -1
	}
	result := requireBuild(t, source, artifacts, records)
	if result.Input.Location.HorizontalAccuracyMeters != nil {
		t.Fatalf("unknown accuracy = %v, want absent", *result.Input.Location.HorizontalAccuracyMeters)
	}
}

func TestBuildPreservesAbsentOptionalContext(t *testing.T) {
	source, artifacts, _ := fixtureInput()
	source.Timezone = nil
	source.Albums = nil
	source.Location = nil
	source.KnownPlace = nil
	source.Camera = nil
	source.RequiredPlaceOperations = nil
	result := requireBuild(t, source, artifacts, nil)
	if result.Input.Timezone != nil || len(result.Input.Albums) != 0 || result.Input.Location != nil || result.Input.KnownPlace != nil || result.Input.Camera != nil || len(result.Input.Places) != 0 {
		t.Fatalf("absent optional context became present: %#v", result.Input)
	}
}

func TestBuildPreservesAbsentCameraMeasurements(t *testing.T) {
	source, artifacts, records := fixtureInput()
	source.Camera.FocalLengthMM = nil
	source.Camera.FocalLength35MM = nil
	source.Camera.Aperture = nil
	source.Camera.ShutterSpeed = nil
	source.Camera.ISO = nil
	result := requireBuild(t, source, artifacts, records)
	if result.Input.Camera.FocalLengthMm != nil || result.Input.Camera.FocalLength_35Mm != nil || result.Input.Camera.Aperture != nil || result.Input.Camera.ShutterSpeed != nil || result.Input.Camera.Iso != nil {
		t.Fatalf("absent camera measurements became present: %#v", result.Input.Camera)
	}
}
