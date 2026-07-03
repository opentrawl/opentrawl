package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func TestSyncedAddressBookNamesPopulateSearchAndOpen(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookFixture(t, addressBookPath)

	result, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NamedContacts != 2 {
		t.Fatalf("named contacts = %d, want 2", result.NamedContacts)
	}

	searchOut := runOK(t, "--archive", archivePath, "--json", "search", "--limit", "3", "dinner")
	var search searchListJSON
	if err := json.Unmarshal([]byte(searchOut), &search); err != nil {
		t.Fatalf("search json = %s err=%v", searchOut, err)
	}
	bySnippet := map[string]searchResultJSON{}
	for _, item := range search.Results {
		bySnippet[item.Snippet] = item
	}
	assertSearchName(t, bySnippet, "phone dinner plan", "Katja Example", "Katja Example")
	assertSearchName(t, bySnippet, "email dinner plan", "Alice Mail", "Alice Mail")
	assertSearchName(t, bySnippet, "unmatched dinner plan", "+15550999", "+15550999")

	openOut := runOK(t, "--archive", archivePath, "--json", "open", bySnippet["phone dinner plan"].Ref)
	var opened openJSON
	if err := json.Unmarshal([]byte(openOut), &opened); err != nil {
		t.Fatalf("open json = %s err=%v", openOut, err)
	}
	if opened.Chat.Name != "Katja Example" || opened.Message.Who != "Katja Example" || opened.Message.Where != "Katja Example" {
		t.Fatalf("open names = %#v", opened)
	}
	if len(opened.Chat.Participants) != 1 || opened.Chat.Participants[0] != "Katja Example" {
		t.Fatalf("open participants = %#v", opened.Chat.Participants)
	}

	statusOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "status")
	var status statusOutput
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatalf("status json = %s err=%v", statusOut, err)
	}
	if status.Archive == nil || status.Archive.NamedContacts != 2 {
		t.Fatalf("status named contacts = %#v", status.Archive)
	}
	if got := countValue(status.Counts, "named_contacts"); got != 2 {
		t.Fatalf("named_contacts count = %d, want 2; counts=%#v", got, status.Counts)
	}
}

func TestSearchWhoFiltersSyncedNamesInArchiveQuery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookFixture(t, addressBookPath)
	addFixtureMessage(t, dbPath, 4, 1, 1, 400, "second phone dinner plan")

	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	filteredOut := runOK(t, "--archive", archivePath, "--json", "search", "--who", "Katja Example", "--limit", "1", "dinner")
	var filtered searchListJSON
	if err := json.Unmarshal([]byte(filteredOut), &filtered); err != nil {
		t.Fatalf("filtered search json = %s err=%v", filteredOut, err)
	}
	if filtered.TotalMatches != 2 || !filtered.Truncated || len(filtered.Results) != 1 {
		t.Fatalf("filtered search envelope = %#v", filtered)
	}
	if filtered.WhoResolved == nil || filtered.WhoResolved.Who != "Katja Example" || !hasString(filtered.WhoResolved.Identifiers, "+15550100") {
		t.Fatalf("filtered search who_resolved = %#v", filtered.WhoResolved)
	}
	if filtered.Results[0].Who != "Katja Example" || filtered.Results[0].Where != "Katja Example" || !strings.Contains(filtered.Results[0].Snippet, "phone dinner") {
		t.Fatalf("filtered search result = %#v", filtered.Results[0])
	}
	if strings.Contains(filteredOut, "email dinner plan") || strings.Contains(filteredOut, "unmatched dinner plan") {
		t.Fatalf("filtered search leaked unfiltered matches: %s", filteredOut)
	}

	caseOut := runOK(t, "--archive", archivePath, "--json", "search", "dinner", "--who", "katja example", "--limit", "3")
	var caseFiltered searchListJSON
	if err := json.Unmarshal([]byte(caseOut), &caseFiltered); err != nil {
		t.Fatalf("case search json = %s err=%v", caseOut, err)
	}
	if caseFiltered.TotalMatches != 2 || caseFiltered.Truncated || len(caseFiltered.Results) != 2 {
		t.Fatalf("case-insensitive search = %#v", caseFiltered)
	}
	if caseFiltered.WhoResolved == nil || caseFiltered.WhoResolved.Who != "Katja Example" {
		t.Fatalf("case-insensitive who_resolved = %#v", caseFiltered.WhoResolved)
	}

	rawOut := runOK(t, "--archive", archivePath, "--json", "search", "dinner", "--who", "+15550999")
	var rawFiltered searchListJSON
	if err := json.Unmarshal([]byte(rawOut), &rawFiltered); err != nil {
		t.Fatalf("raw handle search json = %s err=%v", rawOut, err)
	}
	if rawFiltered.TotalMatches != 1 || len(rawFiltered.Results) != 1 || rawFiltered.Results[0].Who != "+15550999" {
		t.Fatalf("raw handle search = %#v", rawFiltered)
	}
	if rawFiltered.WhoResolved == nil || rawFiltered.WhoResolved.Who != "+15550999" || !hasString(rawFiltered.WhoResolved.Identifiers, "+15550999") {
		t.Fatalf("raw handle who_resolved = %#v", rawFiltered.WhoResolved)
	}
}

