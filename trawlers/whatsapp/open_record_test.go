package wacrawl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	whatsappopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/whatsapp/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previousLocal })
	target := store.Message{SourcePK: 2, ChatName: "Lantern group", MessageID: "m2", SenderName: "Avery Example", Timestamp: time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC), MediaType: "image", MediaTitle: "fixture.jpg", MediaSize: 2048, Starred: true}
	context := []store.Message{
		{SourcePK: 1, ChatName: "Lantern group", MessageID: "m1", FromMe: true, Timestamp: time.Date(2026, 7, 10, 13, 59, 0, 0, time.UTC), Text: "Sent."},
		target,
		{SourcePK: 3, ChatName: "Lantern group", MessageID: "m3", SenderName: "Avery Example", Timestamp: time.Date(2026, 7, 10, 14, 1, 0, 0, time.UTC), Text: "Received."},
	}
	input := struct {
		Target       store.Message   `json:"target"`
		Context      []store.Message `json:"context"`
		Participants []string        `json:"participants"`
	}{target, context, []string{"Avery Example", "Morgan Example"}}
	want := &whatsappopenv1.WhatsAppRecord{
		Ref: "whatsapp:msg/m2", Chat: "Lantern group", Participants: input.Participants,
		Message: &whatsappopenv1.Message{Ref: "whatsapp:msg/m2", Time: "2026-07-10T14:00:00Z", Who: "Avery Example", Where: "Lantern group", Text: "[image]", Type: testString("image"), Media: &whatsappopenv1.Media{Type: testString("image"), Title: testString("fixture.jpg"), SizeBytes: testInt64(2048)}, Starred: testBool(true)},
		Context: []*whatsappopenv1.Message{
			{Ref: "whatsapp:msg/m1", Time: "2026-07-10T13:59:00Z", Who: "me", Where: "Lantern group", Text: "Sent.", Type: testString("text")},
			{Ref: "whatsapp:msg/m2", Time: "2026-07-10T14:00:00Z", Who: "Avery Example", Where: "Lantern group", Text: "[image]", Type: testString("image"), Media: &whatsappopenv1.Media{Type: testString("image"), Title: testString("fixture.jpg"), SizeBytes: testInt64(2048)}, Starred: testBool(true), Current: testBool(true)},
			{Ref: "whatsapp:msg/m3", Time: "2026-07-10T14:01:00Z", Who: "Avery Example", Where: "Lantern group", Text: "Received.", Type: testString("text")},
		}, Window: &whatsappopenv1.Window{Before: 1, After: 1},
	}
	assertOpenRecord(t, input, projectOpenRecord(target, context, input.Participants), want, "trawl.source.whatsapp.open.v1.WhatsAppRecord", `{"ref":"whatsapp:msg/m2","chat":"Lantern group","participants":["Avery Example","Morgan Example"],"message":{"ref":"whatsapp:msg/m2","time":"2026-07-10T14:00:00Z","who":"Avery Example","where":"Lantern group","text":"[image]","type":"image","media":{"type":"image","title":"fixture.jpg","size_bytes":"2048"},"starred":true},"context":[{"ref":"whatsapp:msg/m1","time":"2026-07-10T13:59:00Z","who":"me","where":"Lantern group","text":"Sent.","type":"text"},{"ref":"whatsapp:msg/m2","time":"2026-07-10T14:00:00Z","who":"Avery Example","where":"Lantern group","text":"[image]","type":"image","media":{"type":"image","title":"fixture.jpg","size_bytes":"2048"},"starred":true,"current":true},{"ref":"whatsapp:msg/m3","time":"2026-07-10T14:01:00Z","who":"Avery Example","where":"Lantern group","text":"Received.","type":"text"}],"window":{"before":1,"after":1}}`)
	presentation := projectOpenPresentation(target, context, input.Participants)
	if presentation.Title != "Lantern group" || len(presentation.Blocks) != 4 || len(presentation.Blocks[2].GetTable().Rows) != 3 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Lantern group"
blocks: { fields: { fields: { label: "Ref" display: "whatsapp:msg/m2" } fields: { label: "Participants" display: "Avery Example, Morgan Example" } } }
blocks: { prose: { text: "[image]" } }
blocks: { table: { columns: "Time" columns: "From" columns: "Text" rows: { role: ROLE_NORMAL cells: { display: "2026-07-10T13:59:00Z" } cells: { display: "me" } cells: { display: "Sent." } } rows: { role: ROLE_TARGET cells: { display: "2026-07-10T14:00:00Z" } cells: { display: "Avery Example" } cells: { display: "[image]" } } rows: { role: ROLE_NORMAL cells: { display: "2026-07-10T14:01:00Z" } cells: { display: "Avery Example" } cells: { display: "Received." } } } }
blocks: { fields: { fields: { label: "Media type" display: "image" } fields: { label: "Media title" display: "fixture.jpg" } fields: { label: "Media size" display: "2048 bytes" } } }`)
	assertOpenPresentation(t, "whatsapp", input, projectOpenRecord(target, context, input.Participants), presentation)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := target
		blank.ChatName = ""
		if got := projectOpenPresentation(blank, []store.Message{blank}, nil).Title; got != "WhatsApp conversation" {
			t.Fatalf("title = %q", got)
		}
	})
}

func assertOpenRecord(t *testing.T, input any, got, want proto.Message, wantName, wantJSON string) {
	t.Helper()
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	if !proto.Equal(got, want) {
		t.Fatalf("record = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
	text := prototext.Format(got)
	t.Logf("protobuf text:\n%s", text)
	if text != prototext.Format(want) {
		t.Fatal("protobuf text changed")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	var actualCompact, wantCompact bytes.Buffer
	if err := json.Compact(&actualCompact, data); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&wantCompact, []byte(wantJSON)); err != nil {
		t.Fatal(err)
	}
	if actualCompact.String() != wantCompact.String() {
		t.Fatalf("ProtoJSON = %s\nwant = %s", data, wantJSON)
	}
	name := string(got.ProtoReflect().Descriptor().FullName())
	if name != wantName {
		t.Fatalf("message name = %q, want %q", name, wantName)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/"+wantName {
		t.Fatal("type URL changed")
	}
}

func testString(value string) *string { return &value }
func testInt64(value int64) *int64    { return &value }
func testBool(value bool) *bool       { return &value }

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
