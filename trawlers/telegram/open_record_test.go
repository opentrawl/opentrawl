package telecrawl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	telegramopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/telegram/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previousLocal })
	before := store.Message{SourcePK: 40, MessageID: "provider-40", ChatJID: "chat-7", ChatName: "Lantern", SenderJID: "peer-1", SenderName: "Morgan Example", Timestamp: time.Date(2026, 7, 10, 13, 59, 0, 0, time.UTC), Text: "Before"}
	target := store.Message{SourcePK: 41, MessageID: "provider-41", ChatJID: "chat-7", ChatName: "Lantern", SenderJID: "peer-2", SenderName: "Avery Example", Timestamp: time.Date(2026, 7, 10, 14, 0, 0, 0, time.UTC), EditTime: time.Date(2026, 7, 10, 14, 2, 0, 0, time.UTC), Text: "Target", MessageType: "photo", MediaType: "image", MediaTitle: "fixture", MediaURL: "https://example.com/fixture", MediaSize: 2048, MetadataType: "link", MetadataTitle: "Example", MetadataURL: "https://example.com", Starred: true, ReplyToID: "provider-40", ReplyToChat: "chat-7", Views: 12, Forwards: 2, RepliesCount: 1, Pinned: true}
	unavailable := store.Message{SourcePK: 42, MessageID: "provider-42", ChatJID: "chat-7", ChatName: "Lantern", SenderJID: "opaque-peer", Timestamp: time.Date(2026, 7, 10, 14, 1, 0, 0, time.UTC), Text: "After"}
	directWithoutSender := store.Message{SourcePK: 43, MessageID: "provider-43", ChatJID: "direct-7", ChatName: "Direct chat", SenderJID: "direct-7", Timestamp: time.Date(2026, 7, 10, 14, 2, 0, 0, time.UTC), Text: "No exported sender"}
	input := store.MessageWindow{Target: target, Messages: []store.Message{before, target, unavailable, directWithoutSender}, Participants: []string{"Avery Example", "Morgan Example"}, BeforeTruncated: true, AfterTruncated: false}
	got := projectOpenRecord(input)
	if got.Message.GetReplyToMessageRef() != "telegram:msg/40" {
		t.Fatalf("reply ref = %q", got.Message.GetReplyToMessageRef())
	}
	if got.Context[2].Sender.State != telegramopenv1.SenderState_SENDER_STATE_UNAVAILABLE || got.Context[2].Sender.DisplayName != nil {
		t.Fatalf("unavailable sender = %#v", got.Context[2].Sender)
	}
	if got.Context[3].Sender.State != telegramopenv1.SenderState_SENDER_STATE_UNAVAILABLE || got.Context[3].Sender.DisplayName != nil {
		t.Fatalf("direct chat without exported sender = %#v", got.Context[3].Sender)
	}
	assertOpenRecord(t, input, got, "trawl.source.telegram.open.v1.TelegramRecord", `{"ref":"telegram:msg/41","chat":{"ref":"telegram:chat/chat-7","name":"Lantern"},"participants":["Avery Example","Morgan Example"],"message":{"ref":"telegram:msg/41","is_target":true,"time":"2026-07-10T14:00:00Z","edit_time":"2026-07-10T14:02:00Z","chat":{"ref":"telegram:chat/chat-7","name":"Lantern"},"sender":{"ref":"telegram:chat/peer-2","display_name":"Avery Example","state":"SENDER_STATE_AVAILABLE"},"from_me":false,"text":"Target","message_type":"photo","media":{"type":"image","title":"fixture","url":"https://example.com/fixture","size_bytes":"2048"},"metadata":{"type":"link","title":"Example","url":"https://example.com"},"starred":true,"reply_to_message_ref":"telegram:msg/40","reply_to_chat_ref":"telegram:chat/chat-7","views":12,"forwards":2,"replies_count":1,"pinned":true},"context":[{"ref":"telegram:msg/40","time":"2026-07-10T13:59:00Z","chat":{"ref":"telegram:chat/chat-7","name":"Lantern"},"sender":{"ref":"telegram:chat/peer-1","display_name":"Morgan Example","state":"SENDER_STATE_AVAILABLE"},"from_me":false,"text":"Before"},{"ref":"telegram:msg/41","is_target":true,"time":"2026-07-10T14:00:00Z","edit_time":"2026-07-10T14:02:00Z","chat":{"ref":"telegram:chat/chat-7","name":"Lantern"},"sender":{"ref":"telegram:chat/peer-2","display_name":"Avery Example","state":"SENDER_STATE_AVAILABLE"},"from_me":false,"text":"Target","message_type":"photo","media":{"type":"image","title":"fixture","url":"https://example.com/fixture","size_bytes":"2048"},"metadata":{"type":"link","title":"Example","url":"https://example.com"},"starred":true,"reply_to_message_ref":"telegram:msg/40","reply_to_chat_ref":"telegram:chat/chat-7","views":12,"forwards":2,"replies_count":1,"pinned":true},{"ref":"telegram:msg/42","time":"2026-07-10T14:01:00Z","chat":{"ref":"telegram:chat/chat-7","name":"Lantern"},"sender":{"ref":"telegram:chat/opaque-peer","state":"SENDER_STATE_UNAVAILABLE"},"from_me":false,"text":"After"},{"ref":"telegram:msg/43","time":"2026-07-10T14:02:00Z","chat":{"ref":"telegram:chat/direct-7","name":"Direct chat"},"sender":{"ref":"telegram:chat/direct-7","state":"SENDER_STATE_UNAVAILABLE"},"from_me":false,"text":"No exported sender"}],"context_window":{"before":1,"after":2,"before_truncated":true,"after_truncated":false},"target_position":1}`, []string{"source_pk", "message_id", "raw_type", "media_path", "metadata_json", "forward_json", "reactions_json"})
	presentation := projectOpenPresentation(input)
	if presentation.Title != "Lantern" || len(presentation.Blocks) != 3 || len(presentation.Actions) != 2 || len(presentation.Facts) != 1 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Lantern"
