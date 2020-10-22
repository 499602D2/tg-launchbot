import os
import time
import sqlite3
import logging
import datetime

from utils import time_delta_to_legible_eta, reconstruct_message_for_markdown

# creates a new notifications database, if one doesn't exist
def create_notify_database(db_path):
	if not os.path.isdir(db_path):
		os.makedirs(db_path)

	# Establish connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	try:
		# chat ID - keyword - UNIX timestamp - enabled true/false
		cursor.execute("CREATE TABLE notify (chat TEXT, keyword TEXT, muted_launches TEXT, enabled INT, PRIMARY KEY (chat, keyword))")
		cursor.execute("CREATE INDEX enabledchats ON notify (chat, enabled)")
	except Exception as error:
		print('⚠️ Error creating notify-table:', error)

	conn.commit()
	conn.close()


# store sent message identifiers
def store_notification_identifiers(launch_id, msg_identifiers):
	launch_dir = 'data'
	conn = sqlite3.connect(os.path.join(launch_dir, 'launchbot-data.db'))
	cursor = conn.cursor()

	try:
		cursor.execute("CREATE TABLE sent_notification_identifiers (id INT, msg_identifiers TEXT, PRIMARY KEY (id))")
		cursor.execute("CREATE INDEX id_identifiers ON notify (id, identifiers)")
		if debug_log:
			logging.info(f'✨ sent-notifications.db created!')
	except sqlite3.OperationalError:
		pass

	try:
		cursor.execute('''INSERT INTO sent_notification_identifiers (id, msg_identifiers) VALUES (?, ?)''',(launch_id, msg_identifiers))
	except:
		cursor.execute('''UPDATE sent_notification_identifiers SET msg_identifiers = ? WHERE id = ?''', (msg_identifiers, launch_id))

	conn.commit()
	conn.close()


# create launch database
def create_launch_db(db_path, cursor):
	'''Summary
	Creates the launch database. Only ran when the table doesn't exist.
	'''
	try:
		cursor.execute('''CREATE TABLE launches
			(name TEXT, unique_id TEXT, ll_id INT, net_unix INT, status_id INT, status_state TEXT,
			in_hold BOOLEAN, probability REAL, success BOOLEAN, launched BOOLEAN,

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

			notify_24h BOOLEAN, notify_12h BOOLEAN, notify_60min BOOLEAN, notify_5min BOOLEAN,
			PRIMARY KEY (unique_id))
		''')

		cursor.execute("CREATE INDEX name_to_unique_id ON launches (name, unique_id)")
		cursor.execute("CREATE INDEX unique_id_to_lsp_short ON launches (unique_id, lsp_short)")
		cursor.execute("CREATE INDEX net_unix_to_lsp_short ON launches (net_unix, lsp_short)")

	except sqlite3.OperationalError as e:
		if debug_log:
			logging.exception(f'⚠️ Error in create_launch_database: {e}')


