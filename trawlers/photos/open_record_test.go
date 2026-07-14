package photoscrawl

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

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	sourcephotos "github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	photosopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/photos/open/v1"
	"github.com/opentrawl/opentrawl/trawlkit/store"
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
			Source: archive.OpenSource{State: "current"}, Captured: &archive.OpenCaptured{Local: "2026-07-10T14:00:00+02:00", Timezone: "Europe/Amsterdam"},
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
	assertExactRecord(t, record, &photosopenv1.PhotosRecord{}, `{"schema_version":5,"ref":"photos:asset/fixture-1","stale":{"since":"2026-07-10T12:00:00Z","reason":"source details changed after this card was created","banner":"Card status: Stale · source details changed after this card was created · since 10 July 2026"},"mechanical":{"source":{"state":"current"},"captured":{"local":"2026-07-10T14:00:00+02:00","timezone":"Europe/Amsterdam"},"media":{"kind":"photo","width":"4032","height":"3024","duration_seconds":1.5},"place":{"name":"Example Square","latitude":52.3702,"longitude":4.8952},"gps":{"latitude":52.3702,"longitude":4.8952,"horizontal_accuracy_meters":4.5},"known_place":{"kind":"home","name":"Example home","after":true},"venue_candidates":[],"camera":{"display":"Example Camera","make":"Example","model":"C1","lens_model":"Prime","focal_length_mm":35,"focal_length_35mm":35,"aperture":2.8,"shutter_speed":"1/125","iso":"200"},"albums":[{"title":"Synthetic trip"}],"original":{"filename":"fixture.heic","bytes":"4096","availability":"local"},"flags":["favourite"]},"model":{"prompt_version":"v1","model_id":"example-model","summary":"Synthetic square.","description":"A synthetic scene.","ocr_text":"EXAMPLE","uncertainties":["weather"]}}`)
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
	if presentation.Title != "Synthetic square." || len(presentation.Blocks) != 6 || len(presentation.Facts) != 2 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	assertOpenPresentation(t, "photos", input, record, presentation)
	assertExactPresentation(t, presentation, `title: "Synthetic square."
blocks: { heading: { text: "Synthetic square." } anchor_id: "asset-details" }
blocks: { fields: { fields: { label: "Photo summary" display: "Synthetic square." anchor_id: "summary" } fields: { label: "Captured local time" display: "10 July 2026 at 14:00" } fields: { label: "Media" display: "photo, 4032 x 3024, 1.5s" anchor_id: "media" } fields: { label: "Place" display: "Example Square" anchor_id: "place" } fields: { label: "GPS" display: "52.3702, 4.8952 (accuracy: 4.5 m)" } fields: { label: "Known place" display: "Example home (home), after capture" anchor_id: "known-place" } fields: { label: "Camera" display: "Example Camera" } fields: { label: "Albums" display: "Synthetic trip" anchor_id: "album" } fields: { label: "Original filename" display: "fixture.heic" anchor_id: "filename" } fields: { label: "Original size" display: "4.0 KiB" } fields: { label: "Availability" display: "local" } } }
blocks: { heading: { text: "Photo text" } }
blocks: { prose: { text: "A synthetic scene." } anchor_id: "description" }
blocks: { prose: { text: "EXAMPLE" } anchor_id: "ocr" }
blocks: { prose: { text: "weather" } anchor_id: "uncertainty" }
facts: { kind: KIND_WARNING message: "Card status: Stale · source details changed after this card was created · since 10 July 2026" }
facts: { kind: KIND_WARNING message: "weather" }
primary_anchor_id: "asset-details"`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Model.Summary = ""
		if got := projectOpenPresentation(blank).Title; got != "Photo" {
			t.Fatalf("title = %q", got)
		}
	})
}

