package archive

import (
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/addressbook"
	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/messages"
)

const ownerDisplayName = "me"

type ownerHandleKey struct {
	kind   string
	handle string
}

func applyOwnerHandles(data *messages.ArchiveData, names []addressbook.ContactName, mappings []ContactMapping) []OwnerHandle {
	owner := initialOwnerHandles(data.Messages, names)
	expandOwnerHandles(owner, mappings)
	if len(owner) == 0 {
		return nil
	}
	for i := range data.Handles {
		key, ok := normalizeOwnerHandle(data.Handles[i].ID)
		if ok && owner[key] {
			data.Handles[i].DisplayName = ownerDisplayName
		}
	}
	return sortedOwnerHandles(owner)
}

func initialOwnerHandles(rows []messages.Message, names []addressbook.ContactName) map[ownerHandleKey]bool {
	owner := map[ownerHandleKey]bool{}
	for _, message := range rows {
		if !message.IsFromMe {
			continue
		}
		if key, ok := normalizeOwnerHandle(message.Account); ok {
			owner[key] = true
		}
	}
	for _, name := range names {
		if !name.IsMe {
			continue
		}
		key := ownerHandleKey{kind: strings.TrimSpace(name.Kind), handle: strings.TrimSpace(name.Handle)}
		if key.kind != "" && key.handle != "" {
			owner[key] = true
		}
	}
	return owner
}

func expandOwnerHandles(owner map[ownerHandleKey]bool, mappings []ContactMapping) {
	if len(owner) == 0 || len(mappings) == 0 {
		return
	}
	ownerContactKeys := map[string]bool{}
	for _, mapping := range mappings {
		key := ownerHandleKey{kind: mapping.Kind, handle: mapping.NormalizedHandle}
		contactKey := strings.TrimSpace(mapping.ContactKey)
		if owner[key] && contactKey != "" {
			ownerContactKeys[contactKey] = true
		}
	}
	for _, mapping := range mappings {
		contactKey := strings.TrimSpace(mapping.ContactKey)
		if contactKey == "" || !ownerContactKeys[contactKey] {
			continue
		}
		key := ownerHandleKey{kind: mapping.Kind, handle: mapping.NormalizedHandle}
		if key.kind != "" && key.handle != "" {
			owner[key] = true
		}
	}
}

func normalizeOwnerHandle(raw string) (ownerHandleKey, bool) {
	for _, candidate := range ownerHandleCandidates(raw) {
		kind, handle, ok := addressbook.NormalizeHandle(candidate)
		if ok {
			return ownerHandleKey{kind: kind, handle: handle}, true
		}
	}
	return ownerHandleKey{}, false
}

// Stripped forms come first: Messages accounts look like
// "E:user@host" or "P:+123", and normalizing the raw string first
// would accept "e:user@host" as an email that never matches the
// handles table.
func ownerHandleCandidates(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var candidates []string
	lower := strings.ToLower(raw)
	for _, prefix := range []string{"mailto:", "tel:", "e:", "p:"} {
		if strings.HasPrefix(lower, prefix) {
			candidates = append(candidates, strings.TrimSpace(raw[len(prefix):]))
		}
	}
	if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) {
		candidates = append(candidates, strings.TrimSpace(raw[idx+1:]))
	}
	return append(candidates, raw)
}

func sortedOwnerHandles(owner map[ownerHandleKey]bool) []OwnerHandle {
	out := make([]OwnerHandle, 0, len(owner))
	for key := range owner {
		if key.kind == "" || key.handle == "" {
			continue
		}
		out = append(out, OwnerHandle{Kind: key.kind, NormalizedHandle: key.handle})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].NormalizedHandle < out[j].NormalizedHandle
	})
	return out
}
