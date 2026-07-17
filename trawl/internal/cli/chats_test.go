package cli

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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

func TestFederatedChatsUsesSharedParticipantMatchingAcrossSources(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"imessage","display_name":"Messages"}`,
			chats: []trawlkit.Chat{{
				ID: "11", Ref: "imessage:chat/11", DisplayID: "11", Title: "Project Group", Group: true,
				ParticipantNames: []string{"Alex-Lee"}, LastActivity: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
			}},
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"telegram","display_name":"Telegram"}`,
			chats: []trawlkit.Chat{{
				ID: "21", Ref: "telegram:chat/21", DisplayID: "21", Title: "Alex-Lee",
				LastActivity: time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC),
			}},
		},
		fakeCrawler{
			name:     "wacrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"whatsapp","display_name":"WhatsApp"}`,
			chats: []trawlkit.Chat{
				{
					ID: "31", Ref: "whatsapp:chat/31", DisplayID: "privacy id", Title: "unknown participant (privacy id)",
					LastActivity: time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC),
				},
				{
					ID: "32", Ref: "whatsapp:chat/32", DisplayID: "privacy id", Title: "Alex-Lee",
					LastActivity: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC),
				},
			},
		},
	)
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats", "--with", "alex lee")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, source := range []string{"Messages", "Telegram", "WhatsApp"} {
		if !strings.Contains(stdout, source) {
			t.Fatalf("shared punctuation/spacing match omitted %s:\n%s", source, stdout)
		}
	}
	if strings.Contains(stdout, "unknown participant") || strings.Contains(stdout, "@lid") {
		t.Fatalf("participant matching exposed or matched a private handle:\n%s", stdout)
	}
}

func TestFederatedChatsUsesPeopleAliasesAcrossMessagingSources(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:       "contacts",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","who"],"id":"contacts","display_name":"Contacts"}`,
			whoQuery:   "Anna Example",
			who:        `{"query":"Anna Example","candidates":[{"who":"Anna Example","identifiers":["+15550100"],"match_quality":"exact","sources":["contacts"],"messages":0}]}`,
			whoAliases: map[string][]string{"Anna Example": {"Anya Telegram"}},
		},
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"imessage","display_name":"Messages"}`,
			chats: []trawlkit.Chat{{
				ID: "11", Ref: "imessage:chat/11", DisplayID: "11", Title: "Anna Example",
			}},
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","chats"],"id":"telegram","display_name":"Telegram"}`,
			chats: []trawlkit.Chat{{
				ID: "21", Ref: "telegram:chat/21", DisplayID: "21", Title: "Anya Telegram",
			}},
		},
	)
	t.Setenv("PATH", binDir)

	stdout, stderr, code := runCLI(t, "chats", "--with", "Anna Example")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"Messages", "Anna Example", "Telegram", "Anya Telegram"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("People alias result missing %q:\n%s", want, stdout)
		}
	}
}

func TestUniqueBestChatPersonRefusesAmbiguityAndCloseSpelling(t *testing.T) {
	if _, ok := uniqueBestChatPerson([]whomatch.Candidate{
		{Who: "Anna Example"},
		{Who: "Anna Example", Identifiers: []string{"anna-2@example.com"}},
	}, "Anna Example"); ok {
		t.Fatal("two equally exact people must remain ambiguous")
	}

	got, ok := uniqueBestChatPerson([]whomatch.Candidate{
		{Who: "Anna Example"},
		{Who: "Anna Exampleton"},
	}, "Anna Example")
	if !ok || got.Who != "Anna Example" {
		t.Fatalf("unique exact match should beat a prefix match: %#v, %v", got, ok)
	}

	if _, ok := uniqueBestChatPerson([]whomatch.Candidate{{Who: "Anna Example"}}, "Anan Example"); ok {
		t.Fatal("close spelling must not silently select a person for chat filtering")
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

type deadlineChatCrawler struct {
	archive string
}

func (c *deadlineChatCrawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:           "telegram",
		DisplayName:  "Telegram",
		DefaultPaths: trawlkit.Paths{Archive: c.archive},
	}
}

func (*deadlineChatCrawler) Status(context.Context, *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus("telegram", "Telegram")
	return &status, nil
}

func (*deadlineChatCrawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{{Name: "chats"}}
}

func (*deadlineChatCrawler) Chats(ctx context.Context, _ *trawlkit.Request, _ trawlkit.ChatQuery) ([]trawlkit.Chat, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestListSourceChatsPreservesTimeoutReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	archive := filepath.Join(t.TempDir(), "telegram.db")
	store, err := ckstore.Open(context.Background(), ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	crawler := &deadlineChatCrawler{archive: archive}
	runtime := &Runtime{ctx: context.Background(), timeout: time.Millisecond, stderr: io.Discard, root: &CLI{}, now: time.Now}
	result := runtime.listSourceChats(Source{ID: "telegram", DisplayName: "Telegram", Crawler: crawler}, ChatsCmd{Limit: 50}, nil)
	if !isTimeoutError(result.err) {
		t.Fatalf("error = %v, want chats timeout", result.err)
	}
	if got := failureReason(result.err); got != "timeout" {
		t.Fatalf("failure reason = %q, want timeout", got)
	}
	if got := runtime.reasonDetail(failureReason(result.err)); got != "timed out after 1ms" {
		t.Fatalf("timeout detail = %q", got)
	}
}
