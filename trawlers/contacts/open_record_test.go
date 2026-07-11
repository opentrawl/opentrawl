package clawdex

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	contactsopenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/contacts/open/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
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
