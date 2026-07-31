insert into messages(
  source_rowid,
  guid,
  handle_rowid,
  date,
  service,
  account,
  is_from_me,
  text,
  has_attachments,
  is_read,
  is_forward,
  item_type,
  group_action_type,
  message_action_type,
  associated_message_type
) values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
