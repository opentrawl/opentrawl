package clawdex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	contactsopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/contacts/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestOpenRecordProjection(t *testing.T) {
	input := model.Person{
		ID: "person_storage_fixture", Name: "Avery Example", SortName: "Example, Avery", AKA: []string{"Avery E."}, Tags: []string{"project"},
		Emails: []model.ContactValue{{Value: "avery@example.com", Label: "work", Source: "provider-private", Primary: true}},
		Phones: []model.ContactValue{{Value: "+15550001111", Label: "mobile"}}, Addresses: []model.ContactValue{{Value: "1 Example Street", Label: "work"}},
		Accounts: map[string][]string{"telegram": {"avery_example"}}, Annotation: "Synthetic collaborator.", AnnotationStatedAt: "2026-07-10",
		Apple: model.ExternalRef{ID: "apple-private"}, Google: model.ExternalRef{ID: "google-private"}, Avatar: model.AvatarRef{SHA256: "private-hash"}, Path: "/private/archive", CreatedAt: time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC), Extra: map[string]map[string]any{"private": {"token": "hidden"}},
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("canonical Go input: %s", inputJSON)
	record := projectOpenRecord(archive.PersonRef(input.ID), input)
	name := string(record.ProtoReflect().Descriptor().FullName())
	if name != "trawl.source.contacts.open.v1.ContactsRecord" {
		t.Fatalf("message name = %q", name)
	}
	if "type.opentrawl.org/"+name != "type.opentrawl.org/trawl.source.contacts.open.v1.ContactsRecord" {
		t.Fatal("type URL changed")
	}
	if record.Ref != "contacts:person/person_storage_fixture" {
		t.Fatalf("ref = %q", record.Ref)
	}
	text := prototext.Format(record)
	t.Logf("protobuf text:\n%s", text)
	data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ProtoJSON: %s", data)
	for _, forbidden := range []string{"person_storage_fixture\"", "apple-private", "google-private", "provider-private", "private-hash", "/private/archive", "token", "created_at", "updated_at", "source"} {
		if forbidden == "person_storage_fixture\"" {
			continue
		}
		if strings.Contains(string(data), forbidden) || strings.Contains(text, forbidden) {
			t.Fatalf("storage field leaked %q: %s", forbidden, data)
		}
	}
	if strings.Contains(string(data), `"id":`) {
		t.Fatalf("storage ID field leaked: %s", data)
	}
	assertExactRecord(t, record, &contactsopenv1.ContactsRecord{}, `{"ref":"contacts:person/person_storage_fixture","name":"Avery Example","sort_name":"Example, Avery","aka":["Avery E."],"tags":["project"],"emails":[{"value":"avery@example.com","label":"work","primary":true}],"phones":[{"value":"+15550001111","label":"mobile"}],"addresses":[{"value":"1 Example Street","label":"work"}],"accounts":{"telegram":{"values":["avery_example"]}},"annotation":"Synthetic collaborator.","annotation_stated_at":"2026-07-10"}`)
	presentation := projectOpenPresentation(archive.PersonRef(input.ID), input)
	if presentation.Title != "Avery Example" || len(presentation.Blocks) != 1 || len(presentation.Blocks[0].GetFields().Fields) != 8 {
		t.Fatalf("presentation = %s", prototext.Format(presentation))
	}
	type evidenceContactValue struct {
		Value   string `json:"value"`
		Label   string `json:"label"`
		Primary bool   `json:"primary"`
	}
	projectedValues := func(values []model.ContactValue) []evidenceContactValue {
		result := make([]evidenceContactValue, 0, len(values))
		for _, value := range values {
			result = append(result, evidenceContactValue{Value: value.Value, Label: value.Label, Primary: value.Primary})
		}
		return result
	}
	evidenceInput := struct {
		Ref                string                 `json:"ref"`
		Name               string                 `json:"name"`
		SortName           string                 `json:"sort_name"`
		AKA                []string               `json:"aka"`
		Tags               []string               `json:"tags"`
		Emails             []evidenceContactValue `json:"emails"`
		Phones             []evidenceContactValue `json:"phones"`
		Addresses          []evidenceContactValue `json:"addresses"`
		Accounts           map[string][]string    `json:"accounts"`
		Annotation         string                 `json:"annotation"`
		AnnotationStatedAt string                 `json:"annotation_stated_at"`
	}{archive.PersonRef(input.ID), input.Name, input.SortName, input.AKA, input.Tags, projectedValues(input.Emails), projectedValues(input.Phones), projectedValues(input.Addresses), input.Accounts, input.Annotation, input.AnnotationStatedAt}
	assertOpenPresentation(t, "contacts", evidenceInput, record, presentation)
	assertExactPresentation(t, presentation, `title: "Avery Example"
blocks: { fields: { fields: { label: "Ref" display: "contacts:person/person_storage_fixture" } fields: { label: "Also known as" display: "Avery E." } fields: { label: "Tags" display: "project" } fields: { label: "Emails" display: "avery@example.com (work) [primary]" } fields: { label: "Phones" display: "+15550001111 (mobile)" } fields: { label: "Addresses" display: "1 Example Street (work)" } fields: { label: "Accounts" display: "telegram: avery_example" } fields: { label: "Annotation" display: "Synthetic collaborator." } } }`)
	t.Run("blank_title_uses_source_fallback", func(t *testing.T) {
		blank := input
		blank.Name = ""
		if got := projectOpenPresentation(archive.PersonRef(blank.ID), blank).Title; got != "Contact" {
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
