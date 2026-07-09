package shortref

import (
	"crypto/sha256"
	"fmt"
	"iter"
	"math"
	"math/big"
	"sort"
	"strings"
)

const (
	MinLength        = 5
	MaxDefaultLength = 10

	// Alphabet has 31 characters: digits 2 to 9, then lowercase letters
	// without i, l and o.
	Alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
)

const (
	aliasHashPrefix             = "sr1|"
	maxEncodedAliasLength       = 52
	negligibleCollisionPairRate = 0.01
)

type Entry struct {
	FullRef string
	Alias   string
}

type aliasFunc func(fullRef string, length int) string

func Alias(fullRef string, length int) string {
	if length <= 0 {
		return ""
	}
	encoded := encodedAlias(fullRef)
	if length > len(encoded) {
		return encoded
	}
	return encoded[:length]
}

// DefaultLength uses birthday arithmetic to pick the smallest useful prefix.
//
// For n refs and m possible aliases, expected collision pairs are
// n*(n-1)/(2*m). Dividing by n gives the expected collision pair rate per ref:
// (n-1)/(2*m). A length is the default once that rate is below 1%. Colliding
// refs still extend during index building, so this only chooses the starting
// display length.
func DefaultLength(corpusSize int) int {
	if corpusSize <= 1 {
		return MinLength
	}
	n := float64(corpusSize)
	for length := MinLength; length <= MaxDefaultLength; length++ {
		aliases := math.Pow(float64(len(Alphabet)), float64(length))
		pairRate := (n - 1) / (2 * aliases)
		if pairRate <= negligibleCollisionPairRate {
			return length
		}
	}
	return MaxDefaultLength
}

func ValidAlias(alias string) bool {
	if len(alias) < MinLength || len(alias) > maxEncodedAliasLength {
		return false
	}
	for i := 0; i < len(alias); i++ {
		if !strings.ContainsRune(Alphabet, rune(alias[i])) {
			return false
		}
	}
	return true
}

func Build(fullRefs iter.Seq[string]) ([]Entry, error) {
	if fullRefs == nil {
		return nil, nil
	}
	refs := make([]string, 0)
	for fullRef := range fullRefs {
		refs = append(refs, fullRef)
	}
	return buildWithAlias(refs, 0, Alias)
}

func BuildSlice(fullRefs []string) ([]Entry, error) {
	return Build(func(yield func(string) bool) {
		for _, fullRef := range fullRefs {
			if !yield(fullRef) {
				return
			}
		}
	})
}

// BuildSliceAvoidingAliases builds display entries whose aliases do not collide
// with reservedAliases.
func BuildSliceAvoidingAliases(fullRefs []string, reservedAliases map[string]struct{}) ([]Entry, error) {
	return buildWithAliasAvoidingAliases(fullRefs, 0, Alias, reservedAliases)
}

func LookupEntries(displayEntries []Entry) []Entry {
	seen := make(map[Entry]struct{})
	lookupEntries := make([]Entry, 0, len(displayEntries))
	for _, entry := range displayEntries {
		for length := MinLength; length <= len(entry.Alias); length++ {
			lookupEntry := Entry{FullRef: entry.FullRef, Alias: entry.Alias[:length]}
			if _, ok := seen[lookupEntry]; ok {
				continue
			}
			seen[lookupEntry] = struct{}{}
			lookupEntries = append(lookupEntries, lookupEntry)
		}
	}
	sort.Slice(lookupEntries, func(i, j int) bool {
		if lookupEntries[i].Alias == lookupEntries[j].Alias {
			return lookupEntries[i].FullRef < lookupEntries[j].FullRef
		}
		return lookupEntries[i].Alias < lookupEntries[j].Alias
	})
	return lookupEntries
}

func buildWithAlias(fullRefs []string, startLength int, alias aliasFunc) ([]Entry, error) {
	return buildWithAliasAvoidingAliases(fullRefs, startLength, alias, nil)
}

func buildWithAliasAvoidingAliases(fullRefs []string, startLength int, alias aliasFunc, reservedAliases map[string]struct{}) ([]Entry, error) {
	refs := uniqueSorted(fullRefs)
	if len(refs) == 0 {
		return nil, nil
	}
	if startLength <= 0 {
		startLength = DefaultLength(len(refs))
	} else if startLength < MinLength {
		startLength = MinLength
	}

	lengths := make(map[string]int, len(refs))
	for _, ref := range refs {
		lengths[ref] = startLength
	}

	for {
		groups := make(map[string][]string, len(refs))
		for _, ref := range refs {
			length := lengths[ref]
			if length > maxEncodedAliasLength {
				return nil, fmt.Errorf("short ref collision did not resolve by %d characters", maxEncodedAliasLength)
			}
			currentAlias := alias(ref, length)
			groups[currentAlias] = append(groups[currentAlias], ref)
		}

		extended := false
		for currentAlias, group := range groups {
			_, reserved := reservedAliases[currentAlias]
			if len(group) < 2 && !reserved {
				continue
			}
			for _, ref := range group {
				lengths[ref]++
			}
			extended = true
		}
		if !extended {
			break
		}
	}

	entries := make([]Entry, 0, len(refs))
	for _, ref := range refs {
		length := lengths[ref]
		entries = append(entries, Entry{FullRef: ref, Alias: alias(ref, length)})
	}
	return entries, nil
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	unique := make([]string, 0, len(seen))
	for value := range seen {
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func encodedAlias(fullRef string) string {
	digest := sha256.Sum256([]byte(aliasHashPrefix + fullRef))
	return encodeBase31(digest[:])
}

func encodeBase31(raw []byte) string {
	value := new(big.Int).SetBytes(raw)

	base := big.NewInt(int64(len(Alphabet)))
	mod := new(big.Int)
	zero := big.NewInt(0)
	encoded := make([]byte, maxEncodedAliasLength)
	for i := range encoded {
		if value.Cmp(zero) == 0 {
			encoded[i] = Alphabet[0]
			continue
		}
		// Aliases use the low-order base-31 digits first. The high-order digit
		// of a 256-bit hash only covers part of the alphabet because 2^256 sits
		// between 31^51 and 31^52; reading from the low end keeps displayed
		// prefix positions spread across the full alphabet.
		value.DivMod(value, base, mod)
		encoded[i] = Alphabet[mod.Int64()]
	}
	return string(encoded)
}
