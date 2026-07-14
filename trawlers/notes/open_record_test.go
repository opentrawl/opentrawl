package notes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/output"
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
	value := openValue{resolvedRef: input.RequestedRef, note: note, body: body}
	record := projectOpenRecord(value)
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
	noteRecord := projectOpenRecord(openValue{resolvedRef: "notes:note/NOTE-FIXTURE", note: note, body: body})
	if noteRecord.Ref != "notes:note/NOTE-FIXTURE" || noteRecord.VersionRef != "notes:version/NOTE-FIXTURE/abc123" {
		t.Fatalf("note open refs = %q %q", noteRecord.Ref, noteRecord.VersionRef)
	}
	presentation := projectOpenPresentation(value)
	if presentation.Title != "Packing list" || len(presentation.Blocks) != 3 || len(presentation.Facts) != 0 {
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
blocks: { heading: { text: "Packing list" } anchor_id: "title" }
blocks: { fields: { fields: { label: "Folder" display: "Examples" } fields: { label: "Created" display: "8 July 2026 at 10:00" } fields: { label: "Modified" display: "10 July 2026 at 14:00" } fields: { label: "Versions" display: "3" } } }
blocks: { prose: { text: "Passport, charger and synthetic train ticket." } anchor_id: "body" }
primary_anchor_id: "body"`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := body
		blank.Title = ""
		noteBlank := note
		noteBlank.Title = ""
		if got := projectOpenPresentation(openValue{resolvedRef: input.RequestedRef, note: noteBlank, body: blank}).Title; got != "Note" {
			t.Fatalf("title = %q", got)
		}
	})
}

func TestNotePresentationContainsTitleAndBodySearchAnchors(t *testing.T) {
	value := openValue{resolvedRef: "notes:note/example", note: archive.Note{ID: "example", Title: "Lantern title"}, body: archive.VersionBody{Version: archive.Version{TextStatus: "decoded"}, Text: "Lantern body"}}
	record := &openv1.OpenRecord{SourceId: "notes", OpenRef: value.resolvedRef, Data: &anypb.Any{TypeUrl: "type.example/notes"}, Presentation: projectOpenPresentation(value)}
	for _, anchorID := range []string{"title", "body"} {
		if err := openrecord.ValidateRequestedAnchor(record, anchorID); err != nil {
			t.Fatalf("anchor %q: %v", anchorID, err)
		}
	}
}

func TestOpenRecordTimestampBoundary(t *testing.T) {
	if err := validateOpenTimestamps(openValue{note: archive.Note{CreatedAt: "2026-07-10T14:00:00.5+02:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateOpenTimestamps(openValue{note: archive.Note{ModifiedAt: "bad timestamp"}}); err == nil {
		t.Fatal("accepted malformed modified time")
	}
	if err := validateOpenTimestamps(openValue{}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecordFixtureBoundary(t *testing.T) {
	f := newFixture(t, false)
	defer f.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	crawler := New()
	crawler.syncStorePath = f.path()
	syncReq := testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := crawler.Sync(context.Background(), syncReq); err != nil {
		closeStore(t, syncReq)
		t.Fatal(err)
	}
	closeStore(t, syncReq)
	setTimes := func(created, modified string) {
		req := testRequest(t, archivePath, output.JSON, nil, true)
		_, err := req.Store.DB().ExecContext(context.Background(), `update notes set created_at = ?, modified_at = ? where note_id = ?`, created, modified, "note-alpha")
		closeStore(t, req)
		if err != nil {
			t.Fatal(err)
		}
	}
	setTimes("2026-07-08T10:00:00.5+02:00", "2026-07-10T14:00:00.5+02:00")
	recordReq := testRequest(t, archivePath, output.JSON, nil, false)
	record, err := crawler.OpenRecord(context.Background(), recordReq, "notes:note/note-alpha")
	closeStore(t, recordReq)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := machine.(*notesopenv1.NotesRecord)
	if !ok {
		t.Fatalf("typed record = %T", machine)
	}
	if typed.GetCreatedAt() != "2026-07-08T10:00:00.5+02:00" || typed.GetModifiedAt() != "2026-07-10T14:00:00.5+02:00" {
		t.Fatalf("typed timestamps = %q/%q", typed.GetCreatedAt(), typed.GetModifiedAt())
	}
	fields := record.Presentation.Blocks[1].GetFields().GetFields()
	if fields[1].GetDisplay() != "8 July 2026 at 10:00" || fields[2].GetDisplay() != "10 July 2026 at 14:00" {
		t.Fatalf("timestamp displays = %#v", fields)
	}
	setTimes("2026-07-08T10:00:00Z", "")
	recordReq = testRequest(t, archivePath, output.JSON, nil, false)
	record, err = crawler.OpenRecord(context.Background(), recordReq, "notes:note/note-alpha")
	closeStore(t, recordReq)
	if err != nil {
		t.Fatal(err)
	}
	machine, err = record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	typed = machine.(*notesopenv1.NotesRecord)
	if typed.GetModifiedAt() != "" {
		t.Fatalf("absent modified timestamp = %q", typed.GetModifiedAt())
	}
	for _, field := range record.Presentation.Blocks[0].GetFields().GetFields() {
		if field.GetLabel() == "Modified" {
			t.Fatal("absent modified timestamp was presented")
		}
	}
	setTimes("2026-07-08T10:00:00Z", "not-a-timestamp")
	recordReq = testRequest(t, archivePath, output.JSON, nil, false)
	_, err = crawler.OpenRecord(context.Background(), recordReq, "notes:note/note-alpha")
	closeStore(t, recordReq)
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
