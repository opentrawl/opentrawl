package gogcrawl

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	gmailopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/gmail/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
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
