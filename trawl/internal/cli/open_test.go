package cli

import (
	"strings"
	"testing"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestOpenPassesHumanCrawlerOutputThrough(t *testing.T) {
	human := "Crawler human open\nSubject: Example item\n\nLine one.\nref: imessage:msg/8842"
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:      "imsgcrawl",
		metadata:  `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		openRef:   "imessage:msg/8842",
		open:      `not-json`,
		openHuman: human,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != strings.Replace(human, "\n", "\n\n", 1)+"\n" {
		t.Fatalf("stdout = %q, want canonical presentation", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestOpenJSONPassesCrawlerPayloadThrough(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:      "imsgcrawl",
		metadata:  `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		openRef:   "imessage:msg/8842",
		open:      `{"body":"Example body","ref":"imessage:msg/8842"}`,
		openHuman: "human output",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	var response openv1.OpenResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("open JSON = %s err=%v", stdout, err)
	}
	if response.GetRequestedRef() != "imessage:msg/8842" || response.GetRecord().GetOpenRef() != "imessage:msg/8842" {
		t.Fatalf("open response = %#v", &response)
	}
}

func TestOpenPassesFullRefToCrawler(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"fake","display_name":"Fake"}`,
		openRef:  "fake:msg/1",
		open:     `{"body":"Example body","ref":"fake:msg/1"}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "open", "fake:msg/1")
	if code != 0 {
		t.Fatalf("open --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	var response openv1.OpenResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("open JSON = %s err=%v", stdout, err)
	}
	if response.GetRequestedRef() != "fake:msg/1" || response.GetRecord().GetOpenRef() != "fake:msg/1" {
		t.Fatalf("open response = %#v", &response)
	}
}

