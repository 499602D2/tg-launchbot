'''
Includes various functions related to common database operations.

Classes:
	None

Functions:

Misc variables:
	None
'''


import os
import time
import sqlite3
import logging
import datetime
import inspect

import redis
import ujson as json

from utils import time_delta_to_legible_eta, reconstruct_message_for_markdown


def create_chats_db(db_path: str, cursor: sqlite3.Cursor):
	'''
	A new database table intended to merge the notify- and preferences tables,
	while also dramatically simplifying the structure of the notify-table.

	Keyword arguments:
		db_path (str): relative database path

	Returns:
		None
	'''
	if not os.path.isdir(db_path):
		os.makedirs(db_path)

	try:
		cursor.execute('''
			CREATE TABLE chats (chat TEXT, subscribed_since INT, member_count INT,
			time_zone TEXT, time_zone_str TEXT, command_permissions TEXT, postpone_notify BOOLEAN,
			notify_time_pref TEXT, enabled_notifications TEXT, disabled_notifications TEXT,
			PRIMARY KEY (chat))
			''')

		cursor.execute("CREATE INDEX chatenabled ON chats (chat, enabled_notifications)")
		cursor.execute("CREATE INDEX chatdisabled ON chats (chat, disabled_notifications)")
	except sqlite3.OperationalError as error:
		logging.exception(f'⚠️ Error creating chats table: {error}')


def migrate_chat(db_path: str, old_id: int, new_id: int):
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	try:
		cursor.execute('UPDATE chats SET chat = ? WHERE chat = ?', (new_id, old_id))
	except:
		logging.exception(f'⚠️ Unable to migrate chat {old_id} to {new_id}!')

	conn.commit()
	conn.close()


def create_launch_db(db_path: str, cursor: sqlite3.Cursor):
	'''
	Creates the launch database. Only ran when the table doesn't exist.

	Keyword arguments:
		db_path (str): relative database path
		cursor (sqlite3.Cursor): sqlite3 db cursor we use

	Returns:
		None
	'''

	try:
		cursor.execute('''CREATE TABLE launches
			(name TEXT, unique_id TEXT, ll_id INT, net_unix INT, status_id INT, status_state TEXT,
			in_hold BOOLEAN, probability REAL, success BOOLEAN, tbd_time BOOLEAN, tbd_date BOOLEAN,
			launched BOOLEAN,

			webcast_islive BOOLEAN, webcast_url_list TEXT,

			lsp_id INT, lsp_name TEXT, lsp_short TEXT, lsp_country_code TEXT,
			
			mission_name TEXT, mission_type TEXT, mission_orbit TEXT, mission_orbit_abbrev TEXT,
			mission_description TEXT,

			pad_name TEXT, location_name TEXT, location_country_code TEXT,

			rocket_name TEXT, rocket_full_name TEXT, rocket_variant TEXT, rocket_family TEXT,
			
			launcher_stage_id TEXT, launcher_stage_type TEXT, launcher_stage_is_reused BOOLEAN,
			launcher_stage_flight_number INT, launcher_stage_turn_around TEXT, launcher_is_flight_proven BOOLEAN,
			launcher_serial_number TEXT, launcher_maiden_flight INT, launcher_last_flight INT,
			launcher_landing_attempt BOOLEAN, launcher_landing_location TEXT, landing_type TEXT,
			launcher_landing_location_nth_landing INT,

			spacecraft_id INT, spacecraft_sn TEXT, spacecraft_name TEXT, spacecraft_crew TEXT,
			spacecraft_crew_count INT, spacecraft_maiden_flight INT,

			pad_nth_launch INT, location_nth_launch INT, agency_nth_launch INT, agency_nth_launch_year INT,
			orbital_nth_launch_year INT, 

			last_updated INT,

			notify_24h BOOLEAN, notify_12h BOOLEAN, notify_60min BOOLEAN, notify_5min BOOLEAN,

			muted_by TEXT, sent_notification_ids TEXT,
			PRIMARY KEY (unique_id))
		''')

		cursor.execute("CREATE INDEX name_to_unique_id ON launches (name, unique_id)")
		cursor.execute("CREATE INDEX unique_id_to_lsp_short ON launches (unique_id, lsp_short)")
		cursor.execute("CREATE INDEX net_unix_to_lsp_short ON launches (net_unix, lsp_short)")

	except sqlite3.OperationalError as e:
		logging.exception(f'⚠️ Error in create_launch_database: {e}')


