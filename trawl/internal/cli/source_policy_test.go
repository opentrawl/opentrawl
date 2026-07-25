package cli

import (
	"encoding/binary"
	"sort"
	"strings"
	"testing"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var betaSourceOrder = []string{"imessage", "whatsapp", "telegram", "notes", "contacts", "calendar"}

var sourceArtworkBundleIDs = map[string]string{
	"imessage": "com.apple.MobileSMS",
	"whatsapp": "net.whatsapp.WhatsApp",
	"telegram": "ru.keepcoder.Telegram",
	"notes":    "com.apple.mobilenotes",
	"contacts": "com.apple.MobileAddressBook",
	"gmail":    "com.google.Gmail",
	"calendar": "com.apple.mobilecal",
	"photos":   "com.apple.mobileslideshow",
	"twitter":  "com.atebits.Tweetie2",
}

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

func TestAppSourceCatalogueCarriesCanonicalPresentationAndReleaseState(t *testing.T) {
	t.Setenv(allSourcesEnvironmentKey, "")
	entries, err := sourceCatalogEntries()
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []string{"imessage", "whatsapp", "telegram", "notes", "contacts", "gmail", "calendar", "photos", "twitter"}
	if got := catalogSourceIDs(entries); strings.Join(got, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("catalogue sources = %v, want %v", got, wantAll)
	}
	contractJSON, err := protojson.MarshalOptions{Multiline: true, Indent: "  ", UseProtoNames: true}.Marshal(&federationv1.StatusResponse{Catalog: entries})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production source catalogue contract:\n%s", contractJSON)
	betaIDs := make(map[string]bool, len(betaSourceOrder))
	for _, id := range betaSourceOrder {
		betaIDs[id] = true
	}
	for index, entry := range entries {
		manifest := entry.GetManifest()
		branding := manifest.GetBranding()
		if strings.TrimSpace(manifest.GetDisplayName()) == "" || strings.TrimSpace(branding.GetSymbolName()) == "" || !validHexColour(branding.GetAccentColor()) {
			t.Errorf("catalogue entry %d has incomplete presentation metadata: %v", index, entry)
		}
		if got, want := branding.GetArtworkBundleIdentifier(), sourceArtworkBundleIDs[manifest.GetSourceId()]; got != want {
			t.Errorf("catalogue entry %d artwork bundle identifier = %q, want %q", index, got, want)
		}
		if betaIDs[manifest.GetSourceId()] {
			if entry.GetReleaseState() != federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_AVAILABLE || !entry.GetEnabled() || strings.TrimSpace(branding.GetBundleIdentifier()) == "" {
				t.Errorf("available catalogue entry %d = %v", index, entry)
			}
		} else if entry.GetReleaseState() != federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_COMING_SOON || entry.GetEnabled() {
			t.Errorf("coming-soon catalogue entry %d = %v", index, entry)
		}
	}

	t.Setenv(allSourcesEnvironmentKey, "1")
	localEntries, err := sourceCatalogEntries()
	if err != nil {
		t.Fatal(err)
	}
	for index, entry := range localEntries {
		if !entry.GetEnabled() {
			t.Errorf("local catalogue entry %d is disabled: %v", index, entry)
		}
		id := entry.GetManifest().GetSourceId()
		if betaIDs[id] && entry.GetReleaseState() != federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_AVAILABLE {
			t.Errorf("local override changed available release state for entry %d: %v", index, entry)
		}
		if !betaIDs[id] && entry.GetReleaseState() != federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_COMING_SOON {
			t.Errorf("local override changed product release state for entry %d: %v", index, entry)
		}
	}
}

func TestSourceCatalogueRejectsMissingOrMalformedPresentationMetadata(t *testing.T) {
	tests := []struct {
		name string
		edit func(*crawlerRegistration)
		want string
	}{
		{name: "missing symbol", edit: func(registration *crawlerRegistration) { registration.branding.SymbolName = "" }, want: "symbol name is empty"},
		{name: "malformed colour", edit: func(registration *crawlerRegistration) { registration.branding.AccentColor = "red" }, want: "is not #RRGGBB"},
		{name: "missing available bundle", edit: func(registration *crawlerRegistration) { registration.branding.BundleIdentifier = "" }, want: "bundle identifier is empty"},
		{name: "missing artwork bundle", edit: func(registration *crawlerRegistration) { registration.branding.ArtworkBundleIdentifier = "" }, want: "artwork bundle identifier is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registrations := append([]crawlerRegistration(nil), crawlerFactories...)
			test.edit(&registrations[0])
			old := crawlerFactories
			crawlerFactories = registrations
			t.Cleanup(func() { crawlerFactories = old })
			_, err := sourceCatalogEntries()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("sourceCatalogEntries error = %v, want %q", err, test.want)
			}
		})
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
	response := decodeAppStatusResponse(t, stdout)
	if got := catalogSourceIDs(response.GetCatalog()); strings.Join(got, ",") != "imessage,whatsapp,telegram,notes,contacts,gmail,calendar,photos,twitter" {
		t.Fatalf("app status catalogue = %v", got)
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
	for _, entry := range decodeAppStatusResponse(t, stdout).GetCatalog() {
		if !entry.GetEnabled() {
			t.Fatalf("all-source app status catalogue entry is disabled: %v", entry)
		}
	}
}

func appStatusSourceIDs(t *testing.T, framed string) []string {
	t.Helper()
	response := decodeAppStatusResponse(t, framed)
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

func decodeAppStatusResponse(t *testing.T, framed string) *federationv1.StatusResponse {
	t.Helper()
	frame := []byte(framed)
	if len(frame) < 4 || int(binary.LittleEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("invalid app status frame length %d", len(frame))
	}
	var response federationv1.StatusResponse
	if err := proto.Unmarshal(frame[4:], &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func registeredSourceIDs() []string {
	crawlers := registeredCrawlers()
	ids := make([]string, 0, len(crawlers))
	for _, crawler := range crawlers {
		ids = append(ids, crawler.Info().ID)
	}
	return ids
}

func catalogSourceIDs(entries []*federationv1.SourceCatalogEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.GetManifest().GetSourceId())
	}
	return ids
}

func assertNoExperimentalSources(t *testing.T, surface, output string) {
	t.Helper()
	for _, forbidden := range []string{"Gmail", "Photos", "Twitter (X)", "gmail", "photos", "twitter"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("%s leaked experimental source %q:\n%s", surface, forbidden, output)
		}
	}
}
