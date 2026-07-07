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

func TestCloseSpellingMatchesGenuineTypos(t *testing.T) {
	tests := []struct {
		name  string
		query string
		alias string
	}{
		{name: "missing middle letter", query: "danil", alias: "daniel"},
		{name: "adjacent transposition", query: "jhon", alias: "john"},
		{name: "single substitution", query: "robin", alias: "robyn"},
		{name: "missing final duplicate", query: "miff", alias: "mif"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rank, ok := MatchRank(test.query, []string{test.alias})
			if !ok || rank != RankCloseSpelling {
				t.Fatalf("MatchRank(%q, %q) = %v, %v; want %v, true", test.query, test.alias, rank, ok, RankCloseSpelling)
			}
		})
	}
}

func TestCloseSpellingMatchesLatinBaseFirstLetter(t *testing.T) {
	tests := []struct {
		name  string
		query string
		alias string
	}{
		{name: "single word", query: "ozel", alias: "Özel"},
		{name: "word in display name", query: "ozel", alias: "Özel Example"},
		{name: "horned o", query: "oanh", alias: "Ơanh"},
		{name: "horned u", query: "uyen", alias: "Ưyen"},
		{name: "slashed o", query: "oscar", alias: "Øscar"},
		{name: "stroke d", query: "dario", alias: "Đario"},
		{name: "stroke l", query: "lukasz", alias: "Łukasz"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rank, ok := MatchRank(test.query, []string{test.alias})
			if !ok || rank != RankCloseSpelling {
				t.Fatalf("MatchRank(%q, %q) = %v, %v; want %v, true", test.query, test.alias, rank, ok, RankCloseSpelling)
			}
		})
	}
}

func TestCloseSpellingRejectsLooseShortWordMatches(t *testing.T) {
	tests := []struct {
		name  string
		query string
		alias string
	}{
		{name: "different first letter angel", query: "daniel", alias: "angel"},
		{name: "different first letter mantel", query: "daniel", alias: "mantel"},
		{name: "different first letter panel", query: "daniel", alias: "panel"},
		{name: "same first letter distance two short word", query: "daniel", alias: "denzel"},
		{name: "two rune query", query: "jo", alias: "bo"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if rank, ok := MatchRank(test.query, []string{test.alias}); ok {
				t.Fatalf("MatchRank(%q, %q) = %v, true; want no match", test.query, test.alias, rank)
			}
		})
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
