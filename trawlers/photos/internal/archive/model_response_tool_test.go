package archive

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/model"
)

func TestPhotoCardToolCallRejectsInvalidContracts(t *testing.T) {
	valid := map[string]any{
		"summary":            "A synthetic ferry at dusk.",
		"description":        "A synthetic ferry crosses a calm harbour under an orange sky.",
		"venue_plausibility": map[string]any{"candidate_id": "place_1_candidate_1", "verdict": "plausible", "reason": "The terminal is near the synthetic coordinate."},
		"ocr_text":           "FERRY 12", "uncertainties": []string{"The distant shoreline is indistinct."},
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
	badVerdict := cloneToolCard(t, valid)
	badVerdict["venue_plausibility"].(map[string]any)["verdict"] = "certain"
	cases["unknown verdict"] = []model.ToolCall{call(badVerdict)}
	unknownCandidate := cloneToolCard(t, valid)
	unknownCandidate["venue_plausibility"].(map[string]any)["candidate_id"] = "place_9_candidate_9"
	cases["unknown candidate"] = []model.ToolCall{call(unknownCandidate)}
	blankReason := cloneToolCard(t, valid)
	blankReason["venue_plausibility"].(map[string]any)["reason"] = " \t"
	cases["blank venue reason"] = []model.ToolCall{call(blankReason)}
	for name, calls := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parsePhotoCardToolCall(calls, prepared)
			if !errors.Is(err, errModelCardParse) && !errors.Is(err, errUnknownCardCandidate) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPhotoCardToolCallPairsNoneCandidateAndVerdict(t *testing.T) {
	arguments := json.RawMessage(`{"summary":"Synthetic scene.","description":"A synthetic scene with visible pixels.","venue_plausibility":{"candidate_id":"none","verdict":"plausible","reason":"No venue."},"ocr_text":"","uncertainties":[]}`)
	_, err := parsePhotoCardToolCall([]model.ToolCall{{Name: photoCardToolName, Arguments: arguments}}, preparedCardRequest{CandidateByID: map[string]preparedPlaceCandidate{}})
	if !errors.Is(err, errModelCardParse) {
		t.Fatalf("error = %v", err)
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
