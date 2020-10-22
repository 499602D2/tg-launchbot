import os
import sys
import time
import logging
import difflib
import datetime
import sqlite3

import requests
import ujson as json

from apscheduler.schedulers.background import BackgroundScheduler

# local imports
from utils import timestamp_to_unix
from db import update_launch_db, update_stats_db
from notifications import notification_send_scheduler

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
		
		# set launched state based on status_state
		launch_bool = [status for status in {'success', 'failure'} if (status in self.status_state.lower())] 
		self.launched = True if any(launch_bool) else False

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

			launcher_json = launch_json['rocket']['launcher_stage'][0]

			# id, type, reuse status, flight number
			self.launcher_stage_id = launcher_json['id']
			self.launcher_stage_type = launcher_json['type']
			self.launcher_stage_is_reused = launcher_json['reused']
			self.launcher_stage_flight_number = launcher_json['launcher_flight_number']
			self.launcher_stage_turn_around = launcher_json['turn_around_time_days']

			# flight proven and serial number
			self.launcher_is_flight_proven = launcher_json['launcher']['flight_proven']
			self.launcher_serial_number = launcher_json['launcher']['serial_number']

			# first flight and maiden flight
			try:
				self.launcher_maiden_flight = timestamp_to_unix(launcher_json['launcher']['first_launch_date'])
				self.launcher_last_flight = timestamp_to_unix(launcher_json['launcher']['last_launch_date'])
			except:
				self.launcher_maiden_flight = None
				self.launcher_last_flight = None

			# landing attempt, landing location, landing type, landing count at location
			if launcher_json['landing'] is not None:
				landing_json = launcher_json['landing']
				self.launcher_landing_attempt = landing_json['attempt']
				self.launcher_landing_location = landing_json['location']['abbrev']
				self.landing_type = landing_json['type']['abbrev']
				self.launcher_landing_location_nth_landing = landing_json['location']['successful_landings']
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
			spacecraft = launch_json['rocket']['spacecraft_stage']
			self.spacecraft_id = spacecraft['id']
			self.spacecraft_sn = spacecraft['spacecraft']['serial_number']
			self.spacecraft_name = spacecraft['spacecraft']['spacecraft_config']['name']

			# parse mission crew, if applicable
			if spacecraft['launch_crew'] not in (None, []):
				astronauts = set()
				for crew_member in spacecraft['launch_crew']:
					astronauts.add(f"{crew_member['astronaut']['name']}:{crew_member['role']}")

				self.spacecraft_crew = ','.join(astronauts)
				self.spacecraft_crew_count = len(astronauts)
			
			try:
				self.spacecraft_maiden_flight = timestamp_to_unix(
					spacecraft['spacecraft']['spacecraft_config']['maiden_flight'])
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


def construct_params(PARAMS: dict) -> str:
	param_url = ''
	if PARAMS is not None:	
		for enum, keyvals in enumerate(PARAMS.items()):
			key, val = keyvals[0], keyvals[1]
			param_url += f'?{key}={val}' if enum == 0 else f'&{key}={val}'

	return param_url


def ll2_api_call(data_dir: str, scheduler: BackgroundScheduler, bot_username: str):
	# params
	VERSION = '1.6-alpha'
	DEBUG_API = True

	# debug print
	logging.debug('➡️ Running API call...')

	# datetime, so we can only get launches starting today
	now = datetime.datetime.now()
	today_call = f'{now.year}-{now.month}-{now.day}'

	# what we're throwing at the API
	API_URL = 'https://ll.thespacedevs.com'
	API_VERSION = '2.0.0'
	API_REQUEST = 'launch/upcoming'
	PARAMS = {'mode': 'detailed', 'limit': 50}

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}' #&{fields}

	# set headers
	headers = {'user-agent': f'telegram-{BOT_USERNAME}/{VERSION}'}

	# if debugging and the debug file exists, run this
	if DEBUG_API and os.path.isfile(os.path.join(data_dir,'ll2-json.json')):
		with open(os.path.join(data_dir, 'll2-json.json'), 'r') as json_file:
			api_json = json.load(json_file)

		rec_data = 0
		logging.warning('⚠️ API call skipped!')
		time.sleep(1.5)
	else:
		try:
			API_RESPONSE = requests.get(API_CALL, headers=headers)
			rec_data = len(API_RESPONSE.content)
		except Exception as error:
			logging.warning(f'🛑 Error in LL API request: {error}')
			logging.warning('⚠️ Trying again after 3 seconds...')

			time.sleep(3)
			return ll2_api_call(data_dir=data_dir, scheduler=scheduler, bot_username=bot_username)

		try:
			api_json = json.loads(API_RESPONSE.text)
		except Exception as json_parse_error:
			logging.warning('⚠️ Error parsing json')

		# dump json
		with open(os.path.join(data_dir, 'll2-json.json'), 'w') as json_file:
			json.dump(api_json, json_file, indent=4)

	# store update time
	api_updated = int(time.time())

	# parse the result json into a set of launch objects
	launch_obj_set = set()
	for launch_json in api_json['results']:
		launch_obj_set.add(LaunchLibrary2Launch(launch_json))

	# success?
	logging.debug(f'✅ Parsed {len(launch_obj_set)} launches into launch_obj_set.')

	# update database with the launch objects
	update_launch_db(launch_set=launch_obj_set, db_path=data_dir, bot_username=bot_username)
	logging.debug('✅ DB update complete!')

	# update statistics
	update_stats_db(
		stats_update={
			'api_requests': 1, 'db_updates': 1,
			'data': rec_data, 'last_api_update': api_updated},
		db_path='data'
	)

	# schedule next API call
	next_api_update = api_call_scheduler(
		db_path=data_dir, scheduler=scheduler, ignore_60=True, bot_username=bot_username)

	# schedule notifications
	notification_send_scheduler(
		db_path=data_dir, next_api_update_time=next_api_update, scheduler=scheduler)


