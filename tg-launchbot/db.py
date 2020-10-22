import os
import sqlite3
import logging

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
def update_launch_db(launch_set, db_path):
	# check if db exists
	if not os.path.isfile(os.path.join(db_path, 'launchbot-data.db')):
		if not os.path.isdir(db_path):
			os.makedirs(db_path)

		create_launch_db(db_path=db_path, cursor=cursor)
		logging.info(f"{'✅ Created launch db' if success else '⚠️ Failed to create launch db'}")

	# open connection
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# verify table exists
	cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'launches'))
	if len(cursor.fetchall()) == 0:
		logging.warning("⚠️ Launches table doesn't exists: creating...")
		create_launch_db(db_path=db_path, cursor=cursor)

	# loop over launch objcets in launch_set
	for launch_object in launch_set:
		try:
			# set fields and values to be inserted according to available keys/values
			insert_fields = ', '.join(vars(launch_object).keys()) + ', notify_24h, notify_12h, notify_60min, notify_5min'
			field_values = tuple(vars(launch_object).values()) + (False, False, False, False)
			
			# set amount of value-characers (?) equal to amount of keys + 4 (notify fields)
			values_string = '?,' * (len(vars(launch_object).keys()) + 4)
			values_string = values_string[0:-1]

			# execute SQL
			cursor.execute(f'INSERT INTO launches ({insert_fields}) VALUES ({values_string})', field_values)

		except Exception as error: 
			# if insert failed, update existing data: everything but unique ID and notification fields
			obj_dict = vars(launch_object)

			# db column names are same as object properties: use keys as fields, and values as field values
			update_fields = obj_dict.keys()
			update_values = obj_dict.values()

			# generate the string for the SET command
			set_str = ' = ?, '.join(update_fields) + ' = ?'

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
		create_stats_database(db_path='data')

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