func TestOpenRoutesLegacyFullRefPrefixes(t *testing.T) {
	tests := []struct {
		name    string
		crawler fakeCrawler
		fullRef string
		payload string
	}{
		{
			name: "whatsapp",
			crawler: fakeCrawler{
				name:     "wacrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"whatsapp","display_name":"WhatsApp"}`,
				openRef:  "wacrawl:msg/group-image",
				open:     `{"ref":"wacrawl:msg/group-image"}`,
			},
			fullRef: "wacrawl:msg/group-image",
			payload: `{"ref":"wacrawl:msg/group-image"}`,
		},
		{
			name: "calendar",
			crawler: fakeCrawler{
				name:     "calcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"calendar","display_name":"Calendar"}`,
				openRef:  "calcrawl:event/11111111-1111-1111-1111-111111111111",
				open:     `{"ref":"calcrawl:event/11111111-1111-1111-1111-111111111111"}`,
			},
			fullRef: "calcrawl:event/11111111-1111-1111-1111-111111111111",
			payload: `{"ref":"calcrawl:event/11111111-1111-1111-1111-111111111111"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawler)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", syntheticHome(t))

			stdout, stderr, code := runCLI(t, "--json", "open", tt.fullRef)
			if code != 1 {
				t.Fatalf("open --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
			}
			var response openv1.OpenResponse
			if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
				t.Fatalf("open JSON = %s err=%v", stdout, err)
			}
			if response.GetRequestedRef() != tt.fullRef || response.GetFailure().GetCode().String() != "FAILURE_CODE_INVALID_INPUT" {
				t.Fatalf("legacy ref response = %#v", &response)
			}
			if stderr != "" {
				t.Fatalf("stderr = %s", stderr)
			}
		})
	}
}

func TestOpenShortRefResolvesExactlyOneMatch(t *testing.T) {
	human := "Resolved human item\nref: imessage:msg/1"
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		shortRefAlias: "t7k3f",
		openRef:       "imessage:msg/1",
		open:          `{"ref":"imessage:msg/1"}`,
		openHuman:     human,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != strings.Replace(human, "\n", "\n\n", 1)+"\n" {
		t.Fatalf("stdout = %q, want canonical presentation", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}

	requestedRef := "  t7k3f  "
	stdout, stderr, code = runCLI(t, "--json", "open", requestedRef)
	if code != 0 {
		t.Fatalf("JSON code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var response openv1.OpenResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("open JSON = %s err=%v", stdout, err)
	}
	if response.GetRequestedRef() != requestedRef || response.GetRecord().GetOpenRef() != "imessage:msg/1" {
		t.Fatalf("open response = %#v", &response)
	}
}

// TestOpenShortRefSurvivesEarlierErroringSource pins the fan-out contract:
// a source failing for a reason unrelated to short
// refs is skipped, never aborts resolution. imsgcrawl sits before
// telecrawl in registration order, so the erroring source is hit first.
func TestOpenShortRefSurvivesEarlierErroringSource(t *testing.T) {
	human := "Resolved human item\nref: telegram:msg/2"
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			shortRefAlias: "t7k3f",
			open:          `crawler crashed`,
			openExit:      1,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			shortRefAlias: "t7k3f",
			openRef:       "telegram:msg/2",
			open:          `{"ref":"telegram:msg/2"}`,
			openHuman:     human,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != strings.Replace(human, "\n", "\n\n", 1)+"\n" {
		t.Fatalf("stdout = %q, want the healthy source's canonical presentation", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

// TestOpenShortRefReportsEverySourceFailing pins the honest-failure
// case: when no source could answer at all, the error
// names each failed source instead of claiming the ref is unknown or
// blaming only the first source. imsgcrawl reproduces the live wacrawl
// shape — a contract error envelope emitted at exit 0.
func TestOpenShortRefReportsEverySourceFailing(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			shortRefAlias: "t7k3f",
			open:          `{"error":{"code":"command_failed","message":"record short ref fingerprint failed","remedy":""}}`,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			shortRefAlias: "t7k3f",
			open:          `crawler crashed`,
			openExit:      1,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	if strings.Contains(stderr, "was not found") {
		t.Fatalf("stderr claims unknown when every source errored:\n%s", stderr)
	}
	for _, want := range []string{`Could not resolve short ref "t7k3f"`, "Messages", "Telegram", "command_failed"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if !strings.Contains(stderr, "run trawl doctor") {
		t.Fatalf("stderr missing a runnable doctor remedy:\n%s", stderr)
	}
}

// TestOpenShortRefUnknownDespiteOneErroringSource pins that one
// erroring source does not turn a clean miss from the healthy sources
// into a resolution failure: the contract outcome stays unknown. The
// healthy source answers unknown at exit 0, which live crawlers do.
func TestOpenShortRefUnknownDespiteOneErroringSource(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			shortRefAlias: "t7k3f",
			open:          `crawler crashed`,
			openExit:      1,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			shortRefAlias: "t7k3f",
			open:          `{"error":{"code":"unknown_short_ref","message":"short ref was not found","remedy":"rerun search or use the full ref"}}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `Short ref "t7k3f" was not found.`) {
		t.Fatalf("stderr missing unknown short ref:\n%s", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestOpenShortRefReportsUnknown(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		shortRefAlias: "t7k3f",
		open:          `{"error":{"code":"unknown_short_ref","message":"short ref was not found","remedy":"rerun search or use the full ref"}}`,
		openExit:      1,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, `Could not resolve short ref "t7k3f". Every source failed:`) {
		t.Fatalf("stderr missing unknown short ref:\n%s", stderr)
	}
}

func TestOpenShortRefReportsAmbiguousJSON(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:          "imsgcrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			shortRefAlias: "t7k3f",
			open:          `{"ref":"imessage:msg/1"}`,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			shortRefAlias: "t7k3f",
			open:          `{"ref":"telegram:msg/2"}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var response openv1.OpenResponse
	if err := (protojson.UnmarshalOptions{}).Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatalf("open JSON = %s err=%v", stdout, err)
	}
	if response.GetOutcome().String() != "OPERATION_OUTCOME_FAILED" || response.GetRequestedRef() != "t7k3f" || response.GetFailure().GetCode().String() != "FAILURE_CODE_INVALID_INPUT" {
		t.Fatalf("ambiguous response = %#v", &response)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestOpenShortRefRejectsLegacyLookupEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		shortRefAlias: "t7k3f",
		open:          `{"alias":"t7k3f","refs":["imessage:msg/1"]}`,
		openExit:      1,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, `Could not resolve short ref "t7k3f".`) {
		t.Fatalf("stderr missing resolution failure:\n%s", stderr)
	}
}

func TestOpenRejectsInvalidRefs(t *testing.T) {
	tests := []string{"msg/8842", ":msg/8842", "imessage:"}
	for _, ref := range tests {
		t.Run(ref, func(t *testing.T) {
			binDir := writeFakeCrawlers(t)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", syntheticHome(t))

			stdout, stderr, code := runCLI(t, "open", ref)
			if code != 1 {
				t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "refs look like <source>:<path>") && !strings.Contains(stderr, "short refs use") && !strings.Contains(stderr, "was not found") {
				t.Fatalf("stderr missing ref remedy:\n%s", stderr)
			}
		})
	}
}

func TestOpenPassesCrawlerFailureThrough(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		openRef:       "imessage:msg/8842",
		openHuman:     "partial crawler output",
		openHumanExit: 7,
		openStderr:    "crawler open failed",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no partial source bytes", stdout)
	}
	if !strings.Contains(stderr, "open failed") || !strings.Contains(stderr, "Remedy: trawl doctor imessage") {
		t.Fatalf("stderr = %q, want typed open failure", stderr)
	}
}
