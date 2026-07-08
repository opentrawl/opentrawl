package shortref

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestAliasIsDeterministic(t *testing.T) {
	fullRef := "telegram:msg/1234567890123456789"
	first := Alias(fullRef, 10)
	if len(first) != 10 {
		t.Fatalf("alias length = %d, want 10", len(first))
	}
	for range 100 {
		if got := Alias(fullRef, 10); got != first {
			t.Fatalf("alias changed: %q then %q", first, got)
		}
	}
}

func TestAlphabetIsSafe(t *testing.T) {
	for _, forbidden := range []string{"l", "o", "0", "1", "i"} {
		if strings.Contains(Alphabet, forbidden) {
			t.Fatalf("alphabet contains forbidden character %q", forbidden)
		}
	}
	for n := range 1000 {
		alias := Alias(fmt.Sprintf("source:item/%d", n), 10)
		if !ValidAlias(alias) {
			t.Fatalf("alias %q should be valid", alias)
		}
		for _, forbidden := range []string{"l", "o", "0", "1", "i"} {
			if strings.Contains(alias, forbidden) {
				t.Fatalf("alias %q contains forbidden character %q", alias, forbidden)
			}
		}
	}
	for _, invalid := range []string{"abcd", "abc0d", "abc1d", "abcld", "abcod", "abcid", "ABCDE"} {
		if ValidAlias(invalid) {
			t.Fatalf("alias %q should be invalid", invalid)
		}
	}
}

func TestDefaultLengthThresholds(t *testing.T) {
	threshold := func(length int) int {
		aliases := math.Pow(float64(len(Alphabet)), float64(length))
		return int(math.Floor(2*aliases*negligibleCollisionPairRate + 1))
	}
	tests := []struct {
		corpusSize int
		want       int
	}{
		{corpusSize: -1, want: MinLength},
		{corpusSize: 0, want: MinLength},
		{corpusSize: 1, want: MinLength},
		{corpusSize: threshold(5), want: 5},
		{corpusSize: threshold(5) + 1, want: 6},
		{corpusSize: threshold(6), want: 6},
		{corpusSize: threshold(6) + 1, want: 7},
	}
	for _, test := range tests {
		if got := DefaultLength(test.corpusSize); got != test.want {
			t.Fatalf("DefaultLength(%d) = %d, want %d", test.corpusSize, got, test.want)
		}
	}
}

func TestAliasPrefixPositionsUseFullAlphabet(t *testing.T) {
	const (
		refs        = 50_000
		aliasLength = 10
	)
	limit := (refs / len(Alphabet)) * 2
	counts := make([]map[byte]int, aliasLength)
	for position := range counts {
		counts[position] = make(map[byte]int, len(Alphabet))
	}

	for n := range refs {
		alias := Alias(fmt.Sprintf("source:item/%d", n), aliasLength)
		for position := range aliasLength {
			counts[position][alias[position]]++
		}
	}

	for position, positionCounts := range counts {
		if len(positionCounts) != len(Alphabet) {
			t.Fatalf("position %d used %d symbols, want %d: %#v", position, len(positionCounts), len(Alphabet), positionCounts)
		}
		for _, symbol := range []byte(Alphabet) {
			if positionCounts[symbol] > limit {
				t.Fatalf("position %d symbol %q count=%d, want <= %d", position, symbol, positionCounts[symbol], limit)
			}
		}
	}
}

func TestBuildExtendsCollisions(t *testing.T) {
	entries, err := buildWithAlias([]string{"source:b", "source:c", "source:a"}, MinLength, craftedAlias)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{FullRef: "source:a", Alias: "22222a"},
		{FullRef: "source:b", Alias: "22222b"},
		{FullRef: "source:c", Alias: "33333"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries = %#v, want %#v", entries, want)
	}

	lookupEntries := LookupEntries(entries)
	for _, wantEntry := range []Entry{
		{FullRef: "source:a", Alias: "22222"},
		{FullRef: "source:b", Alias: "22222"},
		{FullRef: "source:a", Alias: "22222a"},
		{FullRef: "source:b", Alias: "22222b"},
		{FullRef: "source:c", Alias: "33333"},
	} {
		if !containsEntry(lookupEntries, wantEntry) {
			t.Fatalf("lookup entries missing %#v in %#v", wantEntry, lookupEntries)
		}
	}
}

func TestBuildIsDeterministicAcrossInputOrder(t *testing.T) {
	forward, err := BuildSlice([]string{"telegram:msg/2", "telegram:msg/1", "telegram:msg/2"})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := BuildSlice([]string{"telegram:msg/2", "telegram:msg/1"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(forward, reverse) {
		t.Fatalf("entries differ by input order: %#v != %#v", forward, reverse)
	}
}

func craftedAlias(fullRef string, length int) string {
	aliases := map[string]string{
		"source:a": "22222a",
		"source:b": "22222b",
		"source:c": "33333c",
	}
	alias := aliases[fullRef]
	if length <= len(alias) {
		return alias[:length]
	}
	return alias + strings.Repeat("z", length-len(alias))
}

func containsEntry(entries []Entry, want Entry) bool {
	for _, entry := range entries {
		if entry == want {
			return true
		}
	}
	return false
}
