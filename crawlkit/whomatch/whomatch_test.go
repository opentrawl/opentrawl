package whomatch

import (
	"reflect"
	"testing"
	"time"
)

func TestUnicodeFoldNormalizesCaseWhitespaceAndCompactText(t *testing.T) {
	if got := Normalize("  Özel \t Example  "); got != "özel example" {
		t.Fatalf("Normalize() = %q, want %q", got, "özel example")
	}
	if got := Compact(" Özel-123 "); got != "özel123" {
		t.Fatalf("Compact() = %q, want %q", got, "özel123")
	}
	if got := Normalize("KELVIN"); got != "robin" {
		t.Fatalf("Normalize() = %q, want %q", got, "robin")
	}
	rank, ok := MatchRank("özel", []string{"Özel"})
	if !ok || rank != RankExact {
		t.Fatalf("MatchRank() = %v, %v; want %v, true", rank, ok, RankExact)
	}
}

func TestRankLadderOrdering(t *testing.T) {
	if RankExact <= RankPrefix || RankPrefix <= RankSubstring || RankSubstring <= RankCloseSpelling {
		t.Fatalf("rank ladder order changed")
	}
	tests := []struct {
		name    string
		query   string
		aliases []string
		want    Rank
	}{
		{name: "exact", query: "alex", aliases: []string{"alex"}, want: RankExact},
		{name: "compact exact", query: "alex lee", aliases: []string{"Alex-Lee"}, want: RankExact},
		{name: "prefix", query: "alex", aliases: []string{"alexandra"}, want: RankPrefix},
		{name: "substring", query: "lex", aliases: []string{"alexandra"}, want: RankSubstring},
		{name: "close spelling", query: "alex", aliases: []string{"alec"}, want: RankCloseSpelling},
		{name: "best rank wins", query: "alex", aliases: []string{"alec", "alexandra", "alex"}, want: RankExact},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := MatchRank(test.query, test.aliases)
			if !ok || got != test.want {
				t.Fatalf("MatchRank(%q, %#v) = %v, %v; want %v, true", test.query, test.aliases, got, ok, test.want)
			}
		})
	}
}

func TestCloseSpellingJeffJef(t *testing.T) {
	rank, ok := MatchRank("miff", []string{"mif"})
	if !ok || rank != RankCloseSpelling {
		t.Fatalf("MatchRank() = %v, %v; want %v, true", rank, ok, RankCloseSpelling)
	}
}

func TestCloseSpellingRejectsTwoRuneQueries(t *testing.T) {
	if rank, ok := MatchRank("jo", []string{"bo"}); ok {
		t.Fatalf("MatchRank() = %v, true; want no match", rank)
	}
}

func TestIsIdentifierLike(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		identifiers []string
		want        bool
	}{
		{name: "equals identifier", value: "ALICE@example.com", identifiers: []string{"alice@example.com"}, want: true},
		{name: "no letters", value: "+1 (555) 0100", want: true},
		{name: "display name", value: "Alice Example", identifiers: []string{"alice@example.com"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsIdentifierLike(test.value, test.identifiers); got != test.want {
				t.Fatalf("IsIdentifierLike(%q, %#v) = %v, want %v", test.value, test.identifiers, got, test.want)
			}
		})
	}
}

func TestMergeSameNameUnionsIdentifiersAndPreservesOrdering(t *testing.T) {
	oldSeen := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	newSeen := oldSeen.Add(time.Hour)
	candidates := []Candidate{
		{
			Who:         "Morgan Example",
			Identifiers: []string{"michael.icloud@example.com", "michael.gmail@example.com"},
			Aliases:     []string{"Mike"},
			LastSeen:    oldSeen,
			Messages:    1,
		},
		{
			Who:         "  morgan   example ",
			Identifiers: []string{"michael.work@example.com", "michael.gmail@example.com"},
			Aliases:     []string{"Morgan E."},
			LastSeen:    newSeen,
			Messages:    2,
		},
		{
			Who:         "Alice Example",
			Identifiers: []string{"alice@example.com"},
			LastSeen:    oldSeen,
			Messages:    4,
		},
	}

	merged := MergeSameName(candidates)
	if len(merged) != 2 {
		t.Fatalf("MergeSameName() returned %d candidates: %#v", len(merged), merged)
	}
	michael := merged[0]
	if michael.Who != "Morgan Example" {
		t.Fatalf("Who = %q, want Morgan Example", michael.Who)
	}
	wantIdentifiers := []string{"michael.icloud@example.com", "michael.gmail@example.com", "michael.work@example.com"}
	if !reflect.DeepEqual(michael.Identifiers, wantIdentifiers) {
		t.Fatalf("Identifiers = %#v, want %#v", michael.Identifiers, wantIdentifiers)
	}
	wantAliases := []string{"Mike", "Morgan E."}
	if !reflect.DeepEqual(michael.Aliases, wantAliases) {
		t.Fatalf("Aliases = %#v, want %#v", michael.Aliases, wantAliases)
	}
	if !michael.LastSeen.Equal(newSeen) {
		t.Fatalf("LastSeen = %s, want %s", michael.LastSeen, newSeen)
	}
	if michael.Messages != 3 {
		t.Fatalf("Messages = %d, want 3", michael.Messages)
	}
	if merged[1].Who != "Alice Example" {
		t.Fatalf("second candidate = %#v, want Alice Example", merged[1])
	}
}
