package photoscrawl

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	photosopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/photos/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	latitude, longitude := 52.3702, 4.8952
	input := archive.OpenResult{
		SchemaVersion: 4, Ref: "photos:asset/fixture-1",
		Stale: &archive.OpenStale{Since: "2026-07-10T12:00:00Z", Reason: "asset metadata changed in sync (fingerprint drift)", Banner: "legacy debug banner"},
		Mechanical: archive.OpenMechanical{
			Source: archive.OpenSource{State: "current"}, Captured: &archive.OpenCaptured{Local: "10 July 2026 at 14:00", Timezone: "Europe/Amsterdam"},
			Media: &archive.OpenMedia{Kind: "photo", Width: 4032, Height: 3024, DurationSeconds: 1.5},
			Place: &archive.OpenPlace{Name: "Example Square", Latitude: &latitude, Longitude: &longitude}, GPS: &archive.OpenGPS{Latitude: latitude, Longitude: longitude, HorizontalAccuracyMeters: 4.5},
			KnownPlace: &archive.OpenKnownPlace{Kind: "home", Name: "Example home", After: true}, Camera: &archive.OpenCamera{Display: "Example Camera", Make: "Example", Model: "C1", LensModel: "Prime", FocalLengthMM: 35, FocalLength35MM: 35, Aperture: 2.8, ShutterSpeed: "1/125", ISO: 200},
			Albums: []archive.OpenAlbum{{Title: "Synthetic trip"}}, Original: &archive.OpenOriginal{Filename: "fixture.heic", Bytes: 4096, Availability: "local"}, Flags: []string{"favourite"},
		},
		Model: archive.OpenModel{PromptVersion: "v1", ModelID: "example-model", Summary: "Synthetic square.", Description: "A synthetic scene.", OCRText: "EXAMPLE", Uncertainties: []string{"weather"}},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	record := projectOpenRecord(input)
	if record.SchemaVersion != 5 {
		t.Fatalf("schema version = %d", record.SchemaVersion)
	}
	if record.Stale.GetReason() != "source details changed after this card was created" || record.Stale.GetBanner() != "Card status: Stale · source details changed after this card was created · since 10 July 2026" {
		t.Fatalf("stale = %#v", record.Stale)
	}
	if len(record.Mechanical.Albums) != 1 || record.Mechanical.Albums[0].Title != "Synthetic trip" {
		t.Fatalf("albums = %#v", record.Mechanical.Albums)
	}
	if record.Mechanical.Albums[0].ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name("kind")) != nil {
		t.Fatal("album kind remains in generated contract")
	}
	name := string(record.ProtoReflect().Descriptor().FullName())
	if name != "trawl.source.photos.open.v1.PhotosRecord" {
		t.Fatalf("message name = %q", name)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/trawl.source.photos.open.v1.PhotosRecord" {
		t.Fatal("type URL changed")
	}
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	for _, fragment := range []string{`"schema_version":5`, `"ref":"photos:asset/fixture-1"`, `"horizontal_accuracy_meters":4.5`, `"albums":[{"title":"Synthetic trip"}]`} {
		if !strings.Contains(string(data), fragment) {
			t.Fatalf("ProtoJSON missing %q: %s", fragment, data)
		}
	}
	for _, forbidden := range []string{"legacy debug banner", `"kind":"provider-private"`} {
		if strings.Contains(string(data), forbidden) || strings.Contains(text, forbidden) {
			t.Fatalf("removed field leaked %q", forbidden)
		}
	}
	assertExactRecord(t, record, &photosopenv1.PhotosRecord{}, `{"schema_version":5,"ref":"photos:asset/fixture-1","stale":{"since":"2026-07-10T12:00:00Z","reason":"source details changed after this card was created","banner":"Card status: Stale · source details changed after this card was created · since 10 July 2026"},"mechanical":{"source":{"state":"current"},"captured":{"local":"10 July 2026 at 14:00","timezone":"Europe/Amsterdam"},"media":{"kind":"photo","width":"4032","height":"3024","duration_seconds":1.5},"place":{"name":"Example Square","latitude":52.3702,"longitude":4.8952},"gps":{"latitude":52.3702,"longitude":4.8952,"horizontal_accuracy_meters":4.5},"known_place":{"kind":"home","name":"Example home","after":true},"venue_candidates":[],"camera":{"display":"Example Camera","make":"Example","model":"C1","lens_model":"Prime","focal_length_mm":35,"focal_length_35mm":35,"aperture":2.8,"shutter_speed":"1/125","iso":"200"},"albums":[{"title":"Synthetic trip"}],"original":{"filename":"fixture.heic","bytes":"4096","availability":"local"},"flags":["favourite"]},"model":{"prompt_version":"v1","model_id":"example-model","summary":"Synthetic square.","description":"A synthetic scene.","ocr_text":"EXAMPLE","uncertainties":["weather"]}}`)
	current := input
	current.Stale = nil
	currentRecord := projectOpenRecord(current)
	currentJSON, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(currentRecord)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(currentJSON), `"stale"`) {
		t.Fatalf("current record has stale: %s", currentJSON)
	}
	gpsOnly := archive.OpenResult{Ref: "photos:asset/gps-only", Mechanical: archive.OpenMechanical{Source: archive.OpenSource{State: "current"}, GPS: &archive.OpenGPS{Latitude: 1.25, Longitude: 2.5, HorizontalAccuracyMeters: 3.75}}}
	gpsJSON, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(projectOpenRecord(gpsOnly))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gpsJSON), `"horizontal_accuracy_meters":3.75`) || strings.Contains(string(gpsJSON), `"address"`) {
		t.Fatalf("GPS-only ProtoJSON = %s", gpsJSON)
	}
	presentation := projectOpenPresentation(input)
	if presentation.Title != "Synthetic square." || len(presentation.Blocks) != 3 || len(presentation.Facts) != 2 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertOpenPresentation(t, "photos", input, record, presentation)
	assertExactPresentation(t, presentation, `title: "Synthetic square."
blocks: { fields: { fields: { label: "Ref" display: "photos:asset/fixture-1" } fields: { label: "Captured" display: "10 July 2026 at 14:00" } fields: { label: "Media" display: "photo, 4032 x 3024, 1.5s" } fields: { label: "Place" display: "Example Square" } fields: { label: "GPS" display: "52.3702, 4.8952 (accuracy: 4.5 m)" } fields: { label: "Known place" display: "Example home (home), after capture" } fields: { label: "Camera" display: "Example Camera" } fields: { label: "Albums" display: "Synthetic trip" } fields: { label: "Original filename" display: "fixture.heic" } fields: { label: "Original size" display: "4096 bytes" } fields: { label: "Availability" display: "local" } } }
blocks: { prose: { text: "A synthetic scene." } }
blocks: { prose: { text: "EXAMPLE" } }
facts: { kind: KIND_WARNING message: "Card status: Stale · source details changed after this card was created · since 10 July 2026" }
facts: { kind: KIND_WARNING message: "weather" }`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Model.Summary = ""
		if got := projectOpenPresentation(blank).Title; got != "Photo" {
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
