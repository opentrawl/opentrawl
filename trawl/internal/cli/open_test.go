package cli

import (
	"strings"
	"testing"
)

func TestOpenRendersTranscript(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		openRef:  "imessage:msg/8842",
		open: `{
			"ref":"imessage:msg/8842",
			"chat":{"name":"Family chat","participants":["alice@example.com"]},
			"message":{"ref":"imessage:msg/8842","time":"2026-05-15T00:01:00Z","who":"Me","where":"Family chat","text":"Target\nmessage"},
			"context":[
				{"ref":"imessage:msg/8841","time":"2026-05-14T23:58:00Z","who":"Alice","where":"Family chat","text":"Yesterday"},
				{"ref":"imessage:msg/8842","time":"2026-05-15T00:01:00Z","who":"Me","where":"Family chat","text":"Target\nmessage","target":true},
				{"ref":"imessage:msg/8843","time":"2026-05-15T00:02:00Z","who":"Bob","where":"Family chat","has_attachments":true}
			]
		}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "imessage:msg/8842")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := "Family chat\n\n" +
		"2026-05-14 23:58  Alice: Yesterday\n" +
		"▶ 2026-05-15 00:01  Me: Target message\n" +
		"2026-05-15 00:02  Bob: [attachment]\n\n" +
		"ref: imessage:msg/8842\n"
	if stdout != want {
		t.Fatalf("open output:\n%s\nwant:\n%s", stdout, want)
	}
	if strings.Contains(stdout, "msg/8841") || strings.Contains(stdout, "msg/8843") || strings.Contains(stdout, "alice@example.com") {
		t.Fatalf("transcript leaked non-ref ids or participants:\n%s", stdout)
	}
}

func TestOpenRendersMailHeadersAsProse(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "gogcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"gmail","display_name":"Gmail"}`,
		openRef:  "gmail:msg/m3",
		open: `{
			"ref":"gmail:msg/m3",
			"id":"m3",
			"thread_id":"thread-1",
			"time":"2026-05-14T09:12:00Z",
			"headers":{
				"from_name":"Alice Example",
				"from_address":"alice@example.com",
				"to_address":"bob@example.com",
				"cc_address":"carol@example.com",
				"subject":"Project update"
			},
			"body":"Line one.\n\nLine two.",
			"attachments":[{"filename":"plan.pdf","mime_type":"application/pdf","size_bytes":42}]
		}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "gmail:msg/m3")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := "Project update\n" +
		"From Alice Example <alice@example.com> to bob@example.com.\n" +
		"Cc carol@example.com.\n" +
		"Sent 2026-05-14 09:12.\n" +
		"1 attachment.\n\n" +
		"Line one.\n\nLine two.\n\n" +
		"ref: gmail:msg/m3\n"
	if stdout != want {
		t.Fatalf("open output:\n%s\nwant:\n%s", stdout, want)
	}
	if strings.Contains(stdout, "thread_id") || strings.Contains(stdout, "thread-1") || strings.Contains(stdout, "id:") {
		t.Fatalf("mail output leaked raw ids:\n%s", stdout)
	}
}

func TestOpenRendersCalendarAttendeesAsProse(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "calcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"calendar","display_name":"Calendar"}`,
		openRef:  "calendar:event/11111111-1111-1111-1111-111111111111",
		open: `{
			"ref":"calendar:event/11111111-1111-1111-1111-111111111111",
			"uuid":"11111111-1111-1111-1111-111111111111",
			"title":"Planning meeting",
			"start":"2026-03-04T10:00:00+01:00",
			"end":"2026-03-04T10:30:00+01:00",
			"calendar":"Work",
			"account":"iCloud",
			"location":{"title":"Room 1","address":"1 Example Street"},
			"organizer":{"display_name":"Dana Example","email":"dana@example.com"},
			"attendees":[
				{"display_name":"Alice Example","email":"alice@example.com","rsvp_status":"accepted"},
				{"email":"bob@example.com","rsvp_status":"tentative"}
			],
			"description":"Discuss planning."
		}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "calendar:event/11111111-1111-1111-1111-111111111111")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := "Planning meeting\n" +
		"2026-03-04 10:00 to 2026-03-04 10:30 on Work.\n" +
		"Location: Room 1, 1 Example Street.\n" +
		"Organiser: Dana Example.\n" +
		"Attendees: Alice Example (accepted), bob@example.com (tentative).\n\n" +
		"Discuss planning.\n\n" +
		"ref: calendar:event/11111111-1111-1111-1111-111111111111\n"
	if stdout != want {
		t.Fatalf("open output:\n%s\nwant:\n%s", stdout, want)
	}
	if strings.Contains(stdout, "uuid") {
		t.Fatalf("calendar output leaked raw ids:\n%s", stdout)
	}
}

func TestOpenFallsBackToGenericJSONForUnknownShapes(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"fake","display_name":"Fake"}`,
		openRef:  "fake:item/1",
		open: `{
			"body":"Example body",
			"missing":null,
			"priority":2,
			"tags":["one"]
		}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "open", "fake:item/1")
	if code != 0 {
		t.Fatalf("open code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := "body: Example body\n" +
		"missing: —\n" +
		"priority: 2\n" +
		"tags:\n" +
		"  - one\n"
	if stdout != want {
		t.Fatalf("open output:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestOpenJSONPassesCrawlerPayloadThrough(t *testing.T) {
	payload := `{"body":"Example body","ref":"imessage:msg/8842"}`
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
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
	payload := `{"body":"Example body","ref":"imessage:msg/1"}`
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		shortRefAlias: "t7k3f",
		shortRefs:     `{"alias":"t7k3f","refs":["imessage:msg/1"]}`,
		openRef:       "imessage:msg/1",
		open:          payload,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "open", "t7k3f")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != payload+"\n" {
		t.Fatalf("stdout = %q, want raw payload", stdout)
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
		shortRefs:     `{"alias":"t7k3f","refs":[]}`,
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
			shortRefs:     `{"alias":"t7k3f","refs":["imessage:msg/1"]}`,
		},
		fakeCrawler{
			name:          "telecrawl",
			metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			shortRefAlias: "t7k3f",
			shortRefs:     `{"alias":"t7k3f","refs":["telegram:msg/2"]}`,
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

func TestOpenReportsCrawlerFailure(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
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