func TestPhotoPresentationContainsSemanticSearchAnchors(t *testing.T) {
	value := archive.OpenResult{
		Ref:        "photos:asset/example",
		Mechanical: archive.OpenMechanical{Source: archive.OpenSource{State: "current"}, Media: &archive.OpenMedia{Kind: "photo"}, Place: &archive.OpenPlace{Name: "Example place"}, Address: "1 Example Street", Albums: []archive.OpenAlbum{{Title: "Example album"}}, Signals: []archive.OpenSignal{{AnchorID: "metadata-example", Label: "screenshot_candidate"}}, Original: &archive.OpenOriginal{Filename: "example.heic"}, Filenames: []string{"example.heic", "alternate.heic"}},
		Model:      archive.OpenModel{Summary: "Lantern summary", Description: "Lantern description", OCRText: "LANTERN", Uncertainties: []string{"Synthetic uncertainty"}},
	}
	record := &openv1.OpenRecord{SourceId: "photos", OpenRef: value.Ref, Data: &anypb.Any{TypeUrl: "type.example/photos"}, Presentation: projectOpenPresentation(value)}
	for _, anchorID := range []string{"asset-details", "filename", "filenames", "album", "media", "place", "address", "metadata-example", "summary", "description", "ocr", "uncertainty"} {
		if err := openrecord.ValidateRequestedAnchor(record, anchorID); err != nil {
			t.Fatalf("anchor %q: %v", anchorID, err)
		}
	}
}

func TestOpenRecordFixtureBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := trawlkit.Paths{Archive: filepath.Join(root, "photos.db")}
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	snapshot, err := (sourcephotos.SQLiteSnapshotProvider{}).Snapshot(ctx, libraryPath)
	if err != nil {
		t.Fatal(err)
	}
	source := New()
	source.cfg.LibraryPath = "/synthetic/Photos Library.photoslibrary"
	source.snapshotProvider = staticSnapshotProvider{snapshot: snapshot}
	writeStore, err := store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Sync(ctx, &trawlkit.Request{Store: writeStore, Paths: paths, Progress: func(trawlkit.Progress) {}}); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	readStore := openReadStore(t, ctx, paths.Archive)
	search, err := source.Search(ctx, readRequest(readStore, paths), trawlkit.Query{Text: "synthetic", Limit: 1})
	_ = readStore.Close()
	if err != nil || len(search.Results) != 1 {
		t.Fatalf("fixture search = %#v, error = %v", search, err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	record, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, search.Results[0].Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	machine, err := record.Data.UnmarshalNew()
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := machine.(*photosopenv1.PhotosRecord)
	if !ok {
		t.Fatalf("typed record = %T", machine)
	}
	if typed.GetMechanical().GetCaptured().GetLocal() == "" || typed.GetMechanical().GetOriginal().GetBytes() != 12345 {
		t.Fatalf("typed mechanical = %s", prototext.Format(typed.GetMechanical()))
	}
	var fields []*presentationv1.Field
	for _, block := range record.Presentation.Blocks {
		if group := block.GetFields(); group != nil {
			fields = group.GetFields()
			break
		}
	}
	find := func(label string) string {
		for _, field := range fields {
			if field.GetLabel() == label {
				return field.GetDisplay()
			}
		}
		return ""
	}
	if find("Captured local time") == "" || find("Original size") != "12.1 KiB" {
		t.Fatalf("mechanical presentation = %s", prototext.Format(record.Presentation))
	}
}

func TestOpenRecordFollowsSearchAnchors(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths := trawlkit.Paths{Archive: filepath.Join(root, "photos.db")}
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	createSyntheticLibrary(t, libraryPath)
	snapshot, err := (sourcephotos.SQLiteSnapshotProvider{}).Snapshot(ctx, libraryPath)
	if err != nil {
		t.Fatal(err)
	}
	source := New()
	source.cfg.LibraryPath = "/synthetic/Photos Library.photoslibrary"
	source.snapshotProvider = staticSnapshotProvider{snapshot: snapshot}
	writeStore, err := store.Open(ctx, store.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Sync(ctx, &trawlkit.Request{Store: writeStore, Paths: paths, Progress: func(trawlkit.Progress) {}}); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	var assetID string
	if err := writeStore.DB().QueryRowContext(ctx, `select id from asset where local_identifier = 'fixture-uuid-1'`).Scan(&assetID); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	for _, observation := range []struct{ id, kind, text string }{
		{"anchor-description", "card_description", "Synthetic descriptionneedle."},
		{"anchor-summary", "card_summary", "Synthetic summaryneedle."},
	} {
		if _, err := writeStore.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, '{}', 1.0, 'fixture', 'fixture-model', 'v1', '')
`, observation.id, assetID, observation.kind, observation.text); err != nil {
			_ = writeStore.Close()
			t.Fatal(err)
		}
		if _, err := writeStore.DB().ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`, observation.id, assetID, observation.text); err != nil {
			_ = writeStore.Close()
			t.Fatal(err)
		}
	}
	for index := 0; index <= 64; index++ {
		label := fmt.Sprintf("signal-%03d", index)
		if index == 64 {
			label = "zz-beyondanchor"
		}
		id := fmt.Sprintf("anchor-metadata-%03d", index)
		if _, err := writeStore.DB().ExecContext(ctx, `
insert into metadata_observation(id, asset_id, observation_type, label, source, classifier_id, evidence_id)
values (?, ?, 'fixture', ?, 'fixture', '', '')
`, id, assetID, label); err != nil {
			_ = writeStore.Close()
			t.Fatal(err)
		}
		if _, err := writeStore.DB().ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, ?, ?)`, id, assetID, label, label); err != nil {
			_ = writeStore.Close()
			t.Fatal(err)
		}
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}

	openSearchResult := func(query, wantAnchor string) (trawlkit.Hit, *openv1.OpenRecord) {
		t.Helper()
		readStore := openReadStore(t, ctx, paths.Archive)
		request := readRequest(readStore, paths)
		search, err := source.Search(ctx, request, trawlkit.Query{Text: query, Limit: 1})
		if err != nil {
			_ = readStore.Close()
			t.Fatal(err)
		}
		if len(search.Results) != 1 || search.Results[0].AnchorID != wantAnchor {
			_ = readStore.Close()
			t.Fatalf("search %q = %#v", query, search.Results)
		}
		hit := search.Results[0]
		request.RequestedAnchorID = hit.AnchorID
		record, err := source.OpenRecord(ctx, request, hit.Ref)
		_ = readStore.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("search result: %#v", hit)
		t.Logf("public open request: ref=%q anchor=%q", hit.Ref, request.RequestedAnchorID)
		t.Logf("returned presentation: %s", prototext.Format(record.Presentation))
		if err := openrecord.ValidateRequestedAnchor(record, hit.AnchorID); err != nil {
			t.Fatal(err)
		}
		return hit, record
	}

	description, _ := openSearchResult("descriptionneedle", "description")
	_, _ = openSearchResult("summaryneedle", "summary")
	metadata, _ := openSearchResult("beyondanchor", "metadata.YW5jaG9yLW1ldGFkYXRhLTA2NA")
	if description.Ref != metadata.Ref {
		t.Fatalf("search refs differ: description=%q metadata=%q", description.Ref, metadata.Ref)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	request := readRequest(readStore, paths)
	request.RequestedAnchorID = "unknown-anchor"
	record, err := source.OpenRecord(ctx, request, description.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("public open request: ref=%q anchor=%q", description.Ref, request.RequestedAnchorID)
	t.Logf("returned presentation: %s", prototext.Format(record.Presentation))
	if err := openrecord.ValidateRequestedAnchor(record, request.RequestedAnchorID); err == nil {
		t.Fatal("unknown anchor passed canonical validation")
	}
}

func TestPresentationResourceResolverIsOpaqueAndBounded(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "photos.db")
	mediaPath := filepath.Join(t.TempDir(), "synthetic-preview.jpg")
	media := []byte("synthetic preview bytes")
	if err := os.WriteFile(mediaPath, media, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, store.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.DB().ExecContext(ctx, `
create table asset_resource (
  id text primary key,
  asset_id text not null,
  resource_type text not null,
  uti text not null,
  original_filename text not null,
  local_path text not null,
  file_size integer not null,
  available_locally integer not null,
  needs_download integer not null
)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
	insert into asset_resource values (?, ?, 'photo', 'public.jpeg', 'synthetic-preview.jpg', ?, ?, 1, 0)`, "asset_resource:example", "asset:example", mediaPath, len(media)); err != nil {
		t.Fatal(err)
	}

	crawler := New()
	req := &trawlkit.Request{Store: st, Paths: trawlkit.Paths{Archive: archivePath}}
	resource, err := crawler.presentationResource(ctx, req, "photos:asset/example")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("open ref=%q archive asset id=%q selected resource=%q", "photos:asset/example", "asset:example", "asset_resource:example")
	t.Logf("presentation resource=%#v", resource)
	if resource == nil || resource.GetKind() != presentationv1.Resource_KIND_IMAGE || resource.GetRef() != "photos:resource/asset_resource:example" || strings.Contains(resource.GetRef(), mediaPath) {
		t.Fatalf("presentation resource = %#v", resource)
	}
	request := &presentationv1.ResourceRequest{SourceId: "photos", ResourceRef: resource.GetRef(), MaxBytes: uint32(len(media))}
	response, err := crawler.ResolveResource(ctx, req, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := openrecord.ValidateResourceResponse(request, response); err != nil {
		t.Fatal(err)
	}
	t.Logf("resource request=%#v response=%#v", request, response)
	if response.GetContentType() != "image/jpeg" || !bytes.Equal(response.GetData(), media) || strings.Contains(string(response.GetData()), mediaPath) {
		t.Fatalf("resource response = %#v", response)
	}
	tooSmall := &presentationv1.ResourceRequest{SourceId: "photos", ResourceRef: resource.GetRef(), MaxBytes: uint32(len(media) - 1)}
	if _, err := crawler.ResolveResource(ctx, req, tooSmall); err == nil {
		t.Fatal("oversized resource was returned")
	}
	unsafe := &presentationv1.ResourceRequest{SourceId: "photos", ResourceRef: mediaPath, MaxBytes: uint32(len(media))}
	if _, err := crawler.ResolveResource(ctx, req, unsafe); err == nil {
		t.Fatal("raw path resource ref was accepted")
	}
	symlinkPath := filepath.Join(t.TempDir(), "synthetic-link.jpg")
	if err := os.Symlink(mediaPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
	insert into asset_resource values (?, ?, 'photo', 'public.jpeg', 'synthetic-link.jpg', ?, ?, 1, 0)`, "asset_resource:symlink", "asset:example", symlinkPath, len(media)); err != nil {
		t.Fatal(err)
	}
	symlink := &presentationv1.ResourceRequest{SourceId: "photos", ResourceRef: "photos:resource/asset_resource:symlink", MaxBytes: uint32(len(media))}
	if _, err := crawler.ResolveResource(ctx, req, symlink); err == nil {
		t.Fatal("symlink resource was returned")
	}
}

func TestPresentationResourceContentTypesAreBoundedToCommonLocalMedia(t *testing.T) {
	for _, test := range []struct {
		name, uti, filename, want string
	}{
		{name: "jpeg", uti: "public.jpeg", filename: "ignored.bin", want: "image/jpeg"},
		{name: "heic extension", filename: "synthetic.heic", want: "image/heic"},
		{name: "quicktime", uti: "com.apple.quicktime-movie", filename: "ignored.bin", want: "video/quicktime"},
		{name: "mpeg 4", filename: "synthetic.mp4", want: "video/mp4"},
		{name: "m4a", filename: "synthetic.m4a", want: "audio/mp4"},
		{name: "unsupported", filename: "synthetic.txt", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := presentationResourceContentType(test.uti, test.filename); got != test.want {
				t.Fatalf("presentationResourceContentType(%q, %q) = %q, want %q", test.uti, test.filename, got, test.want)
			}
		})
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
