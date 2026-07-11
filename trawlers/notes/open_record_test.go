package notes

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	notesopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/notes/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestOpenRecordProjection(t *testing.T) {
	note := archive.Note{ID: "NOTE-FIXTURE", Title: "Packing list", Folder: "Examples", CreatedAt: "2026-07-08T10:00:00Z", ModifiedAt: "2026-07-10T14:00:00Z", LastSeenAt: "private-sync-time", VersionCount: 3}
	body := archive.VersionBody{Version: archive.Version{Ref: "notes:version/NOTE-FIXTURE/abc123", NoteID: note.ID, SHA256: "private-full-hash", ShortSHA: "abc123", ZDataBytes: 1024, TextStatus: "decoded", SourceModifiedAt: "private-source-time", FirstObservedAt: "private-first", LatestObservedAt: "private-latest", Source: "private-source", SourceDetail: "private-detail", SourceSequence: 7}, Title: note.Title, Folder: note.Folder, Text: "Passport, charger and synthetic train ticket.", ZData: []byte("private-compressed")}
	input := struct {
		RequestedRef string              `json:"requested_ref"`
		Note         archive.Note        `json:"note"`
		Body         archive.VersionBody `json:"body"`
	}{"notes:version/NOTE-FIXTURE/abc123", note, body}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	record := projectOpenRecord(input.RequestedRef, note, body)
	if record.Ref != input.RequestedRef || record.VersionRef != body.Ref {
		t.Fatalf("refs = %q %q", record.Ref, record.VersionRef)
	}
	name := string(record.ProtoReflect().Descriptor().FullName())
	if name != "trawl.source.notes.open.v1.NotesRecord" {
		t.Fatalf("message name = %q", name)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/trawl.source.notes.open.v1.NotesRecord" {
		t.Fatal("type URL changed")
	}
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	for _, forbidden := range []string{"private-full-hash", "private-sync-time", "private-source-time", "private-first", "private-latest", "private-source", "private-detail", "private-compressed", "zdata", "note_id", "source_sequence"} {
		if strings.Contains(string(data), forbidden) || strings.Contains(text, forbidden) {
			t.Fatalf("sync field leaked %q", forbidden)
		}
	}
	assertExactRecord(t, record, &notesopenv1.NotesRecord{}, `{"ref":"notes:version/NOTE-FIXTURE/abc123","version_ref":"notes:version/NOTE-FIXTURE/abc123","title":"Packing list","folder":"Examples","created_at":"2026-07-08T10:00:00Z","modified_at":"2026-07-10T14:00:00Z","version_count":"3","text_state":"TEXT_STATE_DECODED","text":"Passport, charger and synthetic train ticket."}`)
	noteRecord := projectOpenRecord("notes:note/NOTE-FIXTURE", note, body)
	if noteRecord.Ref != "notes:note/NOTE-FIXTURE" || noteRecord.VersionRef != "notes:version/NOTE-FIXTURE/abc123" {
		t.Fatalf("note open refs = %q %q", noteRecord.Ref, noteRecord.VersionRef)
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