func TestWhoCommandResolvesGenerouslyAndDedupesContacts(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookRowsFixture(t, addressBookPath, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Özge', 'Example', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '555-0100', '+1', '', '5550100', 1)`,
		`insert into ZABCDEMAILADDRESS(Z_PK, ZADDRESS, ZOWNER) values (1, 'ALICE@EXAMPLE.COM', 1)`,
	})
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	out := runOK(t, "--archive", archivePath, "--json", "who", "ozge")
	var payload whoEnvelopeJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("who json = %s err=%v", out, err)
	}
	if payload.Query != "ozge" || len(payload.Candidates) != 1 {
		t.Fatalf("who payload = %#v", payload)
	}
	candidate := payload.Candidates[0]
	if candidate.Who != "Özge Example" || candidate.Messages != 2 || candidate.LastSeen == "" {
		t.Fatalf("who candidate = %#v", candidate)
	}
	if !hasString(candidate.Identifiers, "+15550100") || !hasString(candidate.Identifiers, "alice@example.com") {
		t.Fatalf("who identifiers = %#v", candidate.Identifiers)
	}

	human := runOK(t, "--archive", archivePath, "who", "ozge")
	conformance.AssertHumanOutput(t, human)
	if !strings.Contains(human, "who") || !strings.Contains(human, "identifiers") || !strings.Contains(human, "Özge Example") {
		t.Fatalf("who human output =\n%s", human)
	}
}

func TestWhoCommandCloseSpelling(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookFixture(t, addressBookPath)
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	out := runOK(t, "--archive", archivePath, "--json", "who", "Katia")
	var payload whoEnvelopeJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("who close json = %s err=%v", out, err)
	}
	if len(payload.Candidates) != 1 || payload.Candidates[0].Who != "Katja Example" {
		t.Fatalf("close spelling candidates = %#v", payload.Candidates)
	}
}

func TestSearchWhoCloseSpellingOnlySingleMatchSuggests(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookFixture(t, addressBookPath)
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--archive", archivePath, "--json", "search", "--who", "Katia", "dinner"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 5 {
		t.Fatalf("close-only search err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("close-only error json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.Code != "unknown_who" || payload.Error.DidYouMeanTotal != 1 || len(payload.Error.DidYouMean) != 1 {
		t.Fatalf("close-only error = %#v", payload.Error)
	}
	if payload.Error.DidYouMean[0].Who != "Katja Example" {
		t.Fatalf("close-only suggestion = %#v", payload.Error.DidYouMean)
	}
	if strings.Contains(stdout.String(), "results") {
		t.Fatalf("close-only search ran: %s", stdout.String())
	}
}

func TestSearchWhoUnknownReturnsContractError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--archive", archivePath, "--json", "search", "--who", "nobody here", "launch"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 5 {
		t.Fatalf("unknown who err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unknown error json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.Code != "unknown_who" || payload.Error.DidYouMean == nil || payload.Error.Hint == "" {
		t.Fatalf("unknown error = %#v", payload)
	}
	if strings.Contains(stdout.String(), "results") {
		t.Fatalf("unknown search ran: %s", stdout.String())
	}
}

