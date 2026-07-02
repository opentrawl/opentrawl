package cli

import (
	"strings"
	"testing"
)

func TestOpenRendersGenericJSON(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
		openRef:  "imessage:msg/8842",
		open: `{
			"body":"Example body",
			"headers":{"from":"alice@example.com","to":["bob@example.com"]},
			"missing":null,
			"priority":2
		}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := "body: Example body\n" +
		"headers:\n" +
		"  from: alice@example.com\n" +
		"  to:\n" +
		"    - bob@example.com\n" +
		"missing: —\n" +
		"priority: 2\n"
	if stdout != want {
		t.Fatalf("open output:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestOpenJSONPassesCrawlerPayloadThrough(t *testing.T) {
	payload := `{"body":"Example body","ref":"imessage:msg/8842"}`
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
		openRef:  "imessage:msg/8842",
		open:     payload,
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
		metadata: `{"schema_version":1,"contract_version":1,"id":"fake","display_name":"Fake"}`,
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
			if !strings.Contains(stderr, "refs look like <source>:<path>") {
				t.Fatalf("stderr missing ref remedy:\n%s", stderr)
			}
		})
	}
}

func TestOpenReportsCrawlerFailure(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
		openRef:  "imessage:msg/8842",
		open:     `not-json`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Could not open ref") || !strings.Contains(stderr, "run: trawl doctor imessage") {
		t.Fatalf("stderr missing open failure:\n%s", stderr)
	}
}
