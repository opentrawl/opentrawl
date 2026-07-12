package notes

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	notesopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/notes/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
	presentation := projectOpenPresentation(input.RequestedRef, note, body)
	if presentation.Title != "Packing list" || len(presentation.Blocks) != 2 || len(presentation.Facts) != 0 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	evidenceInput := struct {
		RequestedRef string `json:"requested_ref"`
		Note         struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			Folder       string `json:"folder"`
			CreatedAt    string `json:"created_at"`
			ModifiedAt   string `json:"modified_at"`
			VersionCount int64  `json:"version_count"`
		} `json:"note"`
		Body struct {
			Ref         string `json:"ref"`
			TextStatus  string `json:"text_status"`
			Title       string `json:"title"`
			Text        string `json:"text"`
			Unsupported string `json:"unsupported"`
		} `json:"body"`
	}{RequestedRef: input.RequestedRef}
	evidenceInput.Note.ID, evidenceInput.Note.Title, evidenceInput.Note.Folder = note.ID, note.Title, note.Folder
	evidenceInput.Note.CreatedAt, evidenceInput.Note.ModifiedAt, evidenceInput.Note.VersionCount = note.CreatedAt, note.ModifiedAt, note.VersionCount
	evidenceInput.Body.Ref, evidenceInput.Body.TextStatus, evidenceInput.Body.Title = body.Ref, body.TextStatus, body.Title
	evidenceInput.Body.Text, evidenceInput.Body.Unsupported = body.Text, body.Unsupported
	assertOpenPresentation(t, "notes", evidenceInput, record, presentation)
	assertExactPresentation(t, presentation, `title: "Packing list"
blocks: { fields: { fields: { label: "Ref" display: "notes:version/NOTE-FIXTURE/abc123" } fields: { label: "Version ref" display: "notes:version/NOTE-FIXTURE/abc123" } fields: { label: "Folder" display: "Examples" } fields: { label: "Created" display: "2026-07-08T10:00:00Z" } fields: { label: "Modified" display: "2026-07-10T14:00:00Z" } fields: { label: "Versions" display: "3" } } }
blocks: { prose: { text: "Passport, charger and synthetic train ticket." } }`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := body
		blank.Title = ""
		noteBlank := note
		noteBlank.Title = ""
		if got := projectOpenPresentation(input.RequestedRef, noteBlank, blank).Title; got != "Note" {
			t.Fatalf("title = %q", got)
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
