package cli

import (
	"encoding/binary"
	"sort"
	"strings"
	"testing"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"google.golang.org/protobuf/proto"
)

var betaSourceOrder = []string{"imessage", "whatsapp", "telegram", "notes", "contacts"}

func TestSourcePolicyDefaultsToBetaAndHasExplicitAllSourceOverride(t *testing.T) {
	for _, value := range []string{"", "0", "true"} {
		t.Run("override="+value, func(t *testing.T) {
			t.Setenv(allSourcesEnvironmentKey, value)
			if got := registeredSourceIDs(); strings.Join(got, ",") != strings.Join(betaSourceOrder, ",") {
				t.Fatalf("registered sources = %v, want %v", got, betaSourceOrder)
			}
		})
	}

	t.Setenv(allSourcesEnvironmentKey, "1")
	wantAll := []string{"imessage", "whatsapp", "telegram", "notes", "contacts", "gmail", "calendar", "photos", "twitter"}
	if got := registeredSourceIDs(); strings.Join(got, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("all-source registered sources = %v, want %v", got, wantAll)
	}
}

func TestBetaPolicyDrivesHumanAndAppWireSurfaces(t *testing.T) {
	t.Setenv(allSourcesEnvironmentKey, "")
	for _, args := range [][]string{
		nil,
		{"--help"},
		{"status"},
		{"search", "synthetic-no-match"},
	} {
		stdout, stderr, _ := runCLI(t, args...)
		assertNoExperimentalSources(t, strings.Join(args, " "), stdout+stderr)
	}

	for _, args := range [][]string{
		{"search", "synthetic", "--source", "gmail"},
		{"sync", "gmail"},
		{"open", "gmail:message/synthetic"},
		{"gmail"},
	} {
		stdout, stderr, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("trawl %s unexpectedly succeeded: %s%s", strings.Join(args, " "), stdout, stderr)
		}
		if output := stdout + stderr; output != "" && !strings.Contains(output, "not found") && !strings.Contains(output, "unknown command") {
			t.Fatalf("trawl %s did not reject hidden source: %s%s", strings.Join(args, " "), stdout, stderr)
		}
	}

	stdout, stderr, code := runCLI(t, "__app", "status")
	if code != 0 || stderr != "" {
		t.Fatalf("app status code=%d stderr=%q", code, stderr)
	}
	ids := appStatusSourceIDs(t, stdout)
	want := append([]string(nil), betaSourceOrder...)
	sort.Strings(want)
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("app status sources = %v, want %v", ids, want)
	}

	for _, args := range [][]string{
		{"__app", "search", "--source", "gmail", "synthetic"},
		{"__app", "sync", "--source", "gmail"},
		{"__app", "resource", "photos", "photos:resource/synthetic", "32"},
		{"__app", "request-photos"},
	} {
		_, _, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("trawl %s unexpectedly exposed an experimental source", strings.Join(args, " "))
		}
	}

	stdout, stderr, code = runCLI(t, "__app", "open", "gmail", "gmail:message/synthetic", "match")
	if code != 0 || stderr != "" {
		t.Fatalf("app open code=%d stderr=%q", code, stderr)
	}
	frame := []byte(stdout)
	if len(frame) < 4 || int(binary.LittleEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("invalid app open frame length %d", len(frame))
	}
	var openResponse openv1.OpenResponse
	if err := proto.Unmarshal(frame[4:], &openResponse); err != nil {
		t.Fatal(err)
	}
	if openResponse.GetFailure().GetCode() != federationv1.FailureCode_FAILURE_CODE_NOT_FOUND {
		t.Fatalf("hidden app open response = %#v", &openResponse)
	}
}

func TestAllSourceOverrideReachesHumanAndAppWireSurfaces(t *testing.T) {
	t.Setenv(allSourcesEnvironmentKey, "1")
	stdout, stderr, code := runCLI(t)
	if code != 0 || stderr != "" {
		t.Fatalf("bare trawl code=%d stderr=%q", code, stderr)
	}
	for _, name := range []string{"Gmail", "Calendar", "Photos", "Twitter (X)"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("all-source front door missing %q:\n%s", name, stdout)
		}
	}

	stdout, stderr, code = runCLI(t, "__app", "status")
	if code != 0 || stderr != "" {
		t.Fatalf("all-source app status code=%d stderr=%q", code, stderr)
	}
	wantAll := []string{"calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter", "whatsapp"}
	if got := appStatusSourceIDs(t, stdout); strings.Join(got, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("all-source app status sources = %v, want %v", got, wantAll)
	}
}

func appStatusSourceIDs(t *testing.T, framed string) []string {
	t.Helper()
	frame := []byte(framed)
	if len(frame) < 4 || int(binary.LittleEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("invalid app status frame length %d", len(frame))
	}
	var response federationv1.StatusResponse
	if err := proto.Unmarshal(frame[4:], &response); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{})
	for _, source := range response.GetSources() {
		seen[source.GetManifest().GetSourceId()] = struct{}{}
	}
	for _, failure := range response.GetFailures() {
		seen[failure.GetSourceId()] = struct{}{}
	}
	for _, skipped := range response.GetSkippedSources() {
		seen[skipped.GetSourceId()] = struct{}{}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func registeredSourceIDs() []string {
	crawlers := registeredCrawlers()
	ids := make([]string, 0, len(crawlers))
	for _, crawler := range crawlers {
		ids = append(ids, crawler.Info().ID)
	}
	return ids
}

func assertNoExperimentalSources(t *testing.T, surface, output string) {
	t.Helper()
	for _, forbidden := range []string{"Gmail", "Calendar", "Photos", "Twitter (X)", "gmail", "calendar", "photos", "twitter"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("%s leaked experimental source %q:\n%s", surface, forbidden, output)
		}
	}
}
