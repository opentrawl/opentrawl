package archive

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/model"
)

func TestPhotoCardToolCallRejectsInvalidContracts(t *testing.T) {
	valid := map[string]any{
		"summary":      "A synthetic ferry at dusk.",
		"description":  "A synthetic ferry crosses a calm harbour under an orange sky.",
		"location":     map[string]any{"kind": "candidate", "candidate_id": "place_1_candidate_1", "inferred_name": "", "confidence": "high", "reason": "The terminal sign matches the supplied candidate."},
		"visible_text": "FERRY 12", "uncertainties": []string{"The distant shoreline is indistinct."},
	}
	prepared := preparedCardRequest{CandidateByID: map[string]preparedPlaceCandidate{
		"place_1_candidate_1": {ID: "place_1_candidate_1"},
	}}
	call := func(value map[string]any) model.ToolCall {
		arguments, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return model.ToolCall{Name: photoCardToolName, Arguments: arguments}
	}
	if card, err := parsePhotoCardToolCall([]model.ToolCall{call(valid)}, prepared); err != nil || card.Summary != valid["summary"] {
		t.Fatalf("valid tool card = %#v, %v", card, err)
	}
	cases := map[string][]model.ToolCall{
		"no call":        nil,
		"multiple calls": {call(valid), call(valid)},
		"wrong name":     {{Name: "other", Arguments: call(valid).Arguments}},
	}
	missing := cloneToolCard(t, valid)
	delete(missing, "description")
	cases["missing field"] = []model.ToolCall{call(missing)}
	unknown := cloneToolCard(t, valid)
	unknown["unexpected"] = true
	cases["unknown field"] = []model.ToolCall{call(unknown)}
	wrongType := cloneToolCard(t, valid)
	wrongType["uncertainties"] = "not an array"
	cases["wrong type"] = []model.ToolCall{call(wrongType)}
	blankUncertainty := cloneToolCard(t, valid)
	blankUncertainty["uncertainties"] = []string{" \t"}
	cases["blank uncertainty"] = []model.ToolCall{call(blankUncertainty)}
	badKind := cloneToolCard(t, valid)
	badKind["location"].(map[string]any)["kind"] = "certain"
	cases["unknown location kind"] = []model.ToolCall{call(badKind)}
	unknownCandidate := cloneToolCard(t, valid)
	unknownCandidate["location"].(map[string]any)["candidate_id"] = "place_9_candidate_9"
	cases["unknown candidate"] = []model.ToolCall{call(unknownCandidate)}
	blankReason := cloneToolCard(t, valid)
	blankReason["location"].(map[string]any)["reason"] = " \t"
	cases["blank location reason"] = []model.ToolCall{call(blankReason)}
	for name, calls := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parsePhotoCardToolCall(calls, prepared)
			if !errors.Is(err, errModelCardParse) && !errors.Is(err, errUnknownCardCandidate) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPhotoCardToolCallRequiresConsistentLocation(t *testing.T) {
	arguments := json.RawMessage(`{"summary":"Synthetic scene.","description":"A synthetic scene with visible pixels.","location":{"kind":"none","candidate_id":"place_1_candidate_1","inferred_name":"","confidence":"none","reason":"No useful place."},"visible_text":"","uncertainties":[]}`)
	_, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: arguments}}, preparedCardRequest{CandidateByID: map[string]preparedPlaceCandidate{}})
	if !errors.Is(err, errModelCardParse) {
		t.Fatalf("error = %v", err)
	}
}

func TestPhotoCardToolCallAcceptsEachLocationKind(t *testing.T) {
	prepared := preparedCardRequest{CandidateByID: map[string]preparedPlaceCandidate{
		"place_1_candidate_1": {ID: "place_1_candidate_1"},
	}}
	for _, location := range []modelLocation{
		{Kind: locationCandidate, CandidateID: "place_1_candidate_1", Confidence: "low", Reason: "A supplied candidate matches the synthetic sign."},
		{Kind: locationInferred, InferredName: "Synthetic Harbour", Confidence: "medium", Reason: "The synthetic harbour name is visible on the terminal."},
		{Kind: locationNone, Confidence: "none", Reason: "No location evidence is visible in this synthetic scene."},
	} {
		t.Run(location.Kind, func(t *testing.T) {
			arguments, err := json.Marshal(map[string]any{
				"summary": "Synthetic scene.", "description": "A synthetic scene with useful visual detail.",
				"location": location, "visible_text": "SYNTHETIC\nTEXT", "uncertainties": []string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			card, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: arguments}}, prepared)
			if err != nil {
				t.Fatal(err)
			}
			if card.Location != location || card.VisibleText != "SYNTHETIC\nTEXT" {
				t.Fatalf("card = %#v", card)
			}
		})
	}
}

func cloneToolCard(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
