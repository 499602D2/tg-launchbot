import os
import sys
import time
import logging
import difflib
import datetime
import sqlite3

import requests
import ujson as json

class LaunchLibrary2Launch:
	'''Description
	A class for simplifying the handling of launch objects. Contains all the properties needed
	by the bot.
	'''
	def __init__(self, launch_json):
		# launch unique information
		self.name = launch_json['name']
		self.unique_id = launch_json['id']
		self.ll_id = launch_json['launch_library_id']

		# net and status
		self.net_unix = timestamp_to_unix(launch_json['net'])
		self.status_id = launch_json['status']['id']
		self.status_state = launch_json['status']['name']
		self.in_hold = launch_json['inhold']
		self.probability = launch_json['probability']
		self.success = True if 'Success' in launch_json['status']['name'] else False
		self.launched = False

		# lsp/agency info
		self.lsp_id = launch_json['launch_service_provider']['id']
		self.lsp_name = launch_json['launch_service_provider']['name']
		self.lsp_short = launch_json['launch_service_provider']['abbrev']
		self.lsp_country_code = launch_json['launch_service_provider']['country_code']

		# webcast status and links
		self.webcast_islive = launch_json['webcast_live']
		self.webcast_url_list = None # preset to None

		# url_list is a list of dictionaries
		if len(launch_json['vidURLs']) >= 1:
			url_set = set()
			for url_dict in launch_json['vidURLs']:
				url_set.add(url_dict['url'])

			self.webcast_url_list = ','.join(url_set)

		# rocket information
		self.rocket_name = launch_json['rocket']['configuration']['name']
		self.rocket_full_name = launch_json['rocket']['configuration']['full_name']
		self.rocket_variant = launch_json['rocket']['configuration']['variant']
		self.rocket_family = launch_json['rocket']['configuration']['family']
		
		# launcher stage information
		if launch_json['rocket']['launcher_stage'] not in (None, []):
			if len(launch_json['rocket']['launcher_stage']) > 1:
				print('⚠️⚠️⚠️ more than one launcher_stage')
				print(launch_json['rocket']['launcher_stage'])

			# id, type, reuse status, flight number
			self.launcher_stage_id = launch_json['rocket']['launcher_stage'][0]['id']
			self.launcher_stage_type = launch_json['rocket']['launcher_stage'][0]['type']
			self.launcher_stage_is_reused = launch_json['rocket']['launcher_stage'][0]['reused']
			self.launcher_stage_flight_number = launch_json['rocket']['launcher_stage'][0]['launcher_flight_number']
			self.launcher_stage_turn_around = launch_json['rocket']['launcher_stage'][0]['turn_around_time_days']

			# flight proven and serial number
			self.launcher_is_flight_proven = launch_json['rocket']['launcher_stage'][0]['launcher']['flight_proven']
			self.launcher_serial_number = launch_json['rocket']['launcher_stage'][0]['launcher']['serial_number']

			# first flight and maiden flight
			try:
				self.launcher_maiden_flight = timestamp_to_unix(launch_json['rocket']['launcher_stage'][0]['launcher']['first_launch_date'])
				self.launcher_last_flight = timestamp_to_unix(launch_json['rocket']['launcher_stage'][0]['launcher']['last_launch_date'])
			except:
				self.launcher_maiden_flight = None
				self.launcher_last_flight = None

			# landing attempt, landing location, landing type, landing count at location
			if launch_json['rocket']['launcher_stage'][0]['landing'] is not None:
				self.launcher_landing_attempt = launch_json['rocket']['launcher_stage'][0]['landing']['attempt']
				self.launcher_landing_location = launch_json['rocket']['launcher_stage'][0]['landing']['location']['abbrev']
				self.landing_type = launch_json['rocket']['launcher_stage'][0]['landing']['type']['abbrev']
				self.launcher_landing_location_nth_landing = launch_json['rocket']['launcher_stage'][0]['landing']['location']['successful_landings']
			else:
				self.launcher_landing_attempt = None
				self.launcher_landing_location = None
				self.landing_type = None
				self.launcher_landing_location_nth_landing = None
		else:
			self.launcher_stage_id = None
			self.launcher_stage_type = None
			self.launcher_stage_is_reused = None
			self.launcher_stage_flight_number = None
			self.launcher_stage_turn_around = None
			self.launcher_is_flight_proven = None
			self.launcher_serial_number = None
			self.launcher_maiden_flight = None
			self.launcher_last_flight = None
			self.launcher_landing_attempt = None
			self.launcher_landing_location = None
			self.landing_type = None
			self.launcher_landing_location_nth_landing = None

		if launch_json['rocket']['spacecraft_stage'] not in (None, []):
			self.spacecraft_id = launch_json['rocket']['spacecraft_stage']['id']
			self.spacecraft_sn = launch_json['rocket']['spacecraft_stage']['spacecraft']['serial_number']
			self.spacecraft_name = launch_json['rocket']['spacecraft_stage']['spacecraft']['spacecraft_config']['name']

			# parse mission crew, if applicable
			if launch_json['rocket']['spacecraft_stage']['launch_crew'] not in (None, []):
				astronauts = set()
				for crew_member in launch_json['rocket']['spacecraft_stage']['launch_crew']:
					astronauts.add(f"{crew_member['astronaut']['name']}:{crew_member['role']}")

				self.spacecraft_crew = ','.join(astronauts)
				self.spacecraft_crew_count = len(astronauts)
			
			try:
				self.spacecraft_maiden_flight = timestamp_to_unix(launch_json['rocket']['spacecraft_stage']['spacecraft']['spacecraft_config']['maiden_flight'])
			except:
				self.spacecraft_maiden_flight = None
		else:
			self.spacecraft_id = None
			self.spacecraft_sn = None
			self.spacecraft_name = None
			self.spacecraft_crew = None
			self.spacecraft_crew_count = None
			self.spacecraft_maiden_flight = None

		# mission (payload) information
		if launch_json['mission'] is not None:
			self.mission_name = launch_json['mission']['name']
			self.mission_type = launch_json['mission']['type']
			self.mission_description = launch_json['mission']['description']
			if launch_json['mission']['orbit'] is not None:
				self.mission_orbit = launch_json['mission']['orbit']['name']
				self.mission_orbit_abbrev = launch_json['mission']['orbit']['abbrev']
			else:
				self.mission_orbit = None
				self.mission_orbit_abbrev = None

		else:
			self.mission_name = None
			self.mission_type = None
			self.mission_description = None
			self.mission_orbit = None
			self.mission_orbit_abbrev = None

		# launch location information
		self.pad_name = launch_json['pad']['name']
		self.location_name = launch_json['pad']['location']['name']
		self.location_country_code = launch_json['pad']['location']['country_code']

		# tidbits for fun facts etc.
		self.pad_nth_launch = launch_json['pad']['total_launch_count']
		self.location_nth_launch = launch_json['pad']['location']['total_launch_count']
		self.agency_nth_launch = launch_json['agency_launch_attempt_count']
		self.agency_nth_launch_year = launch_json['agency_launch_attempt_count_year']
		if 'orbital_launch_attempt_count_year' in launch_json:
			self.orbital_nth_launch_year = launch_json['orbital_launch_attempt_count_year']
		else:
			self.orbital_nth_launch_year = None

