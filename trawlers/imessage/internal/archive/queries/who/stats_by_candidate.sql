with
candidate_handles(candidate_idx, handle_rowid) as (
{{HANDLE_ROWS}}
),
owner_candidates(candidate_idx) as (
{{OWNER_ROWS}}
),
candidate_messages(candidate_idx, message_rowid, date) as (
  select ch.candidate_idx, m.source_rowid, m.date
  from messages m
  join candidate_handles ch on ch.handle_rowid = m.handle_rowid
  union all
  select ch.candidate_idx, m.source_rowid, m.date
  from chat_participants cp
  join candidate_handles ch on ch.handle_rowid = cp.handle_rowid
  join chat_messages cm on cm.chat_rowid = cp.chat_rowid
  join messages m on m.source_rowid = cm.message_rowid
  union all
  select oc.candidate_idx, m.source_rowid, m.date
  from messages m
  join owner_candidates oc
  where m.is_from_me = 1
)
select candidate_idx, count(distinct message_rowid), coalesce(max(date), 0)
from candidate_messages
group by candidate_idx
