package gogcrawl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	gmailopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/gmail/open/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	input := archive.OpenResult{
		Ref: "gmail:msg/fixture-1", ID: "fixture-1", ThreadID: "thread-1", Time: "2026-07-10T14:00:00Z",
		Headers: archive.MailHeaders{FromName: "Avery Example", FromAddress: "avery@example.com", ToAddress: "morgan@example.com", CcAddress: "team@example.com", Subject: "Project Lantern"},
		Labels:  []string{"INBOX", "STARRED"}, Unread: true,
		Attachments: []archive.Attachment{{Filename: "brief.pdf", MIMEType: "application/pdf", Size: 2048}},
		Body:        "Synthetic review body.", BodyTruncated: true, BodyElidedChars: 17,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	record := projectOpenRecord(input)
	assertRecordIdentity(t, string(record.ProtoReflect().Descriptor().FullName()), "trawl.source.gmail.open.v1.GmailRecord")
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	want := `{"ref":"gmail:msg/fixture-1","id":"fixture-1","thread_id":"thread-1","time":"2026-07-10T14:00:00Z","headers":{"from_name":"Avery Example","from_address":"avery@example.com","to_address":"morgan@example.com","cc_address":"team@example.com","subject":"Project Lantern"},"labels":["INBOX","STARRED"],"unread":true,"attachments":[{"filename":"brief.pdf","mime_type":"application/pdf","size":"2048"}],"body":"Synthetic review body.","body_truncated":true,"body_elided_chars":"17"}`
	wantRecord := &gmailopenv1.GmailRecord{}
	if err := protojson.Unmarshal([]byte(want), wantRecord); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(record, wantRecord) {
		t.Fatalf("record = %s\nwant = %s", text, prototext.Format(wantRecord))
	}
	if text != prototext.Format(wantRecord) {
		t.Fatal("protobuf text changed")
	}
	var actualCompact, wantCompact bytes.Buffer
	if err := json.Compact(&actualCompact, data); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&wantCompact, []byte(want)); err != nil {
		t.Fatal(err)
	}
	if actualCompact.String() != wantCompact.String() {
		t.Fatalf("ProtoJSON = %s\nwant = %s", data, want)
	}
	presentation := projectOpenPresentation(input)
	if presentation.Title != "Project Lantern" || len(presentation.Blocks) != 4 || len(presentation.Facts) != 1 || presentation.Facts[0].Message != "Message body is truncated; 17 characters omitted." {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Project Lantern"
blocks: { heading: { text: "Project Lantern" } anchor_id: "subject" }
blocks: { fields: { fields: { label: "From" display: "Avery Example <avery@example.com>" } fields: { label: "To" display: "morgan@example.com" } fields: { label: "Cc" display: "team@example.com" } fields: { label: "Date" display: "10 July 2026 at 14:00" } fields: { label: "Labels" display: "INBOX, STARRED" } fields: { label: "Unread" display: "Yes" } } }
blocks: { prose: { text: "Synthetic review body." } anchor_id: "body" }
blocks: { table: { columns: "File" columns: "Type" columns: "Bytes" rows: { role: ROLE_NORMAL cells: { display: "brief.pdf" } cells: { display: "application/pdf" } cells: { display: "2.0 KiB" } anchor_id: "attachment-1" } } }
facts: { kind: KIND_TRUNCATION message: "Message body is truncated; 17 characters omitted." }
primary_anchor_id: "body"`)
	assertOpenPresentation(t, "gmail", input, record, presentation)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Headers.Subject = ""
		if got := projectOpenPresentation(blank).Title; got != "(no subject)" {
			t.Fatalf("title = %q", got)
		}
	})
}

func TestMessagePresentationContainsSubjectAndBodySearchAnchors(t *testing.T) {
	value := archive.OpenResult{Ref: "gmail:msg/example", ID: "example", Headers: archive.MailHeaders{Subject: "Lantern subject"}, Body: "Lantern body", Attachments: []archive.Attachment{{Filename: "lantern.pdf", MIMEType: "application/pdf", Size: 42}}}
	record := &openv1.OpenRecord{SourceId: "gmail", OpenRef: value.Ref, Data: &anypb.Any{TypeUrl: "type.example/gmail"}, Presentation: projectOpenPresentation(value)}
	for _, anchorID := range []string{"subject", "body", "attachment-1"} {
		if err := openrecord.ValidateRequestedAnchor(record, anchorID); err != nil {
			t.Fatalf("anchor %q: %v", anchorID, err)
		}
	}
}

func TestOpenRecordTimestampBoundary(t *testing.T) {
	if err := validateOpenTimestamps(archive.OpenResult{Time: "2026-07-10T14:00:00.5+02:00"}); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenTimestamps(archive.OpenResult{Time: "bad timestamp"}); err == nil {
		t.Fatal("accepted malformed date")
	}
	if err := validateOpenTimestamps(archive.OpenResult{}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecordFixtureBoundary(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	root := t.TempDir()
	paths := trawlkit.Paths{Archive: filepath.Join(root, "gmail.db")}
	source := New()
	source.syncQuery = "project"
	source.syncMax = 25
	source.backupRepoPath = filepath.Join(root, "backup")
	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Sync(ctx, &trawlkit.Request{Store: writeStore, Paths: paths, Progress: func(trawlkit.Progress) {}}); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	setTime := func(value string) {
		store, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.DB().ExecContext(ctx, `update messages set time = ? where id = ?`, value, "m3")
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	setTime("2026-07-10T14:00:00.5+02:00")
	readStore := ckstoreOpenRead(t, ctx, paths.Archive)
	record, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, "gmail:msg/m3")
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := machine.(*gmailopenv1.GmailRecord)
	if !ok {
		t.Fatalf("typed record = %T", machine)
	}
	if typed.GetTime() != "2026-07-10T14:00:00.5+02:00" {
		t.Fatalf("typed time = %q", typed.GetTime())
	}
	if got := record.Presentation.Blocks[1].GetFields().GetFields()[3].GetDisplay(); got != "10 July 2026 at 14:00" {
		t.Fatalf("date display = %q", got)
	}
	setTime("not-a-timestamp")
	readStore = ckstoreOpenRead(t, ctx, paths.Archive)
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, "gmail:msg/m3")
	_ = readStore.Close()
	if err == nil {
		t.Fatal("accepted malformed archive timestamp")
	}
}

func ckstoreOpenRead(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	store, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func assertRecordIdentity(t *testing.T, name, want string) {
	t.Helper()
	if name != want {
		t.Fatalf("message name = %q, want %q", name, want)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/"+want {
		t.Fatal("type URL changed")
	}
}

func assertOpenPresentation(t *testing.T, source string, input any, machine interface {
	proto.Message
	GetRef() string
}, presentation *presentationv1.PresentationDocument) {
	t.Helper()
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	open := &openv1.OpenRecord{SourceId: source, OpenRef: machine.GetRef(), Data: packed, Presentation: presentation}
	if err := openrecord.Validate(open); err != nil {
		t.Fatal(err)
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, "input.json", inputJSON)
	writeEvidence(t, source, "record.pbtxt", []byte(prototext.Format(machine)))
	writeEvidence(t, source, "presentation.pbtxt", []byte(prototext.Format(presentation)))
	writeEvidence(t, source, "validated-open.pbtxt", []byte(prototext.Format(open)))
}

func writeEvidence(t *testing.T, source, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	directory = filepath.Join(directory, source)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func writeRuntimeOpenEvidence(t *testing.T, source, caseName, ref string, loaded any, record *openv1.OpenRecord) {
	t.Helper()
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "argv-ref.txt"), []byte("OpenRecord "+ref+"\n"))
	loadedJSON, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "loaded-value.json"), append(loadedJSON, '\n'))
	writeEvidence(t, source, filepath.Join(caseName, "machine.pbtxt"), []byte(prototext.Format(machine)))
	writeEvidence(t, source, filepath.Join(caseName, "presentation.pbtxt"), []byte(prototext.Format(record.Presentation)))
	writeEvidence(t, source, filepath.Join(caseName, "validated-open.pbtxt"), []byte(prototext.Format(record)))
}

func writeLegacyOpenEvidence(t *testing.T, source, caseName, format string, stdout []byte, err error) {
	t.Helper()
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stdout.txt"), stdout)
	writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stderr.txt"), nil)
	if err == nil {
		writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("0\n"))
		writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), nil)
		return
	}
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("1\n"))
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), []byte(err.Error()+"\n"))
}

func assertLegacyOpenGolden(t *testing.T, stdout []byte, err error, wantSHA256 string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(stdout)); got != wantSHA256 {
		t.Fatalf("legacy open stdout SHA-256 = %s, want %s", got, wantSHA256)
	}
}

func writeRawEvidence(t *testing.T, source, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	directory = filepath.Join(directory, source)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(directory, name)), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func assertExactPresentation(t *testing.T, got *presentationv1.PresentationDocument, wantText string) {
	t.Helper()
	want := &presentationv1.PresentationDocument{}
	if err := prototext.Unmarshal([]byte(wantText), want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) || prototext.Format(got) != prototext.Format(want) {
		t.Fatalf("presentation = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
}
