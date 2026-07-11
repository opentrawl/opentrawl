package wacrawl

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	whatsappopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/whatsapp/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
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
