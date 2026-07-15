package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPreparedCardCarriesEveryCheckedCandidateInProviderOrder(t *testing.T) {
	preparation := fixtureCardPreparationFor("asset:model-candidates")
	preparation.Evidence[0].Address = &place.Address{Formatted: "Synthetic place", Source: "synthetic"}
	preparation.Evidence[0].Candidates = syntheticModelCandidates(25)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := renderPreparedCardRequest(preparation.Source, preparation.Artifacts, preparation.Evidence, preparation.CurrentStill, classifier)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(prepared.Input.Input.GetPlaces()[0].GetCandidates()); got != 25 {
		t.Fatalf("CardInput candidates = %d, want 25", got)
	}
	inputJSON, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(prepared.Input.Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, boundary := range [][]byte{inputJSON, prepared.Request.Body()} {
		position := -1
		for index := 1; index <= 25; index++ {
			providerID := fmt.Sprintf("provider-%02d", index)
			next := bytes.Index(boundary[position+1:], []byte(providerID))
			if next < 0 {
				t.Fatalf("boundary omitted %q", providerID)
			}
			position += next + 1
		}
	}
	if got := prepared.Input.Input.GetPlaces()[0].GetCandidates()[24].GetProviderResult().GetFields()["semantic_marker"].GetStringValue(); got != "signal-25" {
		t.Fatalf("candidate 25 provider result = %q", got)
	}
	if _, found := prepared.Input.Input.GetPlaces()[0].GetCandidates()[0].GetProviderResult().GetFields()["api_key"]; found {
		t.Fatal("provider secret reached CardInput")
	}
	if _, found := prepared.Input.Input.GetPlaces()[0].GetCandidates()[0].GetProviderResult().GetFields()["transport"]; found {
		t.Fatal("provider transport reached CardInput")
	}
	if got := len(prepared.CandidatesInSeq); got != 25 {
		t.Fatalf("candidate registry = %d, want 25", got)
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
		t.Fatal("restored approved request changed checked place evidence")
	}
	arguments, err := json.Marshal(map[string]any{
		"summary": "Synthetic scene.", "description": "A synthetic scene with visible evidence.",
		"location":     map[string]string{"kind": "candidate", "candidate_id": "place_1_candidate_25", "inferred_name": "", "confidence": "high", "reason": "The twenty-fifth supplied candidate matches the synthetic sign."},
		"visible_text": "SYNTHETIC", "uncertainties": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	card, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: arguments}}, prepared)
	if err != nil || card.Location.CandidateID != "place_1_candidate_25" {
		t.Fatalf("position-25 card = %#v, error = %v", card, err)
	}
	t.Logf("RAW checked CardInput ProtoJSON:\n%s", inputJSON)
	t.Logf("RAW rendered model request:\n%s", prepared.Request.Body())
	t.Logf("RAW typed model response:\n%s", arguments)
}

func TestPreparedCardRejectsUnknownDuplicateAndOversizedEvidence(t *testing.T) {
	preparation := fixtureCardPreparationFor("asset:model-candidate-boundary")
	preparation.Evidence[0].Candidates = syntheticModelCandidates(25)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := renderPreparedCardRequest(preparation.Source, preparation.Artifacts, preparation.Evidence, preparation.CurrentStill, classifier)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := json.Marshal(map[string]any{
		"summary": "Synthetic scene.", "description": "Synthetic description.",
		"location":     map[string]string{"kind": "candidate", "candidate_id": "place_1_candidate_26", "inferred_name": "", "confidence": "high", "reason": "Synthetic."},
		"visible_text": "", "uncertainties": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: unknown}}, prepared); !errors.Is(err, errUnknownCardCandidate) {
		t.Fatalf("unknown candidate error = %v", err)
	}

	input := proto.Clone(prepared.Input.Input).(*cardwire.CardInput)
	input.Places[0].Candidates[24].CandidateId = input.Places[0].Candidates[23].CandidateId
	if _, _, err := candidateRegistry(input); err == nil {
		t.Fatal("candidate registry accepted a duplicate candidate id")
	}

	item, err := prepareCard(preparedCard{source: preparation.Source, artifacts: preparation.Artifacts, evidence: preparation.Evidence, classify: preparation.Classify, currentStill: preparation.CurrentStill, classifier: classifier}, 1)
	if err != nil {
		t.Fatal(err)
	}
	stored := new(cardwire.CardInput)
	if err := proto.Unmarshal(item.GetCardInput(), stored); err != nil {
		t.Fatal(err)
	}
	stored.GetPlaces()[0].GetCandidates()[0].ProviderResult.Fields["description"] = structpb.NewStringValue(strings.Repeat("x", cardinput.MaxRenderedModelInputBytes))
	item.CardInput, err = proto.MarshalOptions{Deterministic: true}.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(item.CardInput)
	item.CardInputId = "card_input:" + hex.EncodeToString(digest[:])
	if _, err := restorePreparedCardRequestUnchecked(item); !errors.Is(err, cardinput.ErrModelInputTooLarge) {
		t.Fatalf("oversized restore error = %v", err)
	}

	preparation.Evidence[0].Candidates[0].ProviderResult = []byte(`{"semantic_marker":"kept","description":"` + strings.Repeat("x", cardinput.MaxRenderedModelInputBytes) + `"}`)
	if _, err := renderPreparedCardRequest(preparation.Source, preparation.Artifacts, preparation.Evidence, preparation.CurrentStill, classifier); !errors.Is(err, cardinput.ErrModelInputTooLarge) {
		t.Fatalf("oversized render error = %v", err)
	}
}

func syntheticModelCandidates(count int) []place.EvidenceCandidate {
	candidates := make([]place.EvidenceCandidate, count)
	for index := range candidates {
		candidates[index] = place.EvidenceCandidate{
			ProviderIndex:  index,
			ProviderID:     fmt.Sprintf("provider-%02d", index+1),
			Name:           fmt.Sprintf("Synthetic candidate %d", index+1),
			Categories:     []string{"synthetic"},
			DistanceM:      float64(index + 1),
			Source:         "synthetic-provider",
			ProviderResult: []byte(fmt.Sprintf(`{"semantic_marker":"signal-%d","api_key":"synthetic-secret","transport":{"headers":"synthetic"}}`, index+1)),
		}
	}
	return candidates
}
