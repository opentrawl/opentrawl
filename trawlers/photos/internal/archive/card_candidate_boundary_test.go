package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestPreparedCardCarriesUpToTwentyCheckedCandidatesWithoutReordering(t *testing.T) {
	counts := []int{0, 5, 6, cardinput.MaxModelCandidatesPerProjection}
	for _, count := range counts {
		t.Run(fmt.Sprintf("%d candidates", count), func(t *testing.T) {
			preparation := fixtureCardPreparationFor("asset:model-candidates")
			preparation.Evidence[0].Address = &place.Address{Formatted: "Synthetic place", Source: "synthetic"}
			preparation.Evidence[0].Candidates = syntheticModelCandidates(count)
			classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := renderPreparedCardRequest(preparation.Source, preparation.Artifacts, preparation.Evidence, preparation.CurrentStill, classifier)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(prepared.Input.Input.GetPlaces()[0].GetCandidates()); got != count {
				t.Fatalf("CardInput candidates = %d, want %d", got, count)
			}
			inputJSON, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(prepared.Input.Input)
			if err != nil {
				t.Fatal(err)
			}
			for _, boundary := range [][]byte{inputJSON, prepared.Request.Body()} {
				position := -1
				for index := 1; index <= count; index++ {
					providerID := fmt.Sprintf("provider-%02d", index)
					next := bytes.Index(boundary[position+1:], []byte(providerID))
					if next < 0 {
						t.Fatalf("boundary omitted %q: %s", providerID, boundary)
					}
					position += next + 1
				}
			}
			if got := len(prepared.CandidatesInSeq); got != count {
				t.Fatalf("candidate registry = %d, want %d", got, count)
			}
			if count != cardinput.MaxModelCandidatesPerProjection {
				return
			}
			item, err := prepareCard(preparedCard{source: preparation.Source, artifacts: preparation.Artifacts, evidence: preparation.Evidence, classify: preparation.Classify, currentStill: preparation.CurrentStill, classifier: classifier}, 1)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := restorePreparedCardRequestUnchecked(item)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(restored.Input.Bytes, item.GetCardInput()) || !bytes.Equal(restored.Request.Body(), prepared.Request.Body()) || !preparedCandidatesEqual(prepared.CandidateByID, prepared.CandidatesInSeq, restored.CandidateByID, restored.CandidatesInSeq) {
				t.Fatal("restored approved request changed the checked CardInput, model request or candidate registry")
			}
			arguments, err := json.Marshal(map[string]any{
				"summary": "Synthetic scene.", "description": "A synthetic scene with visible evidence.",
				"location":     map[string]string{"kind": "candidate", "candidate_id": "place_1_candidate_20", "inferred_name": "", "confidence": "high", "reason": "The twentieth supplied candidate matches the synthetic sign."},
				"visible_text": "SYNTHETIC", "uncertainties": []string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			card, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: arguments}}, prepared)
			if err != nil || card.Location.CandidateID != "place_1_candidate_20" {
				t.Fatalf("position-20 card = %#v, error = %v", card, err)
			}
			locationText := ""
			for _, observation := range observationsFromCard(card, prepared) {
				if observation.ObservationType == modelObservationCardLocation {
					locationText = observation.ValueText
				}
			}
			if locationText != "Synthetic candidate 20\nThe twentieth supplied candidate matches the synthetic sign." {
				t.Fatalf("position-20 display name = %q", locationText)
			}
			t.Logf("RAW checked CardInput ProtoJSON:\n%s", inputJSON)
			t.Logf("RAW rendered model request:\n%s", prepared.Request.Body())
			t.Logf("RAW typed model response:\n%s", arguments)
			t.Logf("RAW parsed card location: %#v", card.Location)
		})
	}
}

func TestPreparedCardRejectsOverLimitInputBeforeRenderingOrRestore(t *testing.T) {
	preparation := fixtureCardPreparationFor("asset:model-candidate-limit")
	preparation.Evidence[0].Candidates = syntheticModelCandidates(cardinput.MaxModelCandidatesPerProjection + 1)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := renderPreparedCardRequest(preparation.Source, preparation.Artifacts, preparation.Evidence, preparation.CurrentStill, classifier); !errors.Is(err, cardinput.ErrUnsafeEvidence) {
		t.Fatalf("render error = %v, want unsafe evidence", err)
	}

	preparation.Evidence[0].Candidates = syntheticModelCandidates(cardinput.MaxModelCandidatesPerProjection)
	item, err := prepareCard(preparedCard{source: preparation.Source, artifacts: preparation.Artifacts, evidence: preparation.Evidence, classify: preparation.Classify, currentStill: preparation.CurrentStill, classifier: classifier}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := new(cardwire.CardInput)
	if err := proto.Unmarshal(item.GetCardInput(), input); err != nil {
		t.Fatal(err)
	}
	input.Places[0].Candidates = append(input.Places[0].Candidates, &cardwire.PlaceCandidate{CandidateId: "place_1_candidate_21"})
	item.CardInput, err = proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(item.CardInput)
	item.CardInputId = "card_input:" + hex.EncodeToString(digest[:])
	if _, err := restorePreparedCardRequestUnchecked(item); !errors.Is(err, cardinput.ErrUnsafeEvidence) {
		t.Fatalf("restore error = %v, want unsafe evidence", err)
	}

	input.Places[0].Candidates[19].CandidateId = input.Places[0].Candidates[18].CandidateId
	input.Places[0].Candidates = input.Places[0].Candidates[:cardinput.MaxModelCandidatesPerProjection]
	if _, _, err := candidateRegistry(input); err == nil {
		t.Fatal("candidate registry accepted a duplicate candidate id")
	}
}

func syntheticModelCandidates(count int) []place.EvidenceCandidate {
	candidates := make([]place.EvidenceCandidate, count)
	for index := range candidates {
		candidates[index] = place.EvidenceCandidate{
			ProviderIndex: index,
			ProviderID:    fmt.Sprintf("provider-%02d", index+1),
			Name:          fmt.Sprintf("Synthetic candidate %d", index+1),
			Categories:    []string{"synthetic"},
			DistanceM:     float64(index + 1),
			Source:        "synthetic-provider",
		}
	}
	return candidates
}
