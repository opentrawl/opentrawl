with
candidate_handles(candidate_idx, handle_rowid) as (
{{HANDLE_ROWS}}
),
owner_candidates(candidate_idx) as (
{{OWNER_ROWS}}
),
{{CANDIDATE_MESSAGES}}
select candidate_idx, count(distinct message_rowid), coalesce(max(date), 0)
from candidate_messages
group by candidate_idx
