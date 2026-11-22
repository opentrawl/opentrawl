select count(*)
from chat_messages cm
join messages m on m.source_rowid = cm.message_rowid
{{MESSAGE_CHAT_FILTER}}
