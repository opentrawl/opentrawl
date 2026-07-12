package cli

import "testing"

func TestAppWireRecognisesOnlyThePrivateHelperCommand(t *testing.T) {
	if !isAppWireCommand([]string{"__app", "status"}) || isAppWireCommand([]string{"status"}) {
		t.Fatal("helper command recognition changed")
	}
}
