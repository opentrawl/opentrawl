select exists(
  select 1
  from chat_messages
  join messages on messages.source_rowid = chat_messages.message_rowid
  join chats on chats.source_rowid = chat_messages.chat_rowid
)
