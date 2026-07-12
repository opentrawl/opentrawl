package calcrawl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	calendaropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/calendar/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	availability := int64(1)
	input := archive.EventDetail{
		Ref: "calendar:event/event-1", UUID: "event-1", UniqueIdentifier: "provider-event-1", Title: "Synthetic planning", Description: "Review the fixture.", DescriptionTruncated: true,
		Start: "2026-07-10T14:00:00+02:00", End: "2026-07-10T15:00:00+02:00", Calendar: "Projects", Account: "example.com", Availability: &availability,
		Location: &archive.Location{Title: "Example room", Address: "1 Example Street"}, Organizer: archive.Person{DisplayName: "Avery Example", Email: "avery@example.com"},
		Attendees: []archive.Attendee{{DisplayName: "Morgan Example", Email: "morgan@example.com", RSVPStatus: "accepted", Role: "required", Self: true, Comment: "Synthetic"}},
		URL:       "https://example.com/event", Status: "confirmed", HasRecurrences: true,
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	record := projectOpenRecord(input)
	name := string(record.ProtoReflect().Descriptor().FullName())
	if name != "trawl.source.calendar.open.v1.CalendarRecord" {
		t.Fatalf("message name = %q", name)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/trawl.source.calendar.open.v1.CalendarRecord" {
		t.Fatal("type URL changed")
	}
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["start"] != input.Start || decoded["end"] != input.End {
		t.Fatalf("offset changed: %s", data)
	}
	if decoded["availability"] != "1" {
		t.Fatalf("availability changed: %s", data)
	}
	assertExactRecord(t, record, &calendaropenv1.CalendarRecord{}, `{"ref":"calendar:event/event-1","uuid":"event-1","unique_identifier":"provider-event-1","title":"Synthetic planning","description":"Review the fixture.","description_truncated":true,"start":"2026-07-10T14:00:00+02:00","end":"2026-07-10T15:00:00+02:00","all_day":false,"calendar":"Projects","account":"example.com","availability":"1","location":{"title":"Example room","address":"1 Example Street"},"organizer":{"display_name":"Avery Example","email":"avery@example.com"},"attendees":[{"display_name":"Morgan Example","email":"morgan@example.com","rsvp_status":"accepted","role":"required","self":true,"comment":"Synthetic"}],"url":"https://example.com/event","status":"confirmed","has_recurrences":true}`)
	presentation := projectOpenPresentation(input)
	if presentation.Title != "Synthetic planning" || len(presentation.Blocks) != 2 || len(presentation.Actions) != 1 || len(presentation.Facts) != 1 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Synthetic planning"
blocks: { fields: { fields: { label: "Ref" display: "calendar:event/event-1" } fields: { label: "Start" display: "2026-07-10T14:00:00+02:00" } fields: { label: "End" display: "2026-07-10T15:00:00+02:00" } fields: { label: "All day" display: "No" } fields: { label: "Calendar" display: "Projects" } fields: { label: "Account" display: "example.com" } fields: { label: "Availability" display: "1" } fields: { label: "Location" display: "Example room, 1 Example Street" } fields: { label: "Organizer" display: "Avery Example" } fields: { label: "Attendees" display: "Morgan Example (accepted)" } fields: { label: "URL" display: "https://example.com/event" } fields: { label: "Status" display: "confirmed" } fields: { label: "Recurring" display: "Yes" } } }
blocks: { prose: { text: "Review the fixture." } }
actions: { label: "Open event link" url: "https://example.com/event" }
facts: { kind: KIND_TRUNCATION message: "Event description is truncated." }`)
	assertOpenPresentation(t, "calendar", input, record, presentation)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Title = ""
		if got := projectOpenPresentation(blank).Title; got != "Calendar event" {
			t.Fatalf("title = %q", got)
		}
	})
	t.Run("trims_https_action", func(t *testing.T) {
		trimmed := input
		trimmed.URL = " https://example.com/event "
		if got := projectOpenPresentation(trimmed).Actions[0].GetUrl(); got != "https://example.com/event" {
			t.Fatalf("action URL = %q", got)
		}
	})
}

func assertExactRecord(t *testing.T, got, want proto.Message, wantJSON string) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(wantJSON), want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("record = %s\nwant = %s", prototext.Format(got), prototext.Format(want))
	}
	if prototext.Format(got) != prototext.Format(want) {
		t.Fatal("protobuf text changed")
	}
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(got)
	if err != nil {
		t.Fatal(err)
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
