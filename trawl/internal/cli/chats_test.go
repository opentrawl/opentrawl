package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
)

func chatCount(value int64) *int64 { return &value }

func TestFederatedChatsListsAndFiltersMessagingSources(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"imessage","display_name":"Messages"}`,
			chats: []trawlkit.Chat{{
				ID: "11", Ref: "imessage:chat/11", DisplayID: "11", Title: "Anna Example", ParticipantNames: []string{"Anna Example"}, LastActivity: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
			}},
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"telegram","display_name":"Telegram"}`,
			chats: []trawlkit.Chat{
				{ID: "21", Ref: "telegram:chat/21", DisplayID: "21", Title: "Weekend Plans", Group: true, Participants: chatCount(3), ParticipantNames: []string{"Bo Example", "Anna Example", "Cy Example"}, Unread: chatCount(4), LastActivity: time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)},
				{ID: "22", Ref: "telegram:chat/22", DisplayID: "22", Title: "Unrelated", ParticipantNames: []string{"Dana Example"}, LastActivity: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)},
			},
		},
		fakeCrawler{
			name:     "notescrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","search"],"id":"notes","display_name":"Notes"}`,
		},
	)
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats", "--with", "anna")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"Telegram", "Messages", "Weekend Plans", "Anna Example", "newest first"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("text output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Unrelated") || strings.Contains(stdout, "Notes") {
		t.Fatalf("text output ignored --with or included a non-messaging source:\n%s", stdout)
	}
	if strings.Index(stdout, "Weekend Plans") > strings.Index(stdout, "Anna Example") {
		t.Fatalf("chats are not newest first:\n%s", stdout)
	}

	stdout, stderr, code = runCLI(t, "chats", "--unread")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Unread chats") || !strings.Contains(stdout, "Weekend Plans") || strings.Contains(stdout, "\nMessages") {
		t.Fatalf("unread output code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestFederatedChatsKeepsPartialSuccessAndReportsFailure(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"imessage","display_name":"Messages"}`,
			chats:    []trawlkit.Chat{{ID: "11", Ref: "imessage:chat/11", DisplayID: "11", Title: "Synthetic chat", LastActivity: time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)}},
		},
		fakeCrawler{
			name:       "telecrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"telegram","display_name":"Telegram"}`,
			chatsError: "synthetic archive failure",
		},
	)
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats")
	if code != 3 {
		t.Fatalf("code=%d, want partial failure; stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Synthetic chat") || !strings.Contains(stderr, "Telegram chats failed") || !strings.Contains(stderr, "retry with -v") {
		t.Fatalf("partial output lost success or failure context:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if strings.Contains(stderr, "doctor") {
		t.Fatalf("failure offered removed diagnostics command:\n%s", stderr)
	}

	stdout, stderr, code = runCLI(t, "--json", "chats")
	if code != 3 || stderr != "" {
		t.Fatalf("JSON code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope federatedChatsOutput
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(envelope.Chats) != 1 || len(envelope.FailedSources) != 1 || envelope.FailedSources[0].Source != "telegram" {
		t.Fatalf("JSON did not preserve partial outcome: %#v", envelope)
	}
}

func TestFederatedChatsTreatsMissingArchivesAsNormalAbsence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	stdout, stderr, code := runCLI(t, "chats")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "No messaging archives found. Run trawl sync to create them.\n" {
		t.Fatalf("unexpected human output: %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "--json", "chats")
	if code != 0 || stderr != "" {
		t.Fatalf("JSON code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope federatedChatsOutput
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if strings.Join(envelope.UnavailableSources, ",") != "imessage,telegram,whatsapp" {
		t.Fatalf("unavailable sources = %#v", envelope.UnavailableSources)
	}
}

func TestFederatedChatsDoesNotCallAnAvailableEmptyArchiveMissing(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"imessage","display_name":"Messages"}`,
		},
		fakeCrawler{
			name:       "telecrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"telegram","display_name":"Telegram"}`,
			chatsError: trawlkit.NewMissingArchiveError("synthetic").Error(),
		},
	)
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "No chats.\n" {
		t.Fatalf("available empty archive was misreported: %q", stdout)
	}
}

func TestFederatedChatsUnreadNeedsRealReadState(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:       "telecrawl",
		metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","chats"],"id":"telegram","display_name":"Telegram"}`,
		chatsError: trawlkit.ErrChatsNoReadState.Error(),
	})
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats", "--unread")
	if code != 1 || !strings.Contains(stdout, "No chats could be listed.") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "Telegram chats failed") || !strings.Contains(stderr, "Remedy: run trawl sync telegram") {
		t.Fatalf("missing source-specific read-state remedy:\n%s", stderr)
	}
}
