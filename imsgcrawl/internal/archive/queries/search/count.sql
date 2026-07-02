{{WITH}}
select count(*)
from messages_fts
join messages m on m.source_rowid = messages_fts.source_rowid
where messages_fts match ?
{{WHO_FILTER}}
