package calcrawl

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	calendaropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/calendar/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
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
