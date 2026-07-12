package openrecord

import (
	"testing"

	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestValidateAcceptsCompleteRecord(t *testing.T) {
	if err := Validate(validRecord(t)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsEveryUnsafeBoundary(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*openv1.OpenRecord)
	}{
		{"nil record", func(record *openv1.OpenRecord) { *record = openv1.OpenRecord{} }},
		{"blank source", func(record *openv1.OpenRecord) { record.SourceId = " " }},
		{"blank open ref", func(record *openv1.OpenRecord) { record.OpenRef = " " }},
		{"foreign open ref", func(record *openv1.OpenRecord) { record.OpenRef = "photos:asset/1" }},
		{"missing data", func(record *openv1.OpenRecord) { record.Data = nil }},
		{"blank data type", func(record *openv1.OpenRecord) { record.Data.TypeUrl = " " }},
		{"missing presentation", func(record *openv1.OpenRecord) { record.Presentation = nil }},
		{"blank title", func(record *openv1.OpenRecord) { record.Presentation.Title = " " }},
		{"nil block", func(record *openv1.OpenRecord) { record.Presentation.Blocks[0] = nil }},
		{"nil content", func(record *openv1.OpenRecord) { record.Presentation.Blocks[0].Content = nil }},
		{"blank heading", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Heading{Heading: &presentationv1.Heading{}}
		}},
		{"blank prose", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Prose{Prose: &presentationv1.Prose{}}
		}},
		{"nil field group", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Fields{}
		}},
		{"nil field", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{nil}}}
		}},
		{"blank field label", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{{Display: "value"}}}}
		}},
		{"blank field display", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{{Label: "Label"}}}}
		}},
		{"nil table", func(record *openv1.OpenRecord) { record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{} }},
		{"table without columns", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{}}
		}},
		{"blank column", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{" "}}}
		}},
		{"nil row", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Column"}, Rows: []*presentationv1.Row{nil}}}
		}},
		{"unspecified row", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Column"}, Rows: []*presentationv1.Row{{Cells: []*presentationv1.Cell{{}}}}}}
		}},
		{"nil cell", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Column"}, Rows: []*presentationv1.Row{{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{nil}}}}}
		}},
		{"wrong table width", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"One", "Two"}, Rows: []*presentationv1.Row{{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{}}}}}}
		}},
		{"nil resource", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{}
		}},
		{"unspecified resource", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Label: "File", Ref: "notes:file/1"}}
		}},
		{"blank resource label", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_FILE, Ref: "notes:file/1"}}
		}},
		{"foreign resource ref", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_FILE, Label: "File", Ref: "photos:file/1"}}
		}},
		{"nil resource metadata", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_FILE, Label: "File", Ref: "notes:file/1", Metadata: []*presentationv1.Field{nil}}}
		}},
		{"blank resource metadata", func(record *openv1.OpenRecord) {
			record.Presentation.Blocks[0].Content = &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_FILE, Label: "File", Ref: "notes:file/1", Metadata: []*presentationv1.Field{{Label: "Type"}}}}
		}},
		{"nil action", func(record *openv1.OpenRecord) { record.Presentation.Actions[0] = nil }},
		{"blank action label", func(record *openv1.OpenRecord) { record.Presentation.Actions[0].Label = " " }},
		{"missing action target", func(record *openv1.OpenRecord) { record.Presentation.Actions[0].Target = nil }},
		{"foreign action ref", func(record *openv1.OpenRecord) {
			record.Presentation.Actions[0].Target = &presentationv1.Action_OpenRef{OpenRef: "photos:asset/1"}
		}},
		{"invalid action URL", func(record *openv1.OpenRecord) {
			record.Presentation.Actions[0].Target = &presentationv1.Action_Url{Url: "http://example.com"}
		}},
		{"nil fact", func(record *openv1.OpenRecord) { record.Presentation.Facts[0] = nil }},
		{"unspecified fact", func(record *openv1.OpenRecord) {
			record.Presentation.Facts[0].Kind = presentationv1.Fact_KIND_UNSPECIFIED
		}},
		{"blank fact message", func(record *openv1.OpenRecord) { record.Presentation.Facts[0].Message = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := proto.Clone(validRecord(t)).(*openv1.OpenRecord)
			test.mutate(record)
			if err := Validate(record); err == nil {
				t.Fatal("invalid open record was accepted")
			}
		})
	}
}

func TestValidHTTPSURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://example.com": true,
		"https://":            false,
		"http://example.com":  false,
		"/relative":           false,
	} {
		if got := ValidHTTPSURL(raw); got != want {
			t.Fatalf("ValidHTTPSURL(%q) = %t, want %t", raw, got, want)
		}
	}
}

func validRecord(t *testing.T) *openv1.OpenRecord {
	t.Helper()
	data, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	return &openv1.OpenRecord{
		SourceId: "notes",
		OpenRef:  "notes:note/1",
		Data:     data,
		Presentation: &presentationv1.PresentationDocument{
			Title:   "Note",
			Blocks:  []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{{Label: "Ref", Display: "notes:note/1"}}}}}},
			Actions: []*presentationv1.Action{{Label: "Open note", Target: &presentationv1.Action_OpenRef{OpenRef: "notes:note/1"}}},
			Facts:   []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_WARNING, Message: "Synthetic warning."}},
		},
	}
}