func TestSearchFilterOnlyUsesNewestMatchesAndHonestTotals(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	after := archive.AppleDateTime(199).Format(time.RFC3339Nano)
	before := archive.AppleDateTime(301).Format(time.RFC3339Nano)
	out := runOK(t, "--archive", archivePath, "--json", "search", "--after", after, "--before", before, "--limit", "2")
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("filter-only json = %s err=%v", out, err)
	}
	if payload.Query != "" || payload.TotalMatches != 3 || !payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("filter-only envelope = %#v", payload)
	}
	if !strings.Contains(payload.Results[0].Snippet, "group fallback row") || !strings.Contains(payload.Results[1].Snippet, "latest launch note") {
		t.Fatalf("filter-only order = %#v", payload.Results)
	}
	conformance.AssertSearchEnvelope(t, []byte(out))

	filtered := runOK(t, "--archive", archivePath, "--json", "search", "--who", "Most Recent Name", "--limit", "1")
	var whoOnly searchListJSON
	if err := json.Unmarshal([]byte(filtered), &whoOnly); err != nil {
		t.Fatalf("who-only json = %s err=%v", filtered, err)
	}
	if whoOnly.Query != "" || whoOnly.WhoResolved == nil || whoOnly.TotalMatches != 2 || len(whoOnly.Results) != 1 {
		t.Fatalf("who-only filter search = %#v", whoOnly)
	}
}

func TestSearchWhoDedupesMappedHandleVariants(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createMessagesFixture(t, dbPath)
	makeSharedParticipantNameFixture(t, dbPath)
	createAddressBookRowsFixture(t, addressBookPath, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Shared', 'Example', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '555-0100', '+1', '', '5550100', 1)`,
	})
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	out := runOK(t, "--archive", archivePath, "--json", "search", "--who", " shared   example ", "shared")
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("deduped search json = %s err=%v", out, err)
	}
	if payload.TotalMatches != 2 || payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("deduped search envelope = %#v", payload)
	}
	if payload.WhoResolved == nil || payload.WhoResolved.Who != "Shared Example" || !hasString(payload.WhoResolved.Identifiers, "+15550100") || !hasString(payload.WhoResolved.Identifiers, "0015550100") {
		t.Fatalf("deduped who_resolved = %#v", payload.WhoResolved)
	}
	if !snippetsContain(payload.Results, "shared marker one") || !snippetsContain(payload.Results, "shared marker two") {
		t.Fatalf("deduped search did not filter across both mapped handles = %#v", payload.Results)
	}
}

func TestSearchWhoMergesSameStoredNames(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	makeSharedParticipantNameFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	out := runOK(t, "--archive", archivePath, "--json", "search", "--who", "shared example", "shared")
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("merged search json = %s err=%v", out, err)
	}
	if payload.TotalMatches != 2 || payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("merged search envelope = %#v", payload)
	}
	if payload.WhoResolved == nil || payload.WhoResolved.Who != "Shared Example" || !hasString(payload.WhoResolved.Identifiers, "+15550100") || !hasString(payload.WhoResolved.Identifiers, "0015550100") {
		t.Fatalf("merged who_resolved = %#v", payload.WhoResolved)
	}
	if !snippetsContain(payload.Results, "shared marker one") || !snippetsContain(payload.Results, "shared marker two") {
		t.Fatalf("merged search did not filter across both stored names = %#v", payload.Results)
	}
}

