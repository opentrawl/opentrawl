select trim(h.handle),
  coalesce(nullif(trim(h.display_name), ''), trim(h.handle))
from chat_participants cp
join handles h on h.source_rowid = cp.handle_rowid
where cp.chat_rowid = ?
  and nullif(trim(h.handle), '') is not null
order by h.handle
