select
  rowid,
  id,
  service,
  coalesce(uncanonicalized_id, ''),
  coalesce((
    select nullif(trim(c.display_name), '')
    from chat_handle_join chj
    join chat c on c.rowid = chj.chat_id
    where chj.handle_id = h.rowid
      and (select count(*) from chat_handle_join x where x.chat_id = chj.chat_id) = 1
      and nullif(trim(c.display_name), '') is not null
    order by c.rowid desc
    limit 1
  ), '') as display_name
from handle h
order by h.rowid
