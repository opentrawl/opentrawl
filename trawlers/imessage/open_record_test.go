package imsgcrawl

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	imessageopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/imessage/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
	presentation := projectOpenPresentation(input)
	wantPresentation := &presentationv1.PresentationDocument{Title: "Project Lantern", Blocks: []*presentationv1.Block{
		{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{{Label: "Participants", Display: "Avery Example, +15550001111"}}}}},
		{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: "The synthetic pickup moved to Friday."}}},
		{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Time", "From", "Text"}, Rows: []*presentationv1.Row{{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{Display: "10 July 2026 at 13:59"}, {Display: "me"}, {Display: "That works."}}}, {Role: presentationv1.Row_ROLE_TARGET, Cells: []*presentationv1.Cell{{Display: "10 July 2026 at 14:00"}, {Display: "Avery Example"}, {Display: "The synthetic pickup moved to Friday."}}, AnchorId: trawlkit.MatchAnchorID}, {Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{Display: "10 July 2026 at 14:01"}, {Display: "Avery Example"}, {Display: ""}}}}}}},
	}, PrimaryAnchorId: trawlkit.MatchAnchorID}
	assertOpenPresentation(t, input, projectOpenRecord(input), presentation, wantPresentation, "imessage")
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Chat = archive.ChatSummary{}
		if got := projectOpenPresentation(blank).Title; got != "Conversation" {
			t.Fatalf("title = %q", got)
		}
	})
}

func TestOpenRecordTimestampBoundary(t *testing.T) {
	if err := validateOpenTimestamps(archive.MessageContext{Message: archive.MessageRow{Time: "2026-07-10T14:00:00.5+02:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenTimestamps(archive.MessageContext{Message: archive.MessageRow{Time: "bad timestamp"}}); err == nil {
		t.Fatal("accepted malformed message time")
	}
	if err := validateOpenTimestamps(archive.MessageContext{}); err != nil {
		t.Fatal(err)
	}
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

func assertOpenPresentation(t *testing.T, input any, machine interface {
	proto.Message
	GetRef() string
}, got, want *presentationv1.PresentationDocument, sourceID string) {
	t.Helper()
	if !proto.Equal(got, want) {
		t.Fatalf("presentation = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	open := &openv1.OpenRecord{SourceId: sourceID, OpenRef: machine.GetRef(), Data: packed, Presentation: got}
	if err := openrecord.Validate(open); err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, "input.json", mustJSON(t, input))
	writeEvidence(t, "record.pbtxt", []byte(prototext.Format(machine)))
	writeEvidence(t, "presentation.pbtxt", []byte(prototext.Format(got)))
	writeEvidence(t, "validated-open.pbtxt", []byte(prototext.Format(open)))
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	directory = filepath.Join(directory, "imessage")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func writeRuntimeOpenEvidence(t *testing.T, caseName, ref string, loaded any, record *openv1.OpenRecord) {
	t.Helper()
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, filepath.Join(caseName, "argv-ref.txt"), []byte("OpenRecord "+ref+"\n"))
	loadedJSON, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, filepath.Join(caseName, "loaded-value.json"), append(loadedJSON, '\n'))
	writeEvidence(t, filepath.Join(caseName, "machine.pbtxt"), []byte(prototext.Format(machine)))
	writeEvidence(t, filepath.Join(caseName, "presentation.pbtxt"), []byte(prototext.Format(record.Presentation)))
	writeEvidence(t, filepath.Join(caseName, "validated-open.pbtxt"), []byte(prototext.Format(record)))
}

func writeLegacyOpenEvidence(t *testing.T, caseName, format string, stdout []byte, err error) {
	t.Helper()
	writeEvidence(t, filepath.Join(caseName, "open-"+format+"-stdout.txt"), stdout)
	writeRawEvidence(t, filepath.Join(caseName, "open-"+format+"-stderr.txt"), nil)
	if err == nil {
		writeEvidence(t, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("0\n"))
		writeRawEvidence(t, filepath.Join(caseName, "open-"+format+"-error.txt"), nil)
		return
	}
	writeEvidence(t, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("1\n"))
	writeEvidence(t, filepath.Join(caseName, "open-"+format+"-error.txt"), []byte(err.Error()+"\n"))
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

func writeRawEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	directory = filepath.Join(directory, "imessage")
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
