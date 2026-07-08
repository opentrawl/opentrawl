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
			names: map[string]int{"JOSH PALMER": 3, "Josh Palmer": 1},
			want:  "JOSH PALMER",
		},
		{
			name:        "frequency wins even for an identifier-like spelling",
			names:       map[string]int{"ebbak@spotify.com": 5, "Ebba Krusenstierna": 1},
			identifiers: []string{"ebbak@spotify.com"},
			want:        "ebbak@spotify.com",
		},
		{
			name:        "tied counts never pick the email-cruft spelling",
			names:       map[string]int{"Ebba Krusenstierna": 1, "Ebba Krusenstierna <ebbak@spotify.com>": 1},
			identifiers: []string{"ebbak@spotify.com"},
			want:        "Ebba Krusenstierna",
		},
		{
			name:        "cruft spelling pools its count with the clean spelling",
			names:       map[string]int{"Ebba Krusenstierna <ebbak@spotify.com>": 2, "EBBA KRUSENSTIERNA": 3},
			identifiers: []string{"ebbak@spotify.com"},
			want:        "EBBA KRUSENSTIERNA",
		},
		{
			name:        "name unlike identifier beats identifier spelling on tie",
			names:       map[string]int{"joshpalmer123": 1, "Josh Palmer": 1},
			identifiers: []string{"joshpalmer123"},
			want:        "Josh Palmer",
		},
		{
			name:  "no-letter spelling counts as identifier-like",
			names: map[string]int{"+31 6 12345678": 1, "Katja": 1},
			want:  "Katja",
		},
		{
			name:  "mixed case beats lowercase beats caps",
			names: map[string]int{"EBBA": 1, "ebba": 1, "Ebba": 1},
			want:  "Ebba",
		},
		{
			name:  "lowercase beats caps",
			names: map[string]int{"EBBA": 1, "ebba": 1},
			want:  "ebba",
		},
		{
			name:  "case preference beats shorter all-lower spelling",
			names: map[string]int{"katja": 1, "Katja B": 1},
			want:  "Katja B",
		},
		{
			name:  "shortest clean spelling wins",
			names: map[string]int{"Josh Palmer (Work)": 1, "Josh Palmer": 1},
			want:  "Josh Palmer",
		},
		{
			name:  "alphabetical is the final tie-break",
			names: map[string]int{"Bob Baker": 1, "Bob Adams": 1},
			want:  "Bob Adams",
		},
		{
			name:  "pure cruft strips to nothing",
			names: map[string]int{"<ebbak@spotify.com>": 1},
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
			names: map[string]int{"  Ebba   Krusenstierna   <ebbak@spotify.com>  ": 1},
			want:  "Ebba Krusenstierna",
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