def timestamp_to_unix(timestamp):
	# convert to a datetime object. Ex. 2020-10-18T12:25:00Z
	utc_dt = datetime.datetime.strptime(timestamp, '%Y-%m-%dT%H:%M:%S%fZ')

	# convert UTC datetime to seconds since the Epoch, return
	return int((utc_dt - datetime.datetime(1970, 1, 1)).total_seconds())


def construct_params(PARAMS):
	param_url = ''
	if PARAMS is not None:
		for enum, keyvals in enumerate(PARAMS.items()):
			key, val = keyvals[0], keyvals[1]

			if enum == 0:
				param_url += f'?{key}={val}'
			else:
				param_url += f'&{key}={val}'

	return param_url


def update_db(launch_set):
	# check if db exists
	launch_dir = 'data/launch'
	if not os.path.isfile(os.path.join(launch_dir, 'launches.db')):
		if not os.path.isdir(launch_dir):
			os.makedirs(launch_dir)

		# open connection
		conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
		cursor = conn.cursor()

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

			conn.commit()
			conn.close()

		except sqlite3.OperationalError as e:
			if debug_log:
				logging.exception(f'⚠️ Error in create_launch_database: {e}')

			conn.close()

	# open connection
	conn = sqlite3.connect(os.path.join(launch_dir, 'launches.db'))
	cursor = conn.cursor()

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

			update_fields = obj_dict.keys()
			update_values = obj_dict.values()

			# generate the string for the SET command
			set_str = ' = ?, '.join(update_fields) + ' = ?'

			# cursor.execute("UPDATE command_frequency SET commands = commands + ? WHERE day = ? AND time_slot = ?", (stats_update['commands'], date, slot))
			try:
				cursor.execute(f"UPDATE launches SET {set_str} WHERE unique_id = ?", tuple(update_values) + (launch_object.unique_id,))
				print(f'Updated {launch_object.unique_id}!')
			except Exception as error:
				print(f'⚠️ Error updating field for unique_id={launch_object.unique_id}! Error: {error}')

	conn.commit()
	conn.close()

	print('✅ DB update complete!')


