package store

import (
	"context"
	"strings"
)

// GroupRecipientDisplayNames returns human names for active group members only
// when a stored sender identifier exactly matches the owner's participant or
// contact identifier.
func (s *Store) GroupRecipientDisplayNames(ctx context.Context, groupJID string) ([]string, error) {
	groupJID = strings.TrimSpace(groupJID)
	if groupJID == "" {
		return nil, nil
	}
	ownerIdentifiers, err := s.ownerIdentifierSet(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
select gp.user_jid,
       coalesce(c.jid, ''),
       coalesce(c.phone, ''),
       coalesce(c.username, ''),
       coalesce(c.lid, ''),
       coalesce(
         nullif(trim(c.full_name), ''),
         nullif(trim(gp.contact_name), ''),
         nullif(trim(c.business_name), ''),
         nullif(trim(c.first_name || ' ' || c.last_name), ''),
         nullif(trim(gp.first_name), ''),
         ''
       ) as display_name
from group_participants gp
join chats ch on cast(ch.id as text) = gp.group_jid and ch.kind = 'group'
left join contacts c on c.jid = gp.user_jid
where gp.group_jid = ?
  and gp.is_active != 0
order by lower(display_name), display_name`, groupJID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	seenDisplayNames := map[string]struct{}{}
	recipientDisplayNames := []string{}
	ownerParticipantFound := false
	for rows.Next() {
		var participantJID string
		var contactJID, contactPhone, contactUsername, contactLID, displayName string
		if err := rows.Scan(
			&participantJID,
			&contactJID,
			&contactPhone,
			&contactUsername,
			&contactLID,
			&displayName,
		); err != nil {
			return nil, err
		}
		if ownerIdentifiers.Contains(
			participantJID,
			contactJID,
			contactPhone,
			contactUsername,
			contactLID,
		) {
			ownerParticipantFound = true
			continue
		}
		displayName = cleanPeerName(
			displayName,
			participantJID,
			contactJID,
			contactPhone,
			contactUsername,
			contactLID,
		)
		if displayName == "" {
			continue
		}
		displayNameKey := strings.ToLower(displayName)
		if _, found := seenDisplayNames[displayNameKey]; found {
			continue
		}
		seenDisplayNames[displayNameKey] = struct{}{}
		recipientDisplayNames = append(recipientDisplayNames, displayName)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !ownerParticipantFound {
		return nil, nil
	}
	return recipientDisplayNames, nil
}
