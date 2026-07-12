package gogcrawl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	gmailopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/gmail/open/v1"
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
	if presentation.Title != "Project Lantern" || len(presentation.Blocks) != 3 || len(presentation.Facts) != 1 || presentation.Facts[0].Message != "Message body is truncated; 17 characters omitted." {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Project Lantern"
blocks: { fields: { fields: { label: "Ref" display: "gmail:msg/fixture-1" } fields: { label: "From" display: "Avery Example <avery@example.com>" } fields: { label: "To" display: "morgan@example.com" } fields: { label: "Cc" display: "team@example.com" } fields: { label: "Date" display: "2026-07-10T14:00:00Z" } fields: { label: "Labels" display: "INBOX, STARRED" } fields: { label: "Unread" display: "Yes" } } }
blocks: { prose: { text: "Synthetic review body." } }
blocks: { table: { columns: "File" columns: "Type" columns: "Bytes" rows: { role: ROLE_NORMAL cells: { display: "brief.pdf" } cells: { display: "application/pdf" } cells: { display: "2048 bytes" } } } }
facts: { kind: KIND_TRUNCATION message: "Message body is truncated; 17 characters omitted." }`)
	assertOpenPresentation(t, "gmail", input, record, presentation)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Headers.Subject = ""
		if got := projectOpenPresentation(blank).Title; got != "(no subject)" {
			t.Fatalf("title = %q", got)
		}
	})
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

func assertOpenPresentation(t *testing.T, source string, input any, machine proto.Message, presentation *presentationv1.PresentationDocument) {
	t.Helper()
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	open := &openv1.OpenRecord{SourceId: source, OpenRef: presentation.Blocks[0].GetFields().Fields[0].Display, Data: packed, Presentation: presentation}
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
