'''
This file is used to migrate all databases used in 1.5 (0.5) to 1.6,
as the database scheme used is extremely tedious to manually migrate.

The databases to be migrated (preferences.db, notifications.db and statistics.db)
should all be in the same folder as this script. These will be merged into one
database, 'launchbot-data.db'.
'''

import sqlite3
import os
import time


def migrate_notify_db(old_db: str):
	'''
	Migrate the notifications.db file's contents (notify-table)
	into a dictionary, which we'll expand on and later insert into
	our new db file.
	'''
	print('Starting migration...')
	# Establish connection
	conn = sqlite3.connect(old_db)
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	cursor.execute('SELECT * FROM notify')
	query_return = cursor.fetchall()

	chats_dict = {}
	for row in query_return:
		chat_id = row['chat']
		if chat_id not in chats_dict.keys():
			chats_dict[chat_id] = {}
			chats_dict[chat_id]['enabled_notifications'] = ''
			chats_dict[chat_id]['disabled_notifications'] = ''
			chats_dict[chat_id]['time_zone_offset'] = None
			chats_dict[chat_id]['time_zone_str'] = None
			chats_dict[chat_id]['notify_time_pref'] = '1,1,1,1'

		lsp_name = row['keyword']
		lsp_state = row['enabled']

		if lsp_state == 1:
			chats_dict[chat_id]['enabled_notifications'] += lsp_name + ','
		else:
			chats_dict[chat_id]['disabled_notifications'] += lsp_name + ','

	for chat_id, pref_dict in chats_dict.items():
		try:
			if pref_dict['enabled_notifications'][-1] == ',':
				pref_dict['enabled_notifications'] = pref_dict['enabled_notifications'][0:-1]
		except IndexError:
			pass

		try:
			if pref_dict['disabled_notifications'][-1] == ',':
				pref_dict['disabled_notifications'] = pref_dict['disabled_notifications'][0:-1]
		except IndexError:
			pass

	conn.close()
	return chats_dict


def migrate_preferences(old_db: str, chats_dict: dict):
	'''
	Migrate preferences from the old database to the chats_dict
	dict, which we'll later insert.
	'''
	# Establish connection
	conn = sqlite3.connect(old_db)
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	cursor.execute('SELECT * FROM preferences')
	query_return = cursor.fetchall()

	for row in query_return:
		chat = row['chat']

		if chat not in chats_dict:
			chats_dict[chat] = {}
			chats_dict[chat]['enabled_notifications'] = ''
			chats_dict[chat]['disabled_notifications'] = ''
			chats_dict[chat]['time_zone_offset'] = None
			chats_dict[chat]['time_zone_str'] = None
			chats_dict[chat]['notify_time_pref'] = '1,1,1,1'

		tz_offset = row['timezone']
		tz_str = row['timezone_str']
		notif_pref = row['notifications']

		chats_dict[chat]['time_zone_offset'] = tz_offset
		chats_dict[chat]['time_zone_str'] = tz_str
		chats_dict[chat]['notify_time_pref'] = notif_pref

	conn.close()

	return chats_dict


def migrate_statistics(old_db: str):
	'''
	Migrate existing statistics to the newly created
	launchbot-data.db file, into its own table.
	'''
	# Establish connection
	conn = sqlite3.connect(old_db)
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# pull old data
	cursor.execute('SELECT * FROM stats')
	query_return = cursor.fetchall()
	stats_row = query_return[0]
	conn.close()

	# conn to stats table
	conn = sqlite3.connect('launchbot-data.db')
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# create table
	cursor.execute('''CREATE TABLE stats
		(notifications INT, api_requests INT, db_updates INT, commands INT,
		data INT, last_api_update INT, PRIMARY KEY (notifications, api_requests))''')

	cursor.execute('''INSERT INTO stats
		(notifications, api_requests, db_updates, commands, data, last_api_update)
		VALUES (?, ?, ?, ?, ?, ?)''', (stats_row["notifications"], stats_row["API_requests"],
		stats_row["db_updates"], stats_row["commands"], stats_row["data"], None))

	conn.commit()
	conn.close()
	print('✅ Stats inserted!')


def insert_into_new_db(new_db: str, chats_dict: dict):
	'''
	Insert the generated new chats-table into the new
	database.
	'''
	# remove old db if exists
	if os.path.isfile(new_db):
		os.remove(new_db)

	# connect to the new database
	conn = sqlite3.connect(new_db)
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# create new chats table
	cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'chats'))
	if len(cursor.fetchall()) == 0:
		print("⚠️ Chats table doesn't exists: creating...")

		cursor.execute('''
			CREATE TABLE chats (chat TEXT, subscribed_since INT, time_zone TEXT,
			time_zone_str TEXT, command_permissions TEXT, postpone_notify BOOLEAN,
			notify_time_pref TEXT, enabled_notifications TEXT, disabled_notifications TEXT,
			PRIMARY KEY (chat))
			''')

		cursor.execute("CREATE INDEX chatenabled ON chats (chat, enabled_notifications)")
		cursor.execute("CREATE INDEX chatdisabled ON chats (chat, disabled_notifications)")
		conn.commit()
		print('✅ Chats table created!')

	# insert all chats
	for chat, pref_dict in chats_dict.items():
		cursor.execute('''INSERT INTO chats (chat, enabled_notifications, disabled_notifications,
			subscribed_since, time_zone, time_zone_str, notify_time_pref)
			VALUES (?, ?, ?, ?, ?, ?, ?)''',
			(chat, pref_dict['enabled_notifications'], pref_dict['disabled_notifications'],
				int(time.time()), pref_dict['time_zone_offset'], pref_dict['time_zone_str'],
				pref_dict['notify_time_pref']))

	conn.commit()
	conn.close()


if __name__ == '__main__':
	#notif_db_dir = input('Enter notifications.db location (incl. db file): ')
	#pref_db_dir = input('Enter preferences.db location (incl. db file): ')

	notif_db_dir = 'notifications.db'
	pref_db_dir = 'preferences.db'
	stats_db_dir = 'statistics.db'

	use_existing = None
	while use_existing is None:
		inp = input('Use existing database file (y/n): ').lower()
		if inp not in ('y', 'yes', 'n', 'no'):
			continue

		use_existing = bool(inp.lower() in ('y', 'yes'))

	new_db_dir = 'launchbot-data.db'
	full_db = migrate_notify_db(old_db=notif_db_dir)
	full_db = migrate_preferences(old_db=pref_db_dir, chats_dict=full_db)
	insert_into_new_db(new_db=new_db_dir, chats_dict=full_db)
	migrate_statistics(old_db=stats_db_dir)

	print('✅ Done!')
