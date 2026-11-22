package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func TestComplainFlagAndComplaintsListing(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv(trawlkit.StateRootEnvironment, stateRoot)
	t.Setenv("T3CODE_THREAD_ID", "test-thread-id")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := ExecuteWithTrawlInvocationDisplay(
		[]string{
			"complain",
			"Missing attachment filename search",
			"--about", "trawl search invoice --trawler gmail",
			"--trying", "Find recent invoice PDFs",
			"--workaround", "Opened matching vendor emails one by one",
		},
		&stdout,
		&stderr,
		"trawl",
	)
	if exitCode != 0 {
		t.Fatalf("trawl complain exited %d: stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Saved complaint to ") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = ExecuteWithTrawlInvocationDisplay(
		[]string{
			"search",
			"--unknown-complaint-test-flag",
			"--complaint", "Expected an attachment filter flag",
		},
		&stdout,
		&stderr,
		"trawl",
	)
	if exitCode == 0 {
		t.Fatalf("expected unknown flag to fail")
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = ExecuteWithTrawlInvocationDisplay([]string{"complaints"}, &stdout, &stderr, "trawl")
	if exitCode != 0 {
		t.Fatalf("trawl complaints exited %d: stderr=%q", exitCode, stderr.String())
	}
	rendered := stdout.String()
	for _, want := range []string{
		"Complaints: showing 2 of 2.",
		"Expected an attachment filter flag",
		"Command: trawl search --unknown-complaint-test-flag",
		"Missing attachment filename search",
		"Trying: Find recent invoice PDFs",
		"Workaround: Opened matching vendor emails one by one",
		"Thread: test-thread-id",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("trawl complaints output missing %q:\n%s", want, rendered)
		}
	}
}
