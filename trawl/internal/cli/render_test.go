package cli

import (
	"testing"
)

func TestNormalizeSelfKeepsKnownIdentity(t *testing.T) {
	if got := normalizeSelf("ME (@avery_example)"); got != "me (@avery_example)" {
		t.Fatalf("normalizeSelf = %q", got)
	}
	if got := normalizeSelf(" me () "); got != "me" {
		t.Fatalf("normalizeSelf empty identity = %q", got)
	}
}