func TestSearchWhoRejectsAmbiguousDirectMatches(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--archive", archivePath, "--json", "search", "--who", "name", "launch"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 4 {
		t.Fatalf("ambiguous search err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("ambiguous error json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.Code != "ambiguous_who" || len(payload.Error.Candidates) != 2 {
		t.Fatalf("ambiguous error = %#v", payload)
	}
	if payload.Error.CandidateTotal != 2 || payload.Error.Candidates[0].Who == "" || len(payload.Error.Candidates[0].Identifiers) == 0 {
		t.Fatalf("ambiguous candidates = %#v", payload.Error.Candidates)
	}
	if strings.Contains(stdout.String(), "results") {
		t.Fatalf("ambiguous search ran: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(context.Background(), []string{"--archive", archivePath, "search", "--who", "name", "launch"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 4 {
		t.Fatalf("human ambiguous search err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("human ambiguous wrote stdout: %s", stdout.String())
	}
	for _, want := range []string{
		`ambiguous_who: "name" matches more than one person.`,
		"who",
		"identifiers",
		"Retry: imsgcrawl search --who",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("human ambiguous stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestSearchWhoDedupesOneContactWithPhoneAndEmail(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createContactlessMessagesFixture(t, dbPath)
	createAddressBookRowsFixture(t, addressBookPath, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION) values (1, 'Özge', 'Example', '')`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '555-0100', '+1', '', '5550100', 1)`,
		`insert into ZABCDEMAILADDRESS(Z_PK, ZADDRESS, ZOWNER) values (1, 'ALICE@EXAMPLE.COM', 1)`,
	})
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	out := runOK(t, "--archive", archivePath, "--json", "search", "dinner", "--who", "özge   example")
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unicode search json = %s err=%v", out, err)
	}
	if payload.TotalMatches != 2 || payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("unicode search envelope = %#v", payload)
	}
	if payload.WhoResolved == nil || payload.WhoResolved.Who != "Özge Example" || !hasString(payload.WhoResolved.Identifiers, "+15550100") || !hasString(payload.WhoResolved.Identifiers, "alice@example.com") {
		t.Fatalf("unicode who_resolved = %#v", payload.WhoResolved)
	}
	if !snippetsContain(payload.Results, "phone dinner plan") || !snippetsContain(payload.Results, "email dinner plan") {
		t.Fatalf("unicode search did not filter across both contact handles = %#v", payload.Results)
	}
	if snippetsContain(payload.Results, "unmatched dinner plan") {
		t.Fatalf("unicode search leaked unmatched handle = %#v", payload.Results)
	}
}

func TestOwnerPhoneAndEmailHandlesFoldToMe(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	addressBookPath := filepath.Join(dir, "AddressBook-v22.abcddb")
	createOwnerAliasMessagesFixture(t, dbPath)
	createAddressBookRowsFixture(t, addressBookPath, []string{
		`insert into ZABCDRECORD(Z_PK, ZFIRSTNAME, ZLASTNAME, ZORGANIZATION, ZISME) values (1, 'Owner', 'Example', '', 1)`,
		`insert into ZABCDPHONENUMBER(Z_PK, ZFULLNUMBER, ZCOUNTRYCODE, ZAREACODE, ZLOCALNUMBER, ZOWNER) values (1, '555-0100', '+1', '', '5550100', 1)`,
		`insert into ZABCDEMAILADDRESS(Z_PK, ZADDRESS, ZOWNER) values (1, 'OWNER@EXAMPLE.COM', 1)`,
	})
	if _, err := archive.SyncWithOptions(context.Background(), archive.SyncOptions{
		ArchivePath:      archivePath,
		SourcePath:       dbPath,
		AddressBookPaths: []string{addressBookPath},
	}); err != nil {
		t.Fatal(err)
	}

	out := runOK(t, "--archive", archivePath, "--json", "search", "dinner", "--who", "me")
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("owner search json = %s err=%v", out, err)
	}
	if payload.TotalMatches != 2 || payload.Truncated || len(payload.Results) != 2 {
		t.Fatalf("owner search envelope = %#v", payload)
	}
	if payload.WhoResolved == nil || payload.WhoResolved.Who != "me" {
		t.Fatalf("owner who_resolved = %#v", payload.WhoResolved)
	}
	if snippetsContain(payload.Results, "other dinner plan") {
		t.Fatalf("owner search leaked non-owner row = %#v", payload.Results)
	}
	bySnippet := map[string]searchResultJSON{}
	for _, item := range payload.Results {
		bySnippet[item.Snippet] = item
	}
	assertSearchName(t, bySnippet, "owner alias dinner plan", "me", "me")
	assertSearchName(t, bySnippet, "sent dinner plan", "me", "Other Person")

	openOut := runOK(t, "--archive", archivePath, "--json", "open", bySnippet["owner alias dinner plan"].Ref)
	var opened openJSON
	if err := json.Unmarshal([]byte(openOut), &opened); err != nil {
		t.Fatalf("owner open json = %s err=%v", openOut, err)
	}
	if opened.Message.Who != "me" || opened.Chat.Name != "me" || opened.Message.FromMe {
		t.Fatalf("owner alias open = %#v", opened)
	}

	messagesOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "1", "--asc")
	var messages messageListJSON
	if err := json.Unmarshal([]byte(messagesOut), &messages); err != nil {
		t.Fatalf("owner messages json = %s err=%v", messagesOut, err)
	}
	if len(messages.Items) != 1 || messages.Items[0].SenderLabel != "me" {
		t.Fatalf("owner transcript labels = %#v", messages.Items)
	}
}

