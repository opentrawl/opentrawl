//go:build darwin

package photos

import (
	"context"
	"errors"
	"testing"
)

func TestPhotoKitSnapshotCompletenessFollowsAuthorisation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status string
		state  SnapshotCompletenessState
	}{
		{status: "authorized", state: SnapshotComplete},
		{status: "limited", state: SnapshotLimited},
		{status: "denied", state: SnapshotPartial},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			t.Parallel()
			completeness := photoKitSnapshotCompleteness(test.status)
			if completeness.State != test.state || completeness.Evidence["authorization_status"] != test.status || completeness.Evidence["asset_enumeration"] != "completed" {
				t.Fatalf("completeness = %#v", completeness)
			}
			if err := completeness.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestPhotoKitSnapshotCancellationCannotProduceCompleteSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot, err := decodePhotoKitSnapshot(ctx, []byte(`{"authorization_status":"authorized"}`))
	t.Logf("boundary=photokit_native_snapshot input=%s", `{"authorization_status":"authorized"}`)
	t.Logf("boundary=photokit_snapshot output={"+`"completeness":%q,"error":%q`+"}", snapshot.Completeness.State, errorText(err))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("decodePhotoKitSnapshot error = %v, want context cancelled", err)
	}
	if snapshot.Completeness.Complete() {
		t.Fatalf("cancelled PhotoKit snapshot was complete: %#v", snapshot)
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
