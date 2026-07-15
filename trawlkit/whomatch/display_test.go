package whomatch

import "testing"

func TestBestDisplayNameStructuralRules(t *testing.T) {
	tests := []struct {
		name        string
		names       map[string]int
		identifiers []string
		want        string
	}{
		{
			name:  "frequency wins over case quality",
			names: map[string]int{"AVERY EXAMPLE": 3, "Avery Example": 1},
			want:  "AVERY EXAMPLE",
		},
		{
			name:        "frequency wins even for an identifier-like spelling",
			names:       map[string]int{"avery@example.com": 5, "Avery Example": 1},
			identifiers: []string{"avery@example.com"},
			want:        "avery@example.com",
		},
		{
			name:        "tied counts never pick the email-cruft spelling",
			names:       map[string]int{"Avery Example": 1, "Avery Example <avery@example.com>": 1},
			identifiers: []string{"avery@example.com"},
			want:        "Avery Example",
		},
		{
			name:        "cruft spelling pools its count with the clean spelling",
			names:       map[string]int{"Avery Example <avery@example.com>": 2, "AVERY EXAMPLE": 3},
			identifiers: []string{"avery@example.com"},
			want:        "AVERY EXAMPLE",
		},
		{
			name:        "name unlike identifier beats identifier spelling on tie",
			names:       map[string]int{"averyexample123": 1, "Avery Example": 1},
			identifiers: []string{"averyexample123"},
			want:        "Avery Example",
		},
		{
			name:  "no-letter spelling counts as identifier-like",
			names: map[string]int{"+1 555 0100": 1, "Casey Example": 1},
			want:  "Casey Example",
		},
		{
			name:  "mixed case beats lowercase beats caps",
			names: map[string]int{"AVERY": 1, "avery": 1, "Avery": 1},
			want:  "Avery",
		},
		{
			name:  "lowercase beats caps",
			names: map[string]int{"AVERY": 1, "avery": 1},
			want:  "avery",
		},
		{
			name:  "case preference beats shorter all-lower spelling",
			names: map[string]int{"casey": 1, "Casey B": 1},
			want:  "Casey B",
		},
		{
			name:  "shortest clean spelling wins",
			names: map[string]int{"Avery Example (Work)": 1, "Avery Example": 1},
			want:  "Avery Example",
		},
		{
			name:  "alphabetical is the final tie-break",
			names: map[string]int{"Bob Baker": 1, "Bob Adams": 1},
			want:  "Bob Adams",
		},
		{
			name:  "pure cruft strips to nothing",
			names: map[string]int{"<avery@example.com>": 1},
			want:  "",
		},
		{
			name:  "no names",
			names: map[string]int{},
			want:  "",
		},
		{
			name:  "unmatched angle bracket is kept verbatim",
			names: map[string]int{"I <3 Coffee": 1},
			want:  "I <3 Coffee",
		},
		{
			name:  "stripping cleans leftover whitespace",
			names: map[string]int{"  Avery   Example   <avery@example.com>  ": 1},
			want:  "Avery Example",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BestDisplayName(tc.names, tc.identifiers); got != tc.want {
				t.Fatalf("BestDisplayName(%v, %v) = %q, want %q", tc.names, tc.identifiers, got, tc.want)
			}
		})
	}
}

func TestBestDisplayNameIsDeterministic(t *testing.T) {
	names := map[string]int{"Anna": 1, "anna": 1, "ANNA": 1, "Anna B": 1, "Anna C": 1}
	want := BestDisplayName(names, nil)
	for i := 0; i < 50; i++ {
		if got := BestDisplayName(names, nil); got != want {
			t.Fatalf("run %d = %q, want stable %q", i, got, want)
		}
	}
	if want != "Anna" {
		t.Fatalf("pick = %q, want Anna (case, then shortest, then alpha)", want)
	}
}
