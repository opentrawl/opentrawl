package trawlkit

import "testing"

func TestSearchEvidenceVocabularyCoversRepresentativeMatches(t *testing.T) {
	tests := []struct {
		name     string
		fragment EvidenceFragment
	}{
		{name: "message text", fragment: TextMatch("Message from Casey Example", "Synthetic matching evidence")},
		{name: "email header", fragment: FieldMatch("Subject", "subject", "Synthetic matching evidence")},
		{name: "email body passage", fragment: TextMatch("Message body", "Synthetic matching evidence")},
		{name: "calendar field", fragment: FieldMatch("Location", "location", "Synthetic matching evidence")},
		{name: "contact field", fragment: FieldMatch("Email", "email", "Synthetic matching evidence")},
		{name: "note passage", fragment: TextMatch("Note passage", "Synthetic matching evidence")},
		{name: "photo OCR", fragment: TextMatch("OCR", "Synthetic matching evidence")},
		{name: "photo description", fragment: TextMatch("Description", "Synthetic matching evidence")},
		{name: "attachment", fragment: MediaMatch("Attachment", "gmail:resource/example-1", "Synthetic matching evidence")},
		{name: "post relation", fragment: RelationMatch("Reply to", "reply_to", "Synthetic matching evidence")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.fragment.Label == "" {
				t.Fatalf("fragment = %#v", test.fragment)
			}
		})
	}
}

func TestSearchHitCarriesOneOpenIdentityAndOnePrimaryAnchor(t *testing.T) {
	hit := Hit{
		Ref:      "notes:note/example-1",
		AnchorID: "note-passage",
		Summary:  ResultSummary{Title: "Synthetic note", Subtitle: "Notes"},
		Evidence: []EvidenceFragment{TextMatch("Note passage", "Synthetic matching evidence")},
	}
	if hit.Ref != "notes:note/example-1" || hit.AnchorID != "note-passage" || hit.Summary.Title != "Synthetic note" || len(hit.Evidence) != 1 {
		t.Fatalf("hit = %#v", hit)
	}
}

func TestSearchEvidenceTextPreservesLabelsAndOrder(t *testing.T) {
	got := SearchEvidenceText([]EvidenceFragment{
		FieldMatch("Subject", "subject", "Synthetic agenda"),
		TextMatch("Message body", "Synthetic body passage"),
		RelationMatch("Reply to", "reply_to", "Synthetic parent post"),
	})
	want := "Subject: Synthetic agenda · Message body: Synthetic body passage · Reply to: Synthetic parent post"
	if got != want {
		t.Fatalf("SearchEvidenceText() = %q, want %q", got, want)
	}
}

func TestSearchResultTextPreservesSourceOwnedSummaryAndEvidence(t *testing.T) {
	got := SearchResultText(
		ResultSummary{Title: "Synthetic conversation", Subtitle: "Casey Example"},
		[]ArchiveContext{{Kind: "received", Label: "Received"}},
		[]EvidenceFragment{TextMatch("Message from Casey Example", "Synthetic body passage")},
	)
	want := "Synthetic conversation — Casey Example · Received · Message from Casey Example: Synthetic body passage"
	if got != want {
		t.Fatalf("SearchResultText() = %q, want %q", got, want)
	}
}
