package index

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/openclaw/clawdex/internal/model"
)

func (s Store) stageUnmatchedContacts(contacts []model.SourceContact, dryRun bool) ([]model.ImportChange, error) {
	path := s.Repo.UnmatchedContactsPath()
	lines, seen, err := readUnmatchedContacts(path)
	if err != nil {
		return nil, err
	}
	var changes []model.ImportChange
	entries := unmatchedContactEntries(lines)
	remove := map[int]bool{}
	changed := false
	for _, contact := range contacts {
		line := unmatchedContactLine(contact)
		if line == "" {
			continue
		}
		identity := unmatchedContactIdentityFromContact(contact)
		if identity.valid() {
			matches := matchingUnmatchedContactIndexes(entries, identity, remove)
			if len(matches) > 0 {
				first := matches[0]
				lineChanged := lines[first] != line
				if lineChanged {
					delete(seen, lines[first])
					lines[first] = line
					seen[line] = true
					entries[first] = identity
					changed = true
				}
				for _, index := range matches[1:] {
					delete(seen, lines[index])
					remove[index] = true
					changed = true
				}
				if lineChanged || len(matches) > 1 {
					changes = append(changes, model.ImportChange{Action: "stage", Name: contact.Name, Source: contact, Path: path})
				}
				continue
			}
		}
		if seen[line] {
			continue
		}
		lines = append(lines, line)
		seen[line] = true
		entries[len(lines)-1] = identity
		changed = true
		changes = append(changes, model.ImportChange{Action: "stage", Name: contact.Name, Source: contact, Path: path})
	}
	if dryRun || !changed {
		return changes, nil
	}
	if len(remove) > 0 {
		kept := lines[:0]
		for i, line := range lines {
			if !remove[i] {
				kept = append(kept, line)
			}
		}
		lines = kept
	}
	return changes, writeUnmatchedContacts(path, lines)
}

type unmatchedContactIdentity struct {
	source      string
	identifiers map[string]bool
}

func (i unmatchedContactIdentity) valid() bool {
	return i.source != "" && len(i.identifiers) > 0
}

func unmatchedContactEntries(lines []string) map[int]unmatchedContactIdentity {
	entries := map[int]unmatchedContactIdentity{}
	for i, line := range lines {
		identity := unmatchedContactIdentityFromLine(line)
		if identity.valid() {
			entries[i] = identity
		}
	}
	return entries
}

func matchingUnmatchedContactIndexes(entries map[int]unmatchedContactIdentity, identity unmatchedContactIdentity, removed map[int]bool) []int {
	var matches []int
	for index, existing := range entries {
		if removed[index] || !unmatchedContactIdentitiesOverlap(existing, identity) {
			continue
		}
		matches = append(matches, index)
	}
	sort.Ints(matches)
	return matches
}

func unmatchedContactIdentitiesOverlap(a, b unmatchedContactIdentity) bool {
	if !a.valid() || !b.valid() || a.source != b.source {
		return false
	}
	for identifier := range a.identifiers {
		if b.identifiers[identifier] {
			return true
		}
	}
	return false
}

func readUnmatchedContacts(path string) ([]string, map[string]bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []string{
			"# Unmatched contacts",
			"",
			"Promote these by editing or adding person markdown, then rerun the import.",
			"",
		}, map[string]bool{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			seen[line] = true
		}
	}
	return lines, seen, nil
}

func writeUnmatchedContacts(path string, lines []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".unmatched.md.tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func unmatchedContactLine(contact model.SourceContact) string {
	source := strings.TrimSpace(contact.Source)
	name := strings.TrimSpace(contact.Name)
	if source == "" || name == "" {
		return ""
	}
	accounts := flattenedAccounts(contact.Accounts)
	return "- source=" + strconv.Quote(source) +
		" name=" + strconv.Quote(name) +
		" phones=" + quoteList(contactValues(contact.Phones, model.NormalizePhone)) +
		" emails=" + quoteList(contactValues(contact.Emails, model.NormalizeEmail)) +
		" accounts=" + quoteList(accounts)
}

func unmatchedContactIdentityFromContact(contact model.SourceContact) unmatchedContactIdentity {
	identity := unmatchedContactIdentity{source: strings.ToLower(strings.TrimSpace(contact.Source)), identifiers: map[string]bool{}}
	for _, phone := range contactValues(contact.Phones, model.NormalizePhone) {
		identity.identifiers["phone:"+phone] = true
	}
	for _, email := range contactValues(contact.Emails, model.NormalizeEmail) {
		identity.identifiers["email:"+email] = true
	}
	for _, account := range flattenedAccounts(contact.Accounts) {
		identity.identifiers["account:"+account] = true
	}
	return identity
}

func unmatchedContactIdentityFromLine(line string) unmatchedContactIdentity {
	fields, ok := parseUnmatchedContactFields(line)
	if !ok {
		return unmatchedContactIdentity{}
	}
	identity := unmatchedContactIdentity{source: strings.ToLower(strings.TrimSpace(fields["source"])), identifiers: map[string]bool{}}
	for _, phone := range splitUnmatchedList(fields["phones"]) {
		if key := model.NormalizePhone(phone); key != "" {
			identity.identifiers["phone:"+key] = true
		}
	}
	for _, email := range splitUnmatchedList(fields["emails"]) {
		if key := model.NormalizeEmail(email); key != "" {
			identity.identifiers["email:"+key] = true
		}
	}
	for _, account := range splitUnmatchedList(fields["accounts"]) {
		account = strings.ToLower(strings.TrimSpace(account))
		if account != "" {
			identity.identifiers["account:"+account] = true
		}
	}
	return identity
}

func parseUnmatchedContactFields(line string) (map[string]string, bool) {
	if !strings.HasPrefix(line, "- ") {
		return nil, false
	}
	fields := map[string]string{}
	rest := strings.TrimSpace(strings.TrimPrefix(line, "- "))
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			return nil, false
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		if !strings.HasPrefix(rest, `"`) {
			return nil, false
		}
		value, tail, ok := readUnmatchedQuotedValue(rest)
		if !ok {
			return nil, false
		}
		fields[key] = value
		rest = strings.TrimSpace(tail)
	}
	return fields, true
}

func readUnmatchedQuotedValue(text string) (string, string, bool) {
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			value, err := strconv.Unquote(text[:i+1])
			if err != nil {
				return "", "", false
			}
			return value, text[i+1:], true
		}
	}
	return "", "", false
}

func splitUnmatchedList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contactValues(values []model.ContactValue, normalize func(string) string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := normalize(value.Value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func flattenedAccounts(accounts map[string][]string) []string {
	var out []string
	for service, values := range cleanAccounts(accounts) {
		for _, value := range values {
			out = append(out, service+":"+strings.ToLower(strings.TrimSpace(value)))
		}
	}
	sort.Strings(out)
	return out
}

func quoteList(values []string) string {
	return strconv.Quote(strings.Join(values, ","))
}