def ll2_api_call():
	# bot params
	BOT_USERNAME = 'rocketrybot_debug'
	VERSION = '0.6.0'

	# datetime, so we can only get launches starting today
	now = datetime.datetime.now()
	today_call = f'{now.year}-{now.month}-{now.day}'

	# what we're throwing at the API
	API_URL = 'https://ll.thespacedevs.com'
	API_VERSION = '2.0.0'
	API_REQUEST = 'launch/upcoming'
	PARAMS = {'mode': 'detailed', 'limit': 250}

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}' #&{fields}

	# set headers
	headers = {'user-agent': f'telegram-{BOT_USERNAME}/{VERSION}'}

	if os.path.isfile('ll2-json.json'):
		with open('ll2-json.json', 'r') as json_file:
			api_json = json.load(json_file)

		print('⚠️⚠️⚠️ API call skipped!')
		time.sleep(2)
	else:
		try:
			API_RESPONSE = requests.get(API_CALL, headers=headers)
		except Exception as error:
			print(f'🛑 Error in LL API request: {error}')
			print('⚠️ Trying again after 3 seconds...')

			time.sleep(3)
			ll2_api_call()

			print('✅ Success: returning!')
			return

		try:
			api_json = json.loads(API_RESPONSE.text)
		except Exception as json_parse_error:
			print('⚠️ Error parsing json')

	# dump json
	with open('ll2-json.json', 'w') as json_file:
		json.dump(api_json, json_file, indent=4)

	for launch in api_json:
		print(launch)

	print(f"count: {api_json['count']}")
	print(f'next: {len(api_json["next"])}')

	print('➡️ Testing json parsing...\n')

	# parse the result json into a set of launch objects
	launch_obj_set = set()
	for i in range(len(api_json['results'])):
		launch_object = LaunchLibrary2Launch(api_json['results'][i])
		launch_obj_set.add(launch_object)
		
		print('➡️ Got properties')
		for prop, value in vars(launch_object).items():
			print(f'\t{prop}: {value}')

		print('\n\n\n✅ ––––––––––––––––––––––––')

	print('Done!')
	print(f'Parsed {len(launch_obj_set)} launches into launch_obj_set.')

	# update database with the launch objects
	update_db(launch_obj_set)


if __name__ == '__main__':
	print('Starting API calls...')
	ll2_api_call()
	print('\n✅ Done!')

