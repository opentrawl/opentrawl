package cli

import (
	"strings"
	"testing"
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
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != human+"\n" {
		t.Fatalf("stdout = %q, want crawler human output", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestOpenJSONPassesCrawlerPayloadThrough(t *testing.T) {
	payload := `{"body":"Example body","ref":"imessage:msg/8842"}`
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:      "imsgcrawl",
		metadata:  `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		openRef:   "imessage:msg/8842",
		open:      payload,
		openHuman: "human output",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != payload+"\n" {
		t.Fatalf("stdout = %q, want raw payload", stdout)
	}
}

func TestOpenPassesFullRefToCrawler(t *testing.T) {
	payload := `{"body":"Example body","ref":"fake:msg/1"}`
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"fake","display_name":"Fake"}`,
		openRef:  "fake:msg/1",
		open:     payload,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "open", "fake:msg/1")
	if code != 0 {
		t.Fatalf("open --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if stdout != payload+"\n" {
		t.Fatalf("stdout = %q, want raw payload", stdout)
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
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != human+"\n" {
		t.Fatalf("stdout = %q, want crawler human output", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
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
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, `Short ref "t7k3f" was not found.`) {
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
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "open", "t7k3f")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `{"error":{"code":"ambiguous_short_ref","message":"Short ref \"t7k3f\" matched more than one item.","remedy":"rerun the search or use the full ref"}}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
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
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

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
			t.Setenv("HOME", t.TempDir())

			stdout, stderr, code := runCLI(t, "open", ref)
			if code != 1 {
				t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "refs look like <source>:<path>") && !strings.Contains(stderr, "short refs use") {
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
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 7 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "partial crawler output\n" {
		t.Fatalf("stdout = %q, want crawler stdout", stdout)
	}
	if stderr != "crawler open failed\n" {
		t.Fatalf("stderr = %q, want crawler stderr", stderr)
	}
}