def api_call_scheduler(db_path: str, scheduler: BackgroundScheduler, ignore_60: bool, bot_username: str) -> int:
	"""Summary
	Schedules upcoming API calls for when they'll be required.
	Calls are scheduled with the following logic:
	- every 20 minutes, unless any of the following has triggered an update:
		- 30 seconds before upcoming notification sends
		- the moment a launch is due to happen (postpone notification)

	The function returns the timestamp for when the next API call should be run.
	Whenever an API call is performed, the next call should be scheduled.

	TODO improve checking for overlapping jobs, especially when notification checks
	are scheduled. Keep track of scheduled job IDs. LaunchBot-class in main thread?
	"""
	def schedule_call(unix_timestamp: int) -> int:
		# convert to a datetime object
		next_update_dt = datetime.datetime.fromtimestamp(unix_timestamp)

		# schedule next API update, and we're done: next update will be scheduled after the API update
		scheduler.add_job(
			ll2_api_call, 'date', run_date=next_update_dt,
			args=[db_path, scheduler, bot_username], id=f'api-{unix_timestamp}')

		logging.debug('🔄 Next API update scheduled for %s', next_update_dt)
		return unix_timestamp

	def require_immediate_update(cursor) -> tuple:
		'''Summary
		Load previous time on startup to figure out if we need to update right now
		'''
		try:
			cursor.execute(f'SELECT last_api_update FROM stats')
		except sqlite3.OperationalError:
			return (True, None)

		last_update = cursor.fetchall()[0][0]
		return (True, None) if time.time() > last_update + 15 * 60 else (False, last_update)

	# debug print
	logging.info('⏲ Starting api_call_scheduler...')

	# load the next upcoming launch from the database
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# verify we don't need an immediate API update
	db_status = require_immediate_update(cursor)
	update_immediately, last_update = db_status[0], db_status[1]

	if update_immediately:
		logging.info('⚠️ DB outdated: scheduling next API update 5 seconds from now...')
		return schedule_call(int(time.time()) + 5)
	else:
		update_delta = int(time.time()) - last_update
		to_next_update = 15 * 60 - update_delta
		next_auto_update = int(time.time()) + to_next_update

		logging.info(f'🔀 DB up-to-date! Updating in {int(to_next_update / 60)} minutes.')

	# pull all launches with a net greater than or equal to current time
	select_fields = 'net_unix, notify_24h, notify_12h, notify_60min, notify_5min'
	try:
		cursor.execute(f'SELECT {select_fields} FROM launches WHERE net_unix >= ?', (int(time.time()),))
		query_return = cursor.fetchall()
	except sqlite3.OperationalError:
		query_return = set()

	conn.close()

	if len(query_return) == 0:
		logging.warning('⚠️ No launches found for scheduling: running in 5 seconds...')
		os.rename(
			os.path.join(db_path, 'launchbot-data.db'),
			os.path.join(db_path, f'launchbot-data-sched-error-{int(time.time())}.db'))
		return schedule_call(int(time.time()) + 5)

	# sort in-place by NET
	query_return.sort(key=lambda tup:tup[0])

	'''
	Create a list of notification send times, but also during launch to check for a postpone.
	- notification times, if not sent (60 seconds before)
	- as the launch is supposed to occur
	- now + 20 minutes
	'''
	notif_times, time_map = set(), {0: 24*3600+60, 1: 12*3600+60, 2: 3600+60, 3: 5*60+60}
	for launch_row in query_return:
		notif_times.add(launch_row[0])
		for enum, notif_bool in enumerate(launch_row[1::]):
			if not notif_bool:
				# time for check: launch time - notification time (before launch time)
				check_time = launch_row[0] - time_map[enum]

				# if less than 60 sec until next check, pass if ignore_60 flag is set
				if check_time - int(time.time()) < 30 and ignore_60:
					pass
				elif check_time < time.time():
					pass
				else:
					notif_times.add(check_time)


	# add scheduled check every 30 minutes to comparison
	notif_times.add(next_auto_update)

	# pick minimum of all possible API updates, convert to a datetime object
	next_api_update = min(notif_times)

	if next_api_update == next_auto_update:
		logging.info(f'📨 No notifications coming up before next API update.')

	# schedule
	return schedule_call(next_api_update)


if __name__ == '__main__':
	BOT_USERNAME = 'rocketrybot'
	DATA_DIR = 'data'

	# init log
	logging.basicConfig(level=logging.DEBUG,format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	# disable logging for urllib and requests because jesus fuck they make a lot of spam
	logging.getLogger('requests').setLevel(logging.CRITICAL)
	logging.getLogger('urllib3').setLevel(logging.CRITICAL)
	logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)

	# init and start scheduler
	scheduler = BackgroundScheduler()
	scheduler.start()

	# start API and notification scheduler
	api_call_scheduler(db_path=DATA_DIR, ignore_60=False, scheduler=scheduler, bot_username=BOT_USERNAME)

	while True:
		time.sleep(10)
