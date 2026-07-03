{{WITH}}
select count(*)
from messages m
{{FTS_JOIN}}
where 1 = 1
{{FTS_FILTER}}
{{WHO_FILTER}}
{{TIME_FILTER}}
