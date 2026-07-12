package cli

import (
	"bytes"
	"strings"
	"testing"

	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func TestRenderPresentationRendersEveryGenericBlock(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	document := &presentationv1.PresentationDocument{
		Title: "Synthetic note",
		Blocks: []*presentationv1.Block{
			{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: []*presentationv1.Field{{Label: "ref", Display: "notes:note/example-1"}, {Label: "folder", Display: "Examples"}}}}},
			{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: "Synthetic body."}}},
			{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"time", "text"}, Rows: []*presentationv1.Row{{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{Display: "2026-07-11"}, {Display: "Earlier synthetic row."}}}, {Role: presentationv1.Row_ROLE_TARGET, Cells: []*presentationv1.Cell{{Display: "2026-07-12"}, {Display: "Synthetic row."}}}}}}},
			{Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: "Attachments"}}},
			{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_FILE, Label: "Synthetic file", Ref: "notes:attachment/file-1"}}},
			{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_IMAGE, Label: "Synthetic image", Ref: "notes:attachment/example-1", Metadata: []*presentationv1.Field{{Label: "type", Display: "image/heic"}}}}},
			{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_VIDEO, Label: "Synthetic video", Ref: "notes:attachment/video-1"}}},
			{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{Kind: presentationv1.Resource_KIND_AUDIO, Label: "Synthetic audio", Ref: "notes:attachment/audio-1"}}},
		},
		Actions: []*presentationv1.Action{{Label: "Open record", Target: &presentationv1.Action_OpenRef{OpenRef: "notes:note/example-1"}}, {Label: "Open image", Target: &presentationv1.Action_Url{Url: "https://example.com/image"}}},
		Facts: []*presentationv1.Fact{
			{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Synthetic content was truncated."},
			{Kind: presentationv1.Fact_KIND_PROVENANCE, Message: "Synthetic fixture."},
			{Kind: presentationv1.Fact_KIND_WARNING, Message: "Synthetic warning."},
			{Kind: presentationv1.Fact_KIND_ERROR, Message: "Synthetic error.", Remedy: "Re-sync the synthetic fixture."},
		},
	}
	var output bytes.Buffer
	if err := renderPresentation(&output, document); err != nil {
		t.Fatal(err)
	}
	want := "Synthetic note\n\nRef: notes:note/example-1\nFolder: Examples\n\nSynthetic body.\n\ntime          text\n2026-07-11    Earlier synthetic row.\n→ 2026-07-12  Synthetic row.\n\nAttachments\n\nFile: Synthetic file\nRef: notes:attachment/file-1\n\nImage: Synthetic image\nRef: notes:attachment/example-1\nType: image/heic\n\nVideo: Synthetic video\nRef: notes:attachment/video-1\n\nAudio: Synthetic audio\nRef: notes:attachment/audio-1\n\nOpen record: trawl open notes:note/example-1\nOpen image: https://example.com/image\n\nTruncated: Synthetic content was truncated.\nProvenance: Synthetic fixture.\nWarning: Synthetic warning.\nError: Synthetic error.\n  Remedy: Re-sync the synthetic fixture.\n"
	if output.String() != want {
		t.Fatalf("presentation =\n%s\nwant:\n%s", output.String(), want)
	}
}

func TestRenderPresentationRejectsInvalidGenericValues(t *testing.T) {
	tests := []struct {
		name     string
		document *presentationv1.PresentationDocument
		want     string
	}{
		{"nil", nil, "nil"},
		{"unknown block", &presentationv1.PresentationDocument{Blocks: []*presentationv1.Block{{}}}, "unknown content"},
		{"row role", &presentationv1.PresentationDocument{Blocks: []*presentationv1.Block{{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"value"}, Rows: []*presentationv1.Row{{}}}}}}}, "unspecified role"},
		{"resource kind", &presentationv1.PresentationDocument{Blocks: []*presentationv1.Block{{Content: &presentationv1.Block_Resource{Resource: &presentationv1.Resource{}}}}}, "unspecified kind"},
		{"action target", &presentationv1.PresentationDocument{Actions: []*presentationv1.Action{{Label: "Open"}}}, "no target"},
		{"fact kind", &presentationv1.PresentationDocument{Facts: []*presentationv1.Fact{{Message: "Synthetic"}}}, "unspecified kind"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := renderPresentation(&bytes.Buffer{}, test.document); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}
