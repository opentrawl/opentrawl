select
  'handle' as row_kind,
  source_rowid,
  coalesce(handle, ''),
  coalesce(display_name, ''),
  '',
  '',
  '',
  ''
from handles
union all
select
  'mapping' as row_kind,
  0,
  '',
  '',
  coalesce(kind, ''),
  coalesce(normalized_handle, ''),
  coalesce(contact_key, ''),
  coalesce(display_name, '')
from contact_mappings
order by row_kind, source_rowid, 5, 6