func TestSearchWhoRejectsBlankIdentity(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"search", "--who", " \t ", "dinner"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("Run() expected usage error, got err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(err.Error(), "search --who requires an identity") {
		t.Fatalf("err = %v", err)
	}
}

func createOwnerAliasMessagesFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	schema := []string{
		`create table handle (ROWID integer primary key, id text not null, service text not null, uncanonicalized_id text)`,
		`create table chat (ROWID integer primary key, guid text not null, display_name text, chat_identifier text, service_name text, room_name text, is_archived integer)`,
		`create table chat_handle_join (chat_id integer, handle_id integer)`,
		`create table message (ROWID integer primary key, guid text not null, handle_id integer, date integer, service text, account text, is_from_me integer, text text, attributedBody blob)`,
		`create table chat_message_join (chat_id integer, message_id integer)`,
		`create table message_attachment_join (message_id integer, attachment_id integer)`,
	}
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	inserts := []string{
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550100', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, 'owner@example.com', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (3, '+15550200', 'iMessage', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'owner-email-chat', 'Owner Example', 'owner@example.com', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'other-chat', 'Other Person', '+15550200', 'iMessage', '', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 2)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 3)`,
		`insert into message(rowid, guid, handle_id, date, service, account, is_from_me, text, attributedBody) values (1, 'owner-alias-message', 2, 100, 'iMessage', '', 0, 'owner alias dinner plan', null)`,
		`insert into message(rowid, guid, handle_id, date, service, account, is_from_me, text, attributedBody) values (2, 'sent-message', 3, 200, 'iMessage', '+15550100', 1, 'sent dinner plan', null)`,
		`insert into message(rowid, guid, handle_id, date, service, account, is_from_me, text, attributedBody) values (3, 'other-message', 3, 300, 'iMessage', '', 0, 'other dinner plan', null)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 2)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
	}
	for _, stmt := range inserts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSearchName(t *testing.T, results map[string]searchResultJSON, snippet, who, where string) {
	t.Helper()
	item, ok := results[snippet]
	if !ok {
		t.Fatalf("missing snippet %q in %#v", snippet, results)
	}
	if item.Who != who || item.Where != where {
		t.Fatalf("result %q = %#v, want who=%q where=%q", snippet, item, who, where)
	}
	if strings.Contains(item.Who+item.Where, "alice@example.com") {
		t.Fatalf("result leaked matched handle = %#v", item)
	}
}

func countValue(counts []control.Count, id string) int64 {
	for _, count := range counts {
		if count.ID == id {
			return count.Value
		}
	}
	return -1
}

func addFixtureMessage(t *testing.T, path string, messageID, handleID, chatID int64, date int64, text string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (?, ?, ?, ?, 'iMessage', 0, ?, null)`, messageID, "extra-message", handleID, date, text); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into chat_message_join(chat_id, message_id) values (?, ?)`, chatID, messageID); err != nil {
		t.Fatal(err)
	}
}

func makeSharedParticipantNameFixture(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	updates := []string{
		`update chat set display_name = 'Shared Example' where rowid in (1, 2)`,
		`update message set text = 'shared marker one' where rowid = 1`,
		`update message set text = 'shared marker two' where rowid = 3`,
	}
	for _, stmt := range updates {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func snippetsContain(results []searchResultJSON, want string) bool {
	for _, result := range results {
		if strings.Contains(result.Snippet, want) {
			return true
		}
	}
	return false
}
