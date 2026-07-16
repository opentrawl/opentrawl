package conformance

import (
	"strings"
	"testing"
)

func TestCheckHumanOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean output",
			in: strings.Join([]string{
				"Status: ok",
				"Archive is fresh.",
				"",
				"Local archive:",
				"  Database: /tmp/example.db",
				"  Last sync: 2026-07-02T14:03:11+02:00",
				"Recent log:",
				"  Last run: sync succeeded",
			}, "\n"),
		},
		{
			name: "snake case dump",
			in:   "db_path: /tmp/example.db\n",
			want: "line 1 looks like a snake_case key dump: db_path",
		},
		{
			name: "snake case table header allowed",
			in:   "db_path\tvalue\narchive_path\t/tmp/example.db\n",
		},
		{
			name: "key value dump",
			in:   "Most recent error: sync_failed error=boom\n",
			want: "line 1 looks like a key=value dump: error",
		},
		{
			name: "base64 run",
			in:   "Body: 1ByAINA1BGQRt+dXCj9dBTDK4eRNAF0nB7e7NNFiS0kAAAA=\n",
			want: "line 1 contains a base64-like run over 40 characters",
		},
		{
			name: "unmapped enum",
			in:   "Status: status_12\n",
			want: "line 1 contains an unmapped enum value: status_12",
		},
		{
			name: "replacement rune",
			in:   "Snippet: bad \uFFFD text\n",
			want: "line 1 contains U+FFFD replacement text",
		},
		{
			name: "me must be lowercase",
			in:   "From: Me\n",
			want: "line 1 renders \"Me\"; use lowercase me",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := CheckHumanOutput(tc.in)
			if tc.want == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if !containsFailure(failures, tc.want) {
				t.Fatalf("failures = %v, want %q", failures, tc.want)
			}
		})
	}
}

func TestCheckSearchEnvelope(t *testing.T) {
	good := `{"query":"launch","results":[{"ref":"imessage:msg/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"archive_context":[{"kind":"received","label":"Received"}],"evidence":[{"label":"Match","text":{"runs":[{"text":"middle","matched":true}]}}],"who":"me","where":"Alice Example","snippet_front_truncated":true,"snippet":"` + "\u2026" + `middle of the message"}]}`
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean output",
			in:   good,
		},
		{
			name: "invalid json",
			in:   `{`,
			want: "search envelope is not valid JSON",
		},
		{
			name: "missing results",
			in:   `{"query":"launch"}`,
			want: "search envelope is missing results array",
		},
		{
			name: "bad time",
			in:   `{"results":[{"ref":"imessage:msg/1","time":"yesterday","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"hello","matched":true}]}}],"snippet":"hello"}]}`,
			want: `search result 0 time is not RFC3339: "yesterday"`,
		},
		{
			name: "bad ref",
			in:   `{"results":[{"ref":"message/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"hello","matched":true}]}}],"snippet":"hello"}]}`,
			want: `search result 0 ref is not source-prefixed: "message/1"`,
		},
		{
			name: "missing archive context",
			in:   `{"results":[{"ref":"imessage:msg/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"hello","matched":true}]}}],"snippet":"hello"}]}`,
			want: "search result 0 has no archive context",
		},
		{
			name: "uppercase me",
			in:   `{"results":[{"ref":"imessage:msg/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"hello","matched":true}]}}],"who":"Me","snippet":"hello"}]}`,
			want: `search result 0 who renders "Me"; use lowercase me`,
		},
		{
			name: "front truncation without marker",
			in:   `{"results":[{"ref":"imessage:msg/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"middle","matched":true}]}}],"snippet_front_truncated":true,"snippet":"middle of the message"}]}`,
			want: "search result 0 snippet is front-truncated but has no leading marker",
		},
		{
			name: "unmapped enum in snippet",
			in:   `{"results":[{"ref":"calendar:event/1","time":"2026-07-02T14:03:11+02:00","anchor_id":"match","summary":{"title":"Synthetic"},"evidence":[{"label":"Match","text":{"runs":[{"text":"status_7","matched":true}]}}],"snippet":"status_7"}]}`,
			want: "search result 0 snippet contains an unmapped enum value: status_7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := CheckSearchEnvelope([]byte(tc.in))
			if tc.want == "" {
				if len(failures) != 0 {
					t.Fatalf("failures = %v, want none", failures)
				}
				return
			}
			if !containsFailure(failures, tc.want) {
				t.Fatalf("failures = %v, want %q", failures, tc.want)
			}
		})
	}
}

func containsFailure(failures []string, want string) bool {
	for _, failure := range failures {
		if strings.Contains(failure, want) {
			return true
		}
	}
	return false
}

// Long alphanumeric runs without + or = are paths and test names,
// not base64 — they must not flag.
func TestBase64CheckIgnoresPathLikeRuns(t *testing.T) {
	out := "db: /var/folders/3k/TestMetadataAndSyncTextOutputIsAgentReadable2887315221/001/chat.sqlite\n"
	for _, f := range CheckHumanOutput(out) {
		t.Errorf("unexpected failure: %s", f)
	}
}

func TestAppleConstantPattern(t *testing.T) {
	for _, leak := range []string{"MKPOICategoryFitnessCenter", "PHAssetMediaTypeImage", "CLAuthorizationStatusDenied"} {
		if appleConstantPattern.FindString(leak) == "" {
			t.Fatalf("apple constant %q not caught", leak)
		}
	}
	for _, clean := range []string{"fitness centre", "McDonald's Amsterdam", "IMG_8339.HEIC", "MacBook Pro"} {
		if match := appleConstantPattern.FindString(clean); match != "" {
			t.Fatalf("false positive %q in %q", match, clean)
		}
	}
}
