package main

import "testing"

func TestParseIssueIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    IssueIdentifier
		wantErr bool
	}{
		{
			name: "standard identifier",
			raw:  "TRAWL-99",
			want: IssueIdentifier{TeamKey: "TRAWL", Number: 99},
		},
		{
			name: "lowercase team key",
			raw:  "trawl-7",
			want: IssueIdentifier{TeamKey: "TRAWL", Number: 7},
		},
		{name: "missing dash", raw: "TRAWL99", wantErr: true},
		{name: "empty team", raw: "-99", wantErr: true},
		{name: "bad number", raw: "TRAWL-x", wantErr: true},
		{name: "zero number", raw: "TRAWL-0", wantErr: true},
		{name: "bad team", raw: "TRAWL_DEV-1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIssueIdentifier(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
