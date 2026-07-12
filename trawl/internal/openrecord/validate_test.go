package openrecord

import (
	"os"
	"path/filepath"
	"testing"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	imessageopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/imessage/open/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestOpenRecordValidatorAcceptsCompleteRecord(t *testing.T) {
	record, _, _, _ := openRecordFixture(t)
	if err := Validate(record); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecordPinsCompleteProtobufText(t *testing.T) {
	packed, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	record := &openv1.OpenRecord{
		SourceId: "notes",
		OpenRef:  "notes:note/example-1",
		Data:     packed,
		Presentation: &presentationv1.PresentationDocument{
			Title: "Synthetic note",
		},
	}
	want := "" +
		"source_id:  \"notes\"\n" +
		"open_ref:  \"notes:note/example-1\"\n" +
		"data:  {\n" +
		"  [type.googleapis.com/google.protobuf.Empty]:  {}\n" +
		"}\n" +
		"presentation:  {\n" +
		"  title:  \"Synthetic note\"\n" +
		"}\n"
	if got := prototext.Format(record); got != want {
		t.Fatalf("open record protobuf text changed\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestOpenRecordValidatorRejectsEveryUnsafeBoundary(t *testing.T) {
	valid, _, _, _ := openRecordFixture(t)
	tests := []struct {
		name   string
		mutate func(*openv1.OpenRecord)
	}{
		{name: "empty source", mutate: func(record *openv1.OpenRecord) { record.SourceId = "" }},
		{name: "empty open ref", mutate: func(record *openv1.OpenRecord) { record.OpenRef = "" }},
		{name: "foreign open ref", mutate: func(record *openv1.OpenRecord) { record.OpenRef = "notes:note/example" }},
		{name: "missing data", mutate: func(record *openv1.OpenRecord) { record.Data = nil }},
		{name: "missing data type", mutate: func(record *openv1.OpenRecord) { record.Data.TypeUrl = "" }},
		{name: "missing presentation", mutate: func(record *openv1.OpenRecord) { record.Presentation = nil }},
		{name: "foreign resource", mutate: func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].GetResource().Ref = "photos:asset/example"
		}},
		{name: "foreign action ref", mutate: func(record *openv1.OpenRecord) {
			record.Presentation.Actions[0].Target = &presentationv1.Action_OpenRef{OpenRef: "notes:note/example"}
		}},
		{name: "insecure url", mutate: func(record *openv1.OpenRecord) {
			record.Presentation.Actions[1].Target = &presentationv1.Action_Url{Url: "http://example.com/archive"}
		}},
		{name: "relative url", mutate: func(record *openv1.OpenRecord) {
			record.Presentation.Actions[1].Target = &presentationv1.Action_Url{Url: "/archive"}
		}},
		{name: "missing action target", mutate: func(record *openv1.OpenRecord) {
			record.Presentation.Actions[1].Target = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := proto.Clone(valid).(*openv1.OpenRecord)
			test.mutate(record)
			if err := Validate(record); err == nil {
				t.Fatal("invalid open record was accepted")
			}
		})
	}
}

func TestOpenBoundaryEvidence(t *testing.T) {
	record, machine, packed, presentation := openRecordFixture(t)
	if err := Validate(record); err != nil {
		t.Fatal(err)
	}
	decoded := new(imessageopenv1.IMessageRecord)
	if err := packed.UnmarshalTo(decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(machine, decoded) {
		t.Fatal("packed machine record changed")
	}
	writeOpenEvidence(t, "open-machine.pbtxt", []byte(prototext.Format(machine)))
	writeOpenEvidence(t, "open-any.pbtxt", rawAnyText(t, packed))
	writeOpenEvidence(t, "open-presentation.pbtxt", []byte(prototext.Format(presentation)))
	writeOpenEvidence(t, "open-record.pbtxt", []byte(prototext.Format(record)))
}

func rawAnyText(t *testing.T, packed *anypb.Any) []byte {
	t.Helper()
	options := prototext.MarshalOptions{Multiline: true, Indent: "  ", Resolver: new(protoregistry.Types)}
	content, err := options.Marshal(packed)
	if err != nil {
		t.Fatal(err)
	}
	return append(content, '\n')
}

func openRecordFixture(t *testing.T) (*openv1.OpenRecord, *imessageopenv1.IMessageRecord, *anypb.Any, *presentationv1.PresentationDocument) {
	t.Helper()
	target := true
	machine := &imessageopenv1.IMessageRecord{
		Ref:  "imessage:msg/example-2",
		Chat: &imessageopenv1.Chat{Name: "Launch group", Participants: []string{"Casey Example", "Avery Example"}},
		Message: &imessageopenv1.Message{
			Ref: "imessage:msg/example-2", Time: "2026-07-12T09:00:00+02:00", Who: "me", Where: "Launch group", Text: "Synthetic launch note", FromMe: true, Target: &target,
		},
		Context: []*imessageopenv1.Message{{
			Ref: "imessage:msg/example-3", Time: "2026-07-12T09:01:00+02:00", Who: "Casey Example", Where: "Launch group", Text: "Synthetic launch reply",
		}},
	}
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	presentation := &presentationv1.PresentationDocument{
		Title: "Launch group",
		Blocks: []*presentationv1.Block{{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{
			Kind: presentationv1.Resource_KIND_FILE, Label: "Synthetic attachment", Ref: "imessage:attachment/example-1",
			Metadata: []*presentationv1.Field{{Label: "Type", Display: "text/plain"}},
		}}}},
		Actions: []*presentationv1.Action{
			{Label: "Open message", Target: &presentationv1.Action_OpenRef{OpenRef: "imessage:msg/example-2"}},
			{Label: "Open source page", Target: &presentationv1.Action_Url{Url: "https://example.com/archive"}},
		},
		Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_PROVENANCE, Message: "Synthetic source record."}},
	}
	record := &openv1.OpenRecord{SourceId: "imessage", OpenRef: "imessage:msg/example-2", Data: packed, Presentation: presentation}
	return record, machine, packed, presentation
}

func writeOpenEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	if len(content) == 0 {
		t.Fatalf("evidence %s is empty", name)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != string(content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}
