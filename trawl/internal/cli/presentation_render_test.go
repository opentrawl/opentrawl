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

func TestRenderPresentationRendersAffectedSourceDocuments(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	tests := []struct {
		name     string
		document *presentationv1.PresentationDocument
		want     string
	}{
		{
			name: "calendar",
			document: &presentationv1.PresentationDocument{Title: "Synthetic planning", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Start", "10 July 2026 at 14:00:00 +02:00"), field("End", "10 July 2026 at 15:00:00 +02:00"), field("All day", "No"), field("Calendar", "Projects"), field("Account", "example.com"), field("Availability", "Free"), field("Location", "Example room, 1 Example Street"), field("Organizer", "Avery Example"), field("Attendees", "Morgan Example (accepted)"), field("URL", "https://example.com/event"), field("Status", "confirmed"), field("Recurring", "Yes")),
				proseBlock("Review the fixture."),
			}, Actions: []*presentationv1.Action{{Label: "Open event link", Target: &presentationv1.Action_Url{Url: "https://example.com/event"}}}, Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Event description is truncated."}}},
			want: "Synthetic planning\n\nStart: 10 July 2026 at 14:00:00 +02:00\nEnd: 10 July 2026 at 15:00:00 +02:00\nAll day: No\nCalendar: Projects\nAccount: example.com\nAvailability: Free\nLocation: Example room, 1 Example Street\nOrganizer: Avery Example\nAttendees: Morgan Example (accepted)\nURL: https://example.com/event\nStatus: confirmed\nRecurring: Yes\n\nReview the fixture.\n\nOpen event link: https://example.com/event\n\nTruncated: Event description is truncated.\n",
		},
		{
			name: "gmail",
			document: &presentationv1.PresentationDocument{Title: "Project Lantern", Blocks: []*presentationv1.Block{
				fieldsBlock(field("From", "Avery Example <avery@example.com>"), field("To", "morgan@example.com"), field("Cc", "team@example.com"), field("Date", "10 July 2026 at 14:00:00 +00:00"), field("Labels", "INBOX, STARRED"), field("Unread", "Yes")),
				proseBlock("Synthetic review body."),
				tableBlock([]string{"File", "Type", "Bytes"}, row(presentationv1.Row_ROLE_NORMAL, "brief.pdf", "application/pdf", "2.0 KiB")),
			}, Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Message body is truncated; 17 characters omitted."}}},
			want: "Project Lantern\n\nFrom: Avery Example <avery@example.com>\nTo: morgan@example.com\nCc: team@example.com\nDate: 10 July 2026 at 14:00:00 +00:00\nLabels: INBOX, STARRED\nUnread: Yes\n\nSynthetic review body.\n\nfile       type             bytes\nbrief.pdf  application/pdf  2.0 KiB\n\nTruncated: Message body is truncated; 17 characters omitted.\n",
		},
		{
			name: "imessage",
			document: &presentationv1.PresentationDocument{Title: "Project Lantern", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Participants", "Avery Example, +15550001111")),
				proseBlock("The synthetic pickup moved to Friday."),
				tableBlock([]string{"Time", "From", "Text"}, row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 13:59:00 +00:00", "me", "That works."), row(presentationv1.Row_ROLE_TARGET, "10 July 2026 at 14:00:00 +00:00", "Avery Example", "The synthetic pickup moved to Friday."), row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 14:01:00 +00:00", "Avery Example", "")),
			}},
			want: "Project Lantern\n\nParticipants: Avery Example, +1 (555) 000-1111\n\nThe synthetic pickup moved to Friday.\n\ntime                               from           text\n10 July 2026 at 13:59:00 +00:00    me             That works.\n→ 10 July 2026 at 14:00:00 +00:00  Avery Example  The synthetic pickup moved to Friday.\n10 July 2026 at 14:01:00 +00:00    Avery Example  (empty)\n",
		},
		{
			name:     "notes",
			document: &presentationv1.PresentationDocument{Title: "Packing list", Blocks: []*presentationv1.Block{fieldsBlock(field("Folder", "Examples"), field("Created", "8 July 2026 at 10:00:00 +00:00"), field("Modified", "10 July 2026 at 14:00:00 +00:00"), field("Versions", "3")), proseBlock("Passport, charger and synthetic train ticket.")}},
			want:     "Packing list\n\nFolder: Examples\nCreated: 8 July 2026 at 10:00:00 +00:00\nModified: 10 July 2026 at 14:00:00 +00:00\nVersions: 3\n\nPassport, charger and synthetic train ticket.\n",
		},
		{
			name: "photos",
			document: &presentationv1.PresentationDocument{Title: "Synthetic square.", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Captured local time", "10 July 2026 at 14:00:00 +02:00"), field("Media", "photo, 4032 x 3024, 1.5s"), field("Place", "Example Square"), field("GPS", "52.3702, 4.8952 (accuracy: 4.5 m)"), field("Known place", "Example home (home), after capture"), field("Camera", "Example Camera"), field("Albums", "Synthetic trip"), field("Original filename", "fixture.heic"), field("Original size", "4.0 KiB"), field("Availability", "local")),
				proseBlock("A synthetic scene."),
				proseBlock("EXAMPLE"),
			}, Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_WARNING, Message: "Card status: Stale · source details changed after this card was created · since 10 July 2026"}, {Kind: presentationv1.Fact_KIND_WARNING, Message: "weather"}}},
			want: "Synthetic square.\n\nCaptured local time: 10 July 2026 at 14:00:00 +02:00\nMedia: photo, 4032 x 3024, 1.5s\nPlace: Example Square\nGPS: 52.3702, 4.8952 (accuracy: 4.5 m)\nKnown place: Example home (home), after capture\nCamera: Example Camera\nAlbums: Synthetic trip\nOriginal filename: fixture.heic\nOriginal size: 4.0 KiB\nAvailability: local\n\nA synthetic scene.\n\nEXAMPLE\n\nWarning: Card status: Stale · source details changed after this card was created · since 10 July 2026\nWarning: weather\n",
		},
		{
			name: "telegram",
			document: &presentationv1.PresentationDocument{Title: "Lantern", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Participants", "Avery Example, Morgan Example")), proseBlock("Target"),
				tableBlock([]string{"Time", "From", "Text"}, row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 13:59:00 +00:00", "Morgan Example", "Before"), row(presentationv1.Row_ROLE_TARGET, "10 July 2026 at 14:00:00 +00:00", "Avery Example", "Target"), row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 14:01:00 +00:00", "Unavailable", "After"), row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 14:02:00 +00:00", "Unavailable", "No exported sender")),
			}, Actions: []*presentationv1.Action{{Label: "Open media link", Target: &presentationv1.Action_Url{Url: "https://example.com/fixture"}}, {Label: "Open metadata link", Target: &presentationv1.Action_Url{Url: "https://example.com"}}}, Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Earlier context is truncated."}}},
			want: "Lantern\n\nParticipants: Avery Example, Morgan Example\n\nTarget\n\ntime                               from            text\n10 July 2026 at 13:59:00 +00:00    Morgan Example  Before\n→ 10 July 2026 at 14:00:00 +00:00  Avery Example   Target\n10 July 2026 at 14:01:00 +00:00    Unavailable     After\n10 July 2026 at 14:02:00 +00:00    Unavailable     No exported sender\n\nOpen media link: https://example.com/fixture\nOpen metadata link: https://example.com\n\nTruncated: Earlier context is truncated.\n",
		},
		{
			name: "x",
			document: &presentationv1.PresentationDocument{Title: "me (@avery)", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Time", "10 July 2026 at 14:00:00 +00:00"), field("Likes", "4"), field("Reposts", "2"), field("Replies", "1"), field("Counts as of", "10 July 2026 at 15:00:00 +00:00")), proseBlock("RT @example synthetic text"), headingBlock("Ancestors"), tableBlock([]string{"Time", "From", "Text"}, row(presentationv1.Row_ROLE_NORMAL, "", "", "unavailable (not in archive)")), headingBlock("Replies"), tableBlock([]string{"Time", "From", "Text"}, row(presentationv1.Row_ROLE_NORMAL, "", "Morgan Example (@morgan)", "Synthetic reply.")),
			}, Facts: []*presentationv1.Fact{{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Earlier conversation context is truncated."}, {Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Replies are truncated."}}},
			want: "me (@avery)\n\nTime: 10 July 2026 at 14:00:00 +00:00\nLikes: 4\nReposts: 2\nReplies: 1\nCounts as of: 10 July 2026 at 15:00:00 +00:00\n\nRT @example synthetic text\n\nAncestors\n\ntime     from     text\n(empty)  (empty)  unavailable (not in archive)\n\nReplies\n\ntime     from                      text\n(empty)  Morgan Example (@morgan)  Synthetic reply.\n\nTruncated: Earlier conversation context is truncated.\nTruncated: Replies are truncated.\n",
		},
		{
			name: "whatsapp",
			document: &presentationv1.PresentationDocument{Title: "Lantern group", Blocks: []*presentationv1.Block{
				fieldsBlock(field("Participants", "Avery Example, Morgan Example")), proseBlock("[image]"), tableBlock([]string{"Time", "From", "Text"}, row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 13:59:00 +00:00", "me", "Sent."), row(presentationv1.Row_ROLE_TARGET, "10 July 2026 at 14:00:00 +00:00", "Avery Example", "[image]"), row(presentationv1.Row_ROLE_NORMAL, "10 July 2026 at 14:01:00 +00:00", "Avery Example", "Received.")), fieldsBlock(field("Media type", "image"), field("Media title", "fixture.jpg"), field("Media size", "2.0 KiB")),
			}},
			want: "Lantern group\n\nParticipants: Avery Example, Morgan Example\n\n[image]\n\ntime                               from           text\n10 July 2026 at 13:59:00 +00:00    me             Sent.\n→ 10 July 2026 at 14:00:00 +00:00  Avery Example  [image]\n10 July 2026 at 14:01:00 +00:00    Avery Example  Received.\n\nMedia type: image\nMedia title: fixture.jpg\nMedia size: 2.0 KiB\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := renderPresentation(&output, test.document); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("rendered document = %q, want %q", got, test.want)
			}
		})
	}
}

func field(label, display string) *presentationv1.Field {
	return &presentationv1.Field{Label: label, Display: display}
}

func fieldsBlock(fields ...*presentationv1.Field) *presentationv1.Block {
	return &presentationv1.Block{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}
}

func proseBlock(text string) *presentationv1.Block {
	return &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}}
}

func headingBlock(text string) *presentationv1.Block {
	return &presentationv1.Block{Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: text}}}
}

func tableBlock(columns []string, rows ...*presentationv1.Row) *presentationv1.Block {
	return &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: columns, Rows: rows}}}
}

func row(role presentationv1.Row_Role, cells ...string) *presentationv1.Row {
	values := make([]*presentationv1.Cell, 0, len(cells))
	for _, cell := range cells {
		values = append(values, &presentationv1.Cell{Display: cell})
	}
	return &presentationv1.Row{Role: role, Cells: values}
}
