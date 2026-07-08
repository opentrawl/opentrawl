package qa

func createMessagesFixture(path string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return execAll(db,
		`create table handle (ROWID integer primary key, id text not null, service text not null, uncanonicalized_id text)`,
		`create table chat (ROWID integer primary key, guid text not null, display_name text, chat_identifier text, service_name text, room_name text, is_archived integer)`,
		`create table chat_handle_join (chat_id integer, handle_id integer)`,
		`create table message (ROWID integer primary key, guid text not null, handle_id integer, date integer, service text, is_from_me integer, text text, attributedBody blob)`,
		`create table chat_message_join (chat_id integer, message_id integer)`,
		`create table message_attachment_join (message_id integer, attachment_id integer)`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (1, '+15550100', 'iMessage', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (2, '0015550100', 'SMS', '')`,
		`insert into handle(rowid, id, service, uncanonicalized_id) values (3, 'casey@example.com', 'iMessage', '')`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (1, 'chat-one', 'Casey Example', '+15550100', 'iMessage', '', 0)`,
		`insert into chat(rowid, guid, display_name, chat_identifier, service_name, room_name, is_archived) values (2, 'chat-two', 'Launch group', 'group-chat', 'SMS', 'Launch group', 0)`,
		`insert into chat_handle_join(chat_id, handle_id) values (1, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 1)`,
		`insert into chat_handle_join(chat_id, handle_id) values (2, 3)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (1, 'message-one', 1, 100, 'iMessage', 0, 'hello from Casey', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (2, 'message-two', 1, 200, 'SMS', 1, 'launch note from me', null)`,
		`insert into message(rowid, guid, handle_id, date, service, is_from_me, text, attributedBody) values (3, 'message-three', 3, 250, 'SMS', 0, 'launch reply from Casey', null)`,
		`insert into chat_message_join(chat_id, message_id) values (1, 1)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 2)`,
		`insert into chat_message_join(chat_id, message_id) values (2, 3)`,
	)
}
