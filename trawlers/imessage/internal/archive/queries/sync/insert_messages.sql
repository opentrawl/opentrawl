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
  is_read
) values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