def update_launch_db(launch_set: set, db_path: str, bot_username: str, api_update: int):
	'''
	Updates the launch table with whatever data the API call provides.

	Keyword arguments:
		launch_set (set): set of launch objects we use to update the database
		db_path (str): relative database path
		bot_username (str): username of the bot
		api_update (int): unix timestamp of the time the db was updated

	Returns:
		None
	'''
	def verify_no_net_slip(
		launch_object: 'LaunchLibrary2Launch', cursor: sqlite3.Cursor) -> (bool, tuple):
		'''
		Verify the NET of the launch hasn't slipped forward: if it has, verify
		that we haven't sent a notification: if we have, send a postpone
		notification to users.
		'''

		# load launch from db
		cursor.execute('SELECT * FROM launches WHERE unique_id = ?', (launch_object.unique_id,))
		query_return = [dict(row) for row in cursor.fetchall()]
		launch_db = query_return[0]

		# compare NETs: if they match, return
		if launch_db['net_unix'] == launch_object.net_unix:
			return (False, ())

		# NETs don't match: calculate slip (diff), verify we haven't sent any notifications
		net_diff = launch_object.net_unix - launch_db['net_unix']
		notification_states = {
			'notify_24h': launch_db['notify_24h'], 'notify_12h': launch_db['notify_12h'],
			'notify_60min': launch_db['notify_60min'], 'notify_5min': launch_db['notify_5min']}

		# map notification "presend" time to hour multiples (i.e. 3600 * X)
		notif_pre_time_map = {
			'notify_24h': 24, 'notify_12h': 12, 'notify_60min': 1, 'notify_5min': 5/60}

		# keep track of wheter we reset a notification state to 0
		notification_state_reset = False
		skipped_postpones = []

		# if we have at least one sent notification, the net has slipped >5 min, and we haven't launched
		if 1 in notification_states.values() and net_diff >= 5*60 and not launch_object.launched:
			# iterate over the notification states loaded from the database
			for key, status in notification_states.items():
				# reset if net_diff > the notification send period (we're outside the window again)
				if int(status) == 1 and net_diff >= 3600 * notif_pre_time_map[key]:
					notification_states[key] = 0
					notification_state_reset = True
				else:
					# log skipped postpones
					postpone = {'status': status, 'net_diff': net_diff, 'multipl.': notif_pre_time_map[key]}
					skipped_postpones.append(postpone)

			if not notification_state_reset:
				logging.warning('⚠️ No notification states were reset: exiting...')
				logging.warning(f'📜 notification_states: {notification_states}')
				logging.warning(f'📜 skipped_postpones: {skipped_postpones}')
				return (False, None)

			logging.warning('✅ A notification state was reset: continuing...')

			# generate the postpone string
			postpone_str = time_delta_to_legible_eta(time_delta=int(net_diff), full_accuracy=False)

			# we've got the pretty eta: log
			logging.info(f'⏱ ETA string generated for net_diff={net_diff}: {postpone_str}')

			# calculate days until next launch attempt
			eta_sec = launch_object.net_unix - time.time()
			next_attempt_eta_str = time_delta_to_legible_eta(time_delta=int(eta_sec), full_accuracy=False)

			# launch name: handle possible IndexError as well, even while this should never happen
			try:
				launch_name = launch_object.name.split('|')[1].strip()
			except IndexError:
				launch_name = launch_object.name.strip()

			# construct the postpone message
			postpone_msg = f'📢 *{launch_name}* has been postponed by {postpone_str}. '
			postpone_msg += f'*{launch_object.lsp_name}* is now targeting lift-off on *DATEHERE* at *LAUNCHTIMEHERE*.'
			postpone_msg += f'\n\n⏱ {next_attempt_eta_str} until next launch attempt.'

			# reconstruct
			postpone_msg = reconstruct_message_for_markdown(postpone_msg)

			# append the manually escaped footer
			postpone_msg += '\n\nℹ️ _You will be re\-notified of this launch\. '
			postpone_msg += f'For detailed info\, use \/next\@{bot_username}\. '
			postpone_msg += 'To disable\, mute this launch with the button below\._'

			# clean message
			postpone_msg = inspect.cleandoc(postpone_msg)

			# log the message
			logging.info(f'📢 postpone_msg generated:\n{postpone_msg}')

			# generate insert statement for db update
			insert_statement = '=?,'.join(notification_states.keys()) + '=?'

			# generate tuple for values: values for states + unique ID
			values_tuple = tuple(notification_states.values()) + (launch_object.unique_id,)

			# store updated notification states
			cursor.execute(f'UPDATE launches SET {insert_statement} WHERE unique_id = ?', values_tuple)

			# log
			logging.info(f'🚩 Notification states reset for launch_id={launch_object.unique_id}!')
			logging.info(f'ℹ️ Postponed by {postpone_str}. New states: {notification_states}')

			''' return bool + a tuple we can use to send the postpone notification easily
			(launch_object, message) '''
			postpone_tup = (launch_object, postpone_msg)

			return (True, postpone_tup)

		return (False, ())

	# check if folders exist
	if not os.path.isfile(os.path.join(db_path, 'launchbot-data.db')):
		if not os.path.isdir(db_path):
			os.makedirs(db_path)

	# open connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	# verify table exists
	cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'launches'))
	if len(cursor.fetchall()) == 0:
		logging.warning("⚠️ Launches table doesn't exists: creating...")
		create_launch_db(db_path=db_path, cursor=cursor)

	# loop over launch objcets in launch_set
	slipped_launches = set()
	for launch_object in launch_set:
		try: # try inserting as new
			# set fields and values to be inserted according to available keys/values
			insert_fields = ', '.join(vars(launch_object).keys())
			insert_fields += ', last_updated, notify_24h, notify_12h, notify_60min, notify_5min'
			field_values = tuple(vars(launch_object).values()) + (api_update, False, False, False, False)

			# set amount of value-characers (?) equal to amount of keys + 4 (notify fields)
			values_string = '?,' * (len(vars(launch_object).keys()) + 5)
			values_string = values_string[0:-1]

			# execute SQL
			cursor.execute(f'INSERT INTO launches ({insert_fields}) VALUES ({values_string})', field_values)

		except sqlite3.IntegrityError: # update, as the launch already exists
			# if insert failed, update existing data: everything but unique ID and notification fields
			obj_dict = vars(launch_object)

			# db column names are same as object properties: use keys as fields, and values as field values
			update_fields = obj_dict.keys()
			update_values = obj_dict.values()

			# generate the string for the SET command
			set_str = ' = ?, '.join(update_fields) + ' = ?'

			# verify launch hasn't been postponed
			net_slipped, postpone_tuple = verify_no_net_slip(launch_object=launch_object, cursor=cursor)

			if net_slipped:
				slipped_launches.add(postpone_tuple)

			try:
				cursor.execute(f"UPDATE launches SET {set_str} WHERE unique_id = ?",
					tuple(update_values) + (launch_object.unique_id,))
				cursor.execute(f"UPDATE launches SET last_updated = ? WHERE unique_id = ?",
					(api_update,) + (launch_object.unique_id,))
			except Exception:
				logging.exception(f'⚠️ Error updating field for unique_id={launch_object.unique_id}!')

	# commit changes
	conn.commit()
	conn.close()

	# if we have launches that have been postponed, return them
	if len(slipped_launches) > 0:
		return slipped_launches

	return set()


