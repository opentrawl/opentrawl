package calcrawl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	calendaropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/calendar/open/v1"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
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
	if presentation.Title != "Synthetic planning" || len(presentation.Blocks) != 3 || len(presentation.Actions) != 1 || len(presentation.Facts) != 1 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertExactPresentation(t, presentation, `title: "Synthetic planning"
blocks: { heading: { text: "Synthetic planning" } anchor_id: "summary" }
blocks: { fields: { fields: { label: "Start" display: "10 July 2026 at 14:00" } fields: { label: "End" display: "10 July 2026 at 15:00" } fields: { label: "All day" display: "No" } fields: { label: "Calendar" display: "Projects" } fields: { label: "Account" display: "example.com" } fields: { label: "Availability" display: "Free" } fields: { label: "Location" display: "Example room, 1 Example Street" anchor_id: "location" } fields: { label: "Organizer" display: "Avery Example" } fields: { label: "Attendees" display: "Morgan Example (accepted)" anchor_id: "participant" } fields: { label: "URL" display: "https://example.com/event" } fields: { label: "Status" display: "confirmed" } fields: { label: "Recurring" display: "Yes" } } }
blocks: { prose: { text: "Review the fixture." } anchor_id: "description" }
actions: { label: "Open event link" url: "https://example.com/event" }
facts: { kind: KIND_TRUNCATION message: "Event description is truncated." }
primary_anchor_id: "summary"`)
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
	t.Run("renders_EventKit_availability_labels", func(t *testing.T) {
		for _, tc := range []struct {
			raw   int64
			label string
		}{
			{raw: -1, label: "Not supported"},
			{raw: 0, label: "Busy"},
			{raw: 1, label: "Free"},
			{raw: 2, label: "Tentative"},
			{raw: 3, label: "Unavailable"},
			{raw: 99, label: "Unknown"},
		} {
			t.Run(tc.label, func(t *testing.T) {
				fixture := input
				fixture.Availability = &tc.raw
				if got := projectOpenRecord(fixture).GetAvailability(); got != tc.raw {
					t.Fatalf("typed availability = %d, want %d", got, tc.raw)
				}
				for _, field := range projectOpenPresentation(fixture).Blocks[0].GetFields().GetFields() {
					if field.GetLabel() == "Availability" && field.GetDisplay() != tc.label {
						t.Fatalf("availability display = %q, want %q", field.GetDisplay(), tc.label)
					}
				}
			})
		}
	})
}

func TestCalendarPresentationContainsDescriptionAndParticipantSearchAnchors(t *testing.T) {
	value := archive.EventDetail{Ref: "calendar:event/example", UUID: "example", Title: "Lantern event", Description: "Lantern description", Attendees: []archive.Attendee{{DisplayName: "Lantern participant"}}}
	record := &openv1.OpenRecord{SourceId: "calendar", OpenRef: value.Ref, Data: &anypb.Any{TypeUrl: "type.example/calendar"}, Presentation: projectOpenPresentation(value)}
	for _, anchorID := range []string{"summary", "description", "participant"} {
		if err := openrecord.ValidateRequestedAnchor(record, anchorID); err != nil {
			t.Fatalf("anchor %q: %v", anchorID, err)
		}
	}
}

func TestOpenRecordTimestampBoundary(t *testing.T) {
	if err := validateOpenTimestamps(archive.EventDetail{Start: "2026-07-10T14:00:00.5+02:00"}); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenTimestamps(archive.EventDetail{Start: "bad timestamp"}); err == nil {
		t.Fatal("accepted malformed start")
	}
	if err := validateOpenTimestamps(archive.EventDetail{}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecordFixtureBoundary(t *testing.T) {
	ctx := context.Background()
	_, paths := syncedCalendarFixture(t)
	ref := "calendar:event/11111111-1111-1111-1111-111111111111"
	setStart := func(value string) {
		store, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.DB().ExecContext(ctx, `update events set start_time = ? where event_uid = ?`, value, "11111111-1111-1111-1111-111111111111")
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	setStart("2026-07-10T14:00:00.5+02:00")
	readStore := openReadStore(t, ctx, paths.Archive)
	record, err := New().OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := machine.(*calendaropenv1.CalendarRecord)
	if !ok {
		t.Fatalf("typed record = %T", machine)
	}
	if typed.GetStart() != "2026-07-10T14:00:00.5+02:00" {
		t.Fatalf("typed start = %q", typed.GetStart())
	}
	if got := record.Presentation.Blocks[1].GetFields().GetFields()[0].GetDisplay(); got != "10 July 2026 at 14:00" {
		t.Fatalf("start display = %q", got)
	}
	setStart("not-a-timestamp")
	readStore = openReadStore(t, ctx, paths.Archive)
	_, err = New().OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
	_ = readStore.Close()
	if err == nil {
		t.Fatal("accepted malformed archive timestamp")
	}
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

func assertOpenPresentation(t *testing.T, source string, input any, machine interface {
	proto.Message
	GetRef() string
}, presentation *presentationv1.PresentationDocument) {
	t.Helper()
	packed, err := anypb.New(machine)
	if err != nil {
		t.Fatal(err)
	}
	open := &openv1.OpenRecord{SourceId: source, OpenRef: machine.GetRef(), Data: packed, Presentation: presentation}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	readBack, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(readBack, content) {
		t.Fatalf("evidence %s changed on write", name)
	}
}

func writeRuntimeOpenEvidence(t *testing.T, source, caseName, ref string, loaded any, record *openv1.OpenRecord) {
	t.Helper()
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "argv-ref.txt"), []byte("OpenRecord "+ref+"\n"))
	loadedJSON, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, source, filepath.Join(caseName, "loaded-value.json"), append(loadedJSON, '\n'))
	writeEvidence(t, source, filepath.Join(caseName, "machine.pbtxt"), []byte(prototext.Format(machine)))
	writeEvidence(t, source, filepath.Join(caseName, "presentation.pbtxt"), []byte(prototext.Format(record.Presentation)))
	writeEvidence(t, source, filepath.Join(caseName, "validated-open.pbtxt"), []byte(prototext.Format(record)))
}

func writeLegacyOpenEvidence(t *testing.T, source, caseName, format string, stdout []byte, err error) {
	t.Helper()
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stdout.txt"), stdout)
	writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-stderr.txt"), nil)
	if err == nil {
		writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("0\n"))
		writeRawEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), nil)
		return
	}
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-exit.txt"), []byte("1\n"))
	writeEvidence(t, source, filepath.Join(caseName, "open-"+format+"-error.txt"), []byte(err.Error()+"\n"))
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

func writeRawEvidence(t *testing.T, source, name string, content []byte) {
	t.Helper()
	directory := os.Getenv("OPENTRAWL_EVIDENCE_DIR")
	if directory == "" {
		return
	}
	directory = filepath.Join(directory, source)
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
