package store

import (
	"context"
	"strings"
	"unicode"
)

// savedMessagesName is what Telegram calls the owner's self-chat. The
// importer stores that chat under the owner's raw numeric id with no name,
// so display resolves it here.
const savedMessagesName = "Saved Messages"

func (s *Store) humanizeMessages(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	selfJID, err := s.selfChatJID(ctx)
	if err != nil {
		return err
	}
	contacts := map[string]Contact{}
	for i := range messages {
		sender, err := s.contactForDisplay(ctx, contacts, messages[i].SenderJID)
		if err != nil {
			return err
		}
		chat, err := s.contactForDisplay(ctx, contacts, messages[i].ChatJID)
		if err != nil {
			return err
		}
		messages[i].SenderName = humanPeerName(messages[i].SenderName, sender, messages[i].SenderJID)
		if selfJID != "" && strings.TrimSpace(messages[i].ChatJID) == selfJID {
			messages[i].ChatName = savedMessagesName
			continue
		}
		messages[i].ChatName = humanPeerName(messages[i].ChatName, chat, messages[i].ChatJID)
	}
	return nil
}

// selfChatJID identifies the owner's self-chat: the chat whose id equals the
// owner's own id, which the archive stores as the one distinct sender_jid on
// from_me messages (exact identifier equality, no name heuristics). Returns
// "" when the archive has no unambiguous owner.
func (s *Store) selfChatJID(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx, `select distinct trim(sender_jid) from messages where from_me = 1 and trim(coalesce(sender_jid, '')) <> '' limit 2`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var owners []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return "", err
		}
		owners = append(owners, value)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(owners) != 1 {
		return "", nil
	}
	return owners[0], nil
}

// nameSelfChat renders the owner's self-chat as Saved Messages in chat lists.
func (s *Store) nameSelfChat(ctx context.Context, chats []Chat) error {
	if len(chats) == 0 {
		return nil
	}
	selfJID, err := s.selfChatJID(ctx)
	if err != nil || selfJID == "" {
		return err
	}
	for i := range chats {
		if strings.TrimSpace(chats[i].JID) == selfJID {
			chats[i].Name = savedMessagesName
		}
	}
	return nil
}

func (s *Store) contactForDisplay(ctx context.Context, cache map[string]Contact, jid string) (Contact, error) {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return Contact{}, nil
	}
	if contact, ok := cache[jid]; ok {
		return contact, nil
	}
	row := s.db.QueryRowContext(ctx, `select jid,coalesce(peer_type,''),coalesce(phone,''),coalesce(full_name,''),coalesce(first_name,''),coalesce(last_name,''),coalesce(business_name,''),coalesce(username,''),coalesce(lid,''),coalesce(about_text,''),coalesce(avatar_path,''),coalesce(updated_at,0) from contacts where jid=?`, jid)
	contact, err := scanDisplayContact(row)
	if err != nil {
		cache[jid] = Contact{}
		return Contact{}, nil
	}
	cache[jid] = contact
	return contact, nil
}

func scanDisplayContact(scanner messageScanner) (Contact, error) {
	var contact Contact
	var updatedAt int64
	if err := scanner.Scan(&contact.JID, &contact.PeerType, &contact.Phone, &contact.FullName, &contact.FirstName, &contact.LastName, &contact.BusinessName, &contact.Username, &contact.LID, &contact.AboutText, &contact.AvatarPath, &updatedAt); err != nil {
		return Contact{}, err
	}
	contact.UpdatedAt = fromUnix(updatedAt)
	return contact, nil
}

func humanPeerName(stored string, contact Contact, refs ...string) string {
	refs = append(refs, contact.JID, contact.Phone, contact.Username, contact.LID)
	displayName := cleanPeerName(stored, refs...)
	if displayName != "" {
		return displayName
	}
	for _, candidate := range []string{
		contact.FullName,
		contact.BusinessName,
	} {
		if name := cleanPeerName(candidate, refs...); name != "" {
			return name
		}
	}
	if username := cleanPeerUsername(contact.Username); username != "" {
		return username
	}
	return cleanPeerFirstName(contact.FirstName, contact)
}

func ContactDisplayName(contact Contact) string {
	if name := cleanContactDisplayName(contact.FullName, contact); name != "" {
		return name
	}
	return cleanContactDisplayName(strings.TrimSpace(contact.FirstName+" "+contact.LastName), contact)
}

func cleanContactDisplayName(name string, contact Contact) string {
	name = strings.Join(strings.Fields(name), " ")
	switch {
	case name == "":
		return ""
	case sameContactText(name, contact.Phone):
		return ""
	case sameContactText(name, contact.JID):
		return ""
	case sameContactText(name, contact.Username):
		return ""
	case sameContactText(name, contact.LID):
		return ""
	case strings.HasPrefix(name, "@"):
		return ""
	case looksLikePhone(name):
		return ""
	default:
		return name
	}
}

func sameContactText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

func cleanPeerUsername(username string) string {
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" || looksLikePhone(username) {
		return ""
	}
	return username
}

func cleanPeerFirstName(firstName string, contact Contact) string {
	firstName = cleanPeerName(firstName, contact.JID, contact.Phone, contact.Username, contact.LID)
	if firstName == "" || looksLikePhone(firstName) {
		return ""
	}
	return firstName
}

func cleanPeerName(name string, refs ...string) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || strings.EqualFold(name, "unknown") || looksLikePhone(name) {
		return ""
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref != "" && strings.EqualFold(name, ref) {
			return ""
		}
	}
	return name
}

func looksLikePhone(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}