def create_stats_db(db_path: str):
	'''
	Creates a stats table in the launchbot-data.db database.

	Keyword arguments:
		db_path (str): relative database path

	Returns:
		None
	'''
	if not os.path.isdir(db_path):
		os.mkdir(db_path)

	# Establish connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	try:
		# create table
		cursor.execute('''CREATE TABLE stats 
			(notifications INT, api_requests INT, db_updates INT, commands INT,
			data INT, last_api_update INT, PRIMARY KEY (notifications, api_requests))''')

		# insert first (and only) row of data
		cursor.execute('''INSERT INTO stats 
			(notifications, api_requests, db_updates, commands, data, last_api_update)
			VALUES (0, 0, 0, 0, 0, 0)''')
	except sqlite3.OperationalError as sqlite_error:
		logging.warn('⚠️ Error creating stats database: %s', sqlite_error)

	conn.commit()
	conn.close()


def update_stats_db(stats_update: dict, db_path: str):
	'''
	Updates the stats table with the given stats.

	Keyword arguments:
		stats_update (dict): dictionary of key-values to update
		db_path (str): relative database path

	Returns:
		None
	'''
	# check if the db exists
	if not os.path.isfile(os.path.join(db_path, 'launchbot-data.db')):
		create_stats_db(db_path=db_path)

	# Establish connection: sqlite + redis
	stats_conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	stats_cursor = stats_conn.cursor()
	rd = redis.Redis(host='localhost', port=6379, db=0, decode_responses=True)

	# verify table exists
	stats_cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'stats'))
	if len(stats_cursor.fetchall()) == 0:
		logging.warning("⚠️ Statistics table doesn't exists: creating...")
		create_stats_db(db_path)

	# verify cache exists
	if not rd.exists('stats'):
		# pull from disk
		stats_conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
		stats_conn.row_factory = sqlite3.Row
		stats_cursor = stats_conn.cursor()

		try:
			# select stats field
			stats_cursor.execute("SELECT * FROM stats")
			stats = [dict(row) for row in stats_cursor.fetchall()][0]
		except sqlite3.OperationalError:
			stats = {
				'notifications': 0, 'api_requests': 0, 'db_updates': 0,
				'commands': 0, 'data': 0, 'last_api_update': 0}

		if stats['last_api_update'] is None:
			stats['last_api_update'] = int(time.time())

		rd.hmset('stats', stats)

	# Update stats with the provided data
	for stat, val in stats_update.items():
		if stat == 'last_api_update':
			stats_cursor.execute(f"UPDATE stats SET {stat} = {val}")
			rd.hset('stats', stat, val)
		else:
			stats_cursor.execute(f"UPDATE stats SET {stat} = {stat} + {val}")
			rd.hset('stats', stat, int(rd.hget('stats', stat)) + int(val))

	# commit changes
	stats_conn.commit()
	stats_conn.close()
