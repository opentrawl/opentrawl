select coalesce(min(nullif(date, 0)), 0)
from messages
