package store

func whoCandidateAliasesQuery() string {
	senderContact := contactJIDPredicate("c", "m.sender_jid")
	chatContact := contactJIDPredicate("c", "ch.jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select participant_key as identity_key, participant_key, display_name, '' as identifier, name_kind
from (
select '` + ownerWhoKey + `' as participant_key, 'me' as display_name, 'owner' as name_kind
where exists (select 1 from messages where from_me = 1)
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end as participant_key, m.sender_name as display_name, 'push' as name_kind
from messages m
left join contacts c on ` + senderContact + `
where m.from_me = 0 and trim(m.sender_name) <> ''
union all
select 'jid:' || jid, full_name, 'contact_full'
from contacts
where trim(jid) <> '' and trim(full_name) <> ''
union all
select 'jid:' || jid, business_name, 'other'
from contacts
where trim(jid) <> '' and trim(business_name) <> ''
union all
select 'jid:' || jid, trim(first_name || ' ' || last_name), 'other'
from contacts
where trim(jid) <> '' and trim(first_name || ' ' || last_name) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), ch.name, 'other'
from chats ch
left join contacts c on ` + chatContact + `
where ch.kind <> 'group' and trim(ch.jid) <> '' and trim(ch.name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.contact_name, 'other'
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> '' and trim(gp.contact_name) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.first_name, 'other'
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> '' and trim(gp.first_name) <> ''
union all
select 'jid:' || c.jid, c.full_name, 'contact_full'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.full_name) <> ''
union all
select 'jid:' || c.jid, c.business_name, 'other'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.business_name) <> ''
union all
select 'jid:' || c.jid, trim(c.first_name || ' ' || c.last_name), 'other'
from group_participants gp
join contacts c on ` + groupContact + `
where trim(c.jid) <> '' and trim(c.first_name || ' ' || c.last_name) <> ''
)
where trim(participant_key) <> '' and trim(display_name) <> ''
union all
select participant_key as identity_key, participant_key, '' as display_name, identifier, '' as name_kind
from (
select '` + ownerWhoKey + `' as participant_key, 'me' as identifier
where exists (select 1 from messages where from_me = 1)
union all
select 'jid:' || jid as participant_key, jid as identifier from contacts where trim(jid) <> ''
union all
select 'jid:' || jid, phone from contacts where trim(jid) <> '' and trim(phone) <> ''
union all
select 'jid:' || jid, username from contacts where trim(jid) <> '' and trim(username) <> ''
union all
select 'jid:' || jid, case when substr(username, 1, 1) = '@' then username else '@' || username end from contacts where trim(jid) <> '' and trim(username) <> ''
union all
select 'jid:' || jid, lid from contacts where trim(jid) <> '' and trim(lid) <> ''
union all
select 'jid:' || jid, lid || '@lid' from contacts where trim(jid) <> '' and trim(lid) <> ''
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end, m.sender_jid
from messages m
left join contacts c on ` + senderContact + `
where trim(m.sender_jid) <> ''
union all
select 'jid:' || coalesce(c.jid, ch.jid), ch.jid
from chats ch
left join contacts c on ` + chatContact + `
where ch.kind <> 'group' and trim(ch.jid) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), gp.user_jid
from group_participants gp
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> ''
)
where trim(participant_key) <> '' and trim(identifier) <> ''`
}

func whoCandidateStatsQuery() string {
	senderContact := contactJIDPredicate("c", "m.sender_jid")
	chatContact := contactJIDPredicate("c", "ch.jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select participant_key, max(ts) as last_seen, count(distinct source_pk) as messages
from (
select '` + ownerWhoKey + `' as participant_key, source_pk, ts
from messages
where from_me = 1
union all
select case when trim(m.sender_jid) <> '' then 'jid:' || coalesce(c.jid, m.sender_jid) else 'sender:' || trim(m.sender_name) end as participant_key, m.source_pk, m.ts
from messages m
left join contacts c on ` + senderContact + `
where m.from_me = 0 and (trim(m.sender_jid) <> '' or trim(m.sender_name) <> '')
union all
select 'jid:' || coalesce(c.jid, ch.jid), m.source_pk, m.ts
from messages m
join chats ch on ch.jid = m.chat_jid and ch.kind <> 'group'
left join contacts c on ` + chatContact + `
where trim(ch.jid) <> ''
union all
select 'jid:' || coalesce(c.jid, gp.user_jid), m.source_pk, m.ts
from messages m
join group_participants gp on gp.group_jid = m.chat_jid
left join contacts c on ` + groupContact + `
where trim(gp.user_jid) <> ''
)
where trim(participant_key) <> ''
group by participant_key`
}

func whoMessageParticipantKeysQuery(prefix string) string {
	senderContact := contactJIDPredicate("c", prefix+"sender_jid")
	chatContact := contactJIDPredicate("c", prefix+"chat_jid")
	groupContact := contactJIDPredicate("c", "gp.user_jid")
	return `
select case when trim(` + prefix + `sender_jid) <> '' then 'jid:' || coalesce((select c.jid from contacts c where ` + senderContact + ` limit 1), ` + prefix + `sender_jid) else 'sender:' || trim(` + prefix + `sender_name) end as participant_key
where ` + prefix + `from_me = 0 and (trim(` + prefix + `sender_jid) <> '' or trim(` + prefix + `sender_name) <> '')
union all
select '` + ownerWhoKey + `'
where ` + prefix + `from_me = 1
union all
select 'jid:' || coalesce(c.jid, ch.jid)
from chats ch
left join contacts c on ` + chatContact + `
where ch.jid = ` + prefix + `chat_jid and ch.kind <> 'group'
union all
select 'jid:' || coalesce(c.jid, gp.user_jid)
from group_participants gp
left join contacts c on ` + groupContact + `
where gp.group_jid = ` + prefix + `chat_jid`
}

func contactJIDPredicate(contactAlias, jidExpr string) string {
	return contactAlias + ".jid = " + jidExpr + " or " + contactAlias + ".lid = " + jidExpr + " or " + contactAlias + ".lid || '@lid' = " + jidExpr
}