blocks: { fields: { fields: { label: "Ref" display: "telegram:msg/41" } fields: { label: "Participants" display: "Avery Example, Morgan Example" } } }
blocks: { prose: { text: "Target" } }
blocks: { table: { columns: "Time" columns: "From" columns: "Text" rows: { role: ROLE_NORMAL cells: { display: "2026-07-10T13:59:00Z" } cells: { display: "Morgan Example" } cells: { display: "Before" } } rows: { role: ROLE_TARGET cells: { display: "2026-07-10T14:00:00Z" } cells: { display: "Avery Example" } cells: { display: "Target" } } rows: { role: ROLE_NORMAL cells: { display: "2026-07-10T14:01:00Z" } cells: { display: "Unavailable" } cells: { display: "After" } } rows: { role: ROLE_NORMAL cells: { display: "2026-07-10T14:02:00Z" } cells: { display: "Unavailable" } cells: { display: "No exported sender" } } } }
actions: { label: "Open media link" url: "https://example.com/fixture" }
actions: { label: "Open metadata link" url: "https://example.com" }
facts: { kind: KIND_TRUNCATION message: "Earlier context is truncated." }`)
	assertOpenPresentation(t, "telegram", input, got, presentation)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Target.ChatName = ""
		if got := projectOpenPresentation(blank).Title; got != "Telegram conversation" {
			t.Fatalf("title = %q", got)
		}
	})
}

func assertOpenRecord(t *testing.T, input any, got proto.Message, wantName, wantJSON string, forbidden []string) {
	t.Helper()
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	text := prototext.Format(got)
	t.Logf("protobuf text:\n%s", text)
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty protobuf text")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	want := &telegramopenv1.TelegramRecord{}
	if err := protojson.Unmarshal([]byte(wantJSON), want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("record = %s\nwant = %s", text, prototext.Format(want))
	}
	if text != prototext.Format(want) {
		t.Fatal("protobuf text changed")
	}
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
	for _, field := range forbidden {
		if strings.Contains(string(data), field) || strings.Contains(text, field) {
			t.Fatalf("storage field %q leaked", field)
		}
	}
	name := string(got.ProtoReflect().Descriptor().FullName())
	if name != wantName {
		t.Fatalf("message name = %q, want %q", name, wantName)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/"+wantName {
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
