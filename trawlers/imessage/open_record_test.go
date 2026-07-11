package imsgcrawl

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	imessageopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/imessage/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestOpenRecordProjection(t *testing.T) {
	input := archive.MessageContext{
		Chat:    archive.ChatSummary{Title: "Project Lantern", Kind: "group", ParticipantHandles: []string{"Avery Example", "+15550001111"}},
		Message: archive.MessageRow{MessageID: "42", Time: "2026-07-10T14:00:00Z", SenderLabel: "Avery Example", Text: "The synthetic pickup moved to Friday."},
		Before:  []archive.MessageRow{{MessageID: "41", Time: "2026-07-10T13:59:00Z", FromMe: true, Text: "That works."}},
		After:   []archive.MessageRow{{MessageID: "43", Time: "2026-07-10T14:01:00Z", SenderLabel: "Avery Example", HasAttachments: true}},
	}
	want := &imessageopenv1.IMessageRecord{
		Ref:     "imessage:msg/42",
		Chat:    &imessageopenv1.Chat{Name: "Project Lantern", Participants: []string{"Avery Example", "+15550001111"}},
		Message: &imessageopenv1.Message{Ref: "imessage:msg/42", Time: "2026-07-10T14:00:00Z", Who: "Avery Example", Where: "Project Lantern", Text: "The synthetic pickup moved to Friday."},
		Context: []*imessageopenv1.Message{
			{Ref: "imessage:msg/41", Time: "2026-07-10T13:59:00Z", Who: "me", Where: "Project Lantern", Text: "That works.", FromMe: true},
			{Ref: "imessage:msg/42", Time: "2026-07-10T14:00:00Z", Who: "Avery Example", Where: "Project Lantern", Text: "The synthetic pickup moved to Friday.", Target: testBool(true)},
			{Ref: "imessage:msg/43", Time: "2026-07-10T14:01:00Z", Who: "Avery Example", Where: "Project Lantern", HasAttachments: testBool(true)},
		},
	}
	assertOpenRecord(t, input, projectOpenRecord(input), want, "trawl.source.imessage.open.v1.IMessageRecord", `{"ref":"imessage:msg/42", "chat":{"name":"Project Lantern", "participants":["Avery Example", "+15550001111"]}, "message":{"ref":"imessage:msg/42", "time":"2026-07-10T14:00:00Z", "who":"Avery Example", "where":"Project Lantern", "text":"The synthetic pickup moved to Friday.", "from_me":false}, "context":[{"ref":"imessage:msg/41", "time":"2026-07-10T13:59:00Z", "who":"me", "where":"Project Lantern", "text":"That works.", "from_me":true}, {"ref":"imessage:msg/42", "time":"2026-07-10T14:00:00Z", "who":"Avery Example", "where":"Project Lantern", "text":"The synthetic pickup moved to Friday.", "from_me":false, "target":true}, {"ref":"imessage:msg/43", "time":"2026-07-10T14:01:00Z", "who":"Avery Example", "where":"Project Lantern", "text":"", "from_me":false, "has_attachments":true}]}`)
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
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	var wantCompact bytes.Buffer
	if err := json.Compact(&wantCompact, []byte(wantJSON)); err != nil {
		t.Fatal(err)
	}
	if compact.String() != wantCompact.String() {
		t.Fatalf("ProtoJSON = %s\nwant = %s", data, wantJSON)
	}
	name := string(got.ProtoReflect().Descriptor().FullName())
	if name != wantName {
		t.Fatalf("message name = %q, want %q", name, wantName)
	}
	if typeURL := "type.opentrawl.org/" + name; typeURL != "type.opentrawl.org/"+wantName {
		t.Fatalf("type URL = %q", typeURL)
	}
}

func testBool(value bool) *bool { return &value }
