package photos

import "testing"

func TestSnapshotCompletenessRequiresStateAndProviderEvidence(t *testing.T) {
	t.Parallel()
	valid := SnapshotCompleteness{
		State:    SnapshotComplete,
		Evidence: map[string]string{"asset_query": "completed"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid completeness: %v", err)
	}
	for name, completeness := range map[string]SnapshotCompleteness{
		"missing state":    {Evidence: map[string]string{"asset_query": "completed"}},
		"unknown state":    {State: "unknown", Evidence: map[string]string{"asset_query": "completed"}},
		"missing evidence": {State: SnapshotComplete},
		"empty evidence":   {State: SnapshotComplete, Evidence: map[string]string{"asset_query": ""}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := completeness.Validate(); err == nil {
				t.Fatalf("Validate(%#v) = nil", completeness)
			}
		})
	}
}