# update launch database
def update_launch_db(launch_set: set, db_path: str, bot_username: str):
	def verify_no_net_slip(launch_object, cursor) -> bool:
		'''Summary
		Verify the NET of the launch hasn't slipped forward: if it has,
		verify that we haven't sent a notification: if we have, send a postpone
		notification to users.
		'''

		# load launch from db
		cursor.execute('SELECT * FROM launches WHERE unique_id = ?', (launch_object.unique_id,))
		query_return = [dict(row) for row in cursor.fetchall()]
		launch_db = query_return[0]

		# compare NETs: if they match, return
		if launch_db['net_unix'] == launch_object.net_unix:
			return False

		# NETs don't match: calculate slip (diff), verify we haven't sent any notifications
		net_diff = launch_object.net_unix - launch_db['net_unix']
		notification_states = {
			'notify_24h': launch_db['notify_24h'], 'notify_12h': launch_db['notify_12h'],
			'notify_60min': launch_db['notify_60min'], 'notify_5min': launch_db['notify_5min']}

		# map notification "presend" time to hour multiples (i.e. 3600 * X)
		notif_pre_time_map = {
			'notify_24h': 24, 'notify_12h': 12, 'notify_60min': 1, 'notify_5min': 5/60}

		print(f'''
			notification_states: {notification_states},
			net_diff: {net_diff},
			launch_object.launched: {launch_object.launched}
			''')

		# if we have at least one sent notification, the net has slipped >5 min, and we haven't launched
		if 1 in notification_states.values() and net_diff >= 5*60 and launch_object.launched != True:
			# iterate over the notification states loaded from the database
			for key, status in notification_states.items():
				# reset if net_diff > the notification send period (we're outside the window again)
				if status == 1 and net_diff > 3600 * notif_pre_time_map[key]:
					notification_states[key] = 0

			# generate the postpone string
			postpone_str = time_delta_to_legible_eta(time_delta=int(net_diff))

			# we've got the pretty eta: log
			logging.info(f'⏱ ETA string generated for net_diff={net_diff}: {postpone_str}')

			# generate launch time (UTC) string TODO add support for user time zone
			launch_dt = datetime.datetime.utcfromtimestamp(launch_object.net_unix)
			launch_time = f'{launch_dt.hour}:{"0" if launch_dt.minute < 10 else ""}{launch_dt.minute}'

			# lift-off date string in a pretty format
			ymd_split = f'{launch_dt.year}-{launch_dt.month}-{launch_dt.day}'.split('-')
			try:
				suffix = {1: 'st', 2: 'nd', 3: 'rd'}[int(str(ymd_split[2])[-1])]
			except:
				suffix = 'th'

			# map integer month number to string-format
			month_map = {
				1: 'January', 2: 'February', 3: 'March', 4: 'April',
				5: 'May', 6: 'June', 7: 'July', 8: 'August',
				9: 'September', 10: 'October', 11: 'November', 12: 'December'}

			# construct launch date string
			date_str = f'{month_map[int(ymd_split[1])]} {ymd_split[2]}{suffix}'

			# calculate days until next launch attempt
			eta_sec = launch_object.net_unix - time.time()
			next_attempt_eta_str = time_delta_to_legible_eta(time_delta=int(eta_sec))

			# launch name: handle possible IndexError as well, even while this should never happen
			try:
				launch_name = launch_object.name.split('|')[1]
			except IndexError:
				launch_name = launch_object.name

			# construct the postpone message
			postpone_message = f'''
			📢 *{launch_name}* has been postponed by {postpone_str}.
			*{launch_object.lsp_name}* is now targeting lift-off on *{date_str}* at *{launch_time} UTC*.\n\n
			⏱ {next_attempt_eta_str} until next launch attempt.\n\n
			'''

			# reconstruct
			postpone_message = reconstruct_message_for_markdown(postpone_message)

			# append the manually escaped footer
			postpone_message += f'''
			ℹ️ _You will be re\-notified of this launch\. For detailed info\, use \/next\@{bot_username}\.
			To disable\, mute this launch with the button below\._
			'''

			# TODO actually send postpone notification
			logging.info(f'🎉 postpone_str generated: {postpone_message}')

			# generate insert statement for db update
			insert_statement = '=?,'.join(notification_states.keys()) + '=?'

			# generate tuple for values: values for states + unique ID
			values_tuple = tuple(notification_states.values()) + (launch_object.unique_id,)

			# store updated notification states
			cursor.execute(f'UPDATE launches SET {insert_statement} WHERE unique_id = ?', values_tuple)
			
			# log
			logging.info(f'🚩 Notification states reset for launch_id={launch_object.unique_id}!')
			logging.info(f'ℹ️ Postponed by {postpone_str}. New states: {notification_states}')

			return True

	# check if db exists
	if not os.path.isfile(os.path.join(db_path, 'launchbot-data.db')):
		if not os.path.isdir(db_path):
			os.makedirs(db_path)

		create_launch_db(db_path=db_path, cursor=cursor)
		logging.info(f"{'✅ Created launch db' if success else '⚠️ Failed to create launch db'}")

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
	for launch_object in launch_set:
		try: # try inserting as new
			# set fields and values to be inserted according to available keys/values
			insert_fields = ', '.join(vars(launch_object).keys()) + ', notify_24h, notify_12h, notify_60min, notify_5min'
			field_values = tuple(vars(launch_object).values()) + (False, False, False, False)
			
			# set amount of value-characers (?) equal to amount of keys + 4 (notify fields)
			values_string = '?,' * (len(vars(launch_object).keys()) + 4)
			values_string = values_string[0:-1]

			# execute SQL
			cursor.execute(f'INSERT INTO launches ({insert_fields}) VALUES ({values_string})', field_values)

		except Exception as error: # update, as the launch already exists
			# if insert failed, update existing data: everything but unique ID and notification fields
			obj_dict = vars(launch_object)

			# db column names are same as object properties: use keys as fields, and values as field values
			update_fields = obj_dict.keys()
			update_values = obj_dict.values()

			# generate the string for the SET command
			set_str = ' = ?, '.join(update_fields) + ' = ?'

			# verify launch hasn't been postponed
			net_slipped = verify_no_net_slip(launch_object, cursor)

			try:
				cursor.execute(f"UPDATE launches SET {set_str} WHERE unique_id = ?", tuple(update_values) + (launch_object.unique_id,))
			except Exception as error:
				print(f'⚠️ Error updating field for unique_id={launch_object.unique_id}! Error: {error}')

	conn.commit()
	conn.close()


# create a statistics database
def create_stats_db(db_path):
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


# updates our stats with the given input
def update_stats_db(stats_update, db_path):
	# check if the db exists
	if not os.path.isfile(os.path.join(db_path, 'launchbot-data.db')):
		create_stats_db(db_path=db_path)

	# Establish connection
	stats_conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	stats_cursor = stats_conn.cursor()

	# verify table exists
	stats_cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'stats'))
	if len(stats_cursor.fetchall()) == 0:
		logging.warning("⚠️ Statistics table doesn't exists: creating...")
		create_stats_db(db_path)

	# Update stats with the provided data
	for stat, val in stats_update.items():
		if stat == 'last_api_update':
			stats_cursor.execute(f"UPDATE stats SET {stat} = {val}")
		else:
			stats_cursor.execute(f"UPDATE stats SET {stat} = {stat} + {val}")

	stats_conn.commit()
	stats_conn.close()
