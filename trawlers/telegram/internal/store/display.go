package store

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
)

// savedMessagesName is what Telegram calls the owner's self-chat. The
// importer stores that chat under the owner's raw numeric id with no name,
// so display resolves it here.
const savedMessagesName = "Saved Messages"

// deletedAccountName is what Telegram shows for a user peer whose names are
// all empty, which is how Telegram keeps a deleted account.
const deletedAccountName = "Deleted Account"

const displayContactColumns = `jid,coalesce(peer_type,''),coalesce(phone,''),coalesce(full_name,''),coalesce(first_name,''),coalesce(last_name,''),coalesce(business_name,''),coalesce(username,''),coalesce(lid,''),coalesce(about_text,''),coalesce(avatar_path,''),coalesce(updated_at,0)`

func (s *Store) humanizeMessages(ctx context.Context, messages []Message) error {
	contacts := map[string]Contact{}
	humanizeMessagesWithContacts(messages, func(jid string) Contact {
		contact, _ := s.contactForDisplay(ctx, contacts, jid)
		return contact
	})
	return nil
}

func humanizeMessagesWithContacts(messages []Message, contactForJID func(jid string) Contact) {
	for i := range messages {
		sender := contactForJID(messages[i].SenderJID)
		chat := contactForJID(messages[i].ChatJID)
		messages[i].SenderName = humanPeerName(messages[i].SenderName, sender, messages[i].SenderJID)
		if messages[i].FromMe &&
			strings.TrimSpace(messages[i].SenderJID) != "" &&
			strings.TrimSpace(messages[i].SenderJID) == strings.TrimSpace(messages[i].ChatJID) {
			messages[i].ChatName = savedMessagesName
			continue
		}
		messages[i].ChatName = humanPeerName(messages[i].ChatName, chat, messages[i].ChatJID)
		// Telegram leaves the sender of an incoming direct message empty:
		// the other person in the chat sent it.
		if !messages[i].FromMe && messages[i].SenderName == "" && strings.TrimSpace(messages[i].SenderJID) == "" && chat.PeerType == "user" {
			messages[i].SenderName = messages[i].ChatName
		}
	}
}

// nameSelfChat renders the owner's self-chat as Saved Messages in chat lists.
func (s *Store) nameSelfChat(ctx context.Context, chats []Chat) error {
	if len(chats) == 0 {
		return nil
	}
	for i := range chats {
		chatJID := strings.TrimSpace(chats[i].JID)
		if chatJID == "" {
			continue
		}
		var archivedMessageExists int
		err := s.db.QueryRowContext(
			ctx,
			`select 1 from messages where chat_jid = ? and sender_jid = ? and from_me = 1 limit 1`,
			chatJID,
			chatJID,
		).Scan(&archivedMessageExists)
		switch err {
		case nil:
			chats[i].Name = savedMessagesName
		case sql.ErrNoRows:
			continue
		default:
			return err
		}
	}
	return nil
}

// allContactsForDisplay reads every contact up front so a caller streaming
// messages from the same database never needs a second connection.
func (s *Store) allContactsForDisplay(ctx context.Context) (map[string]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `select `+displayContactColumns+` from contacts`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	contacts := map[string]Contact{}
	for rows.Next() {
		contact, err := scanDisplayContact(rows)
		if err != nil {
			return nil, err
		}
		contacts[strings.TrimSpace(contact.JID)] = contact
	}
	return contacts, rows.Err()
}

func (s *Store) contactForDisplay(ctx context.Context, cache map[string]Contact, jid string) (Contact, error) {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return Contact{}, nil
	}
	if contact, ok := cache[jid]; ok {
		return contact, nil
	}
	row := s.db.QueryRowContext(ctx, `select `+displayContactColumns+` from contacts where jid=?`, jid)
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
	if firstName := cleanPeerFirstName(contact.FirstName, contact); firstName != "" {
		return firstName
	}
	if contact.PeerType == "user" && strings.TrimSpace(contact.LastName) == "" && strings.TrimSpace(contact.BusinessName) == "" {
		return deletedAccountName
	}
	return ""
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
	if name == "" || name == "..." || strings.EqualFold(name, "unknown") || looksLikePhone(name) {
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
