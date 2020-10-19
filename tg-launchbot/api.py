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
from db import update_launch_db

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
			param_url += f'?{key}={val}' if enum == 0 else f'&{key}={val}'

	return param_url


def ll2_api_call(data_dir, scheduler):
	# bot params
	BOT_USERNAME = 'rocketrybot'
	VERSION = '1.6-alpha'
	DEBUG_API = True

	# debug print
	print('➡️ Running API call...')

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

	# if debugging and the debug file exists, run this
	if DEBUG_API and os.path.isfile(os.path.join(data_dir,'ll2-json.json')):
		with open(os.path.join(data_dir, 'll2-json.json'), 'r') as json_file:
			api_json = json.load(json_file)

		print('⚠️ API call skipped!')
		time.sleep(1.5)
	else:
		try:
			API_RESPONSE = requests.get(API_CALL, headers=headers)
		except Exception as error:
			print(f'🛑 Error in LL API request: {error}')
			print('⚠️ Trying again after 3 seconds...')

			time.sleep(3)
			return ll2_api_call()

		try:
			api_json = json.loads(API_RESPONSE.text)
		except Exception as json_parse_error:
			print('⚠️ Error parsing json')

		# dump json
		with open('ll2-json.json', 'w') as json_file:
			json.dump(api_json, json_file, indent=4)

	# parse the result json into a set of launch objects
	launch_obj_set = set()
	for launch_json in api_json['results']:
		launch_obj_set.add(LaunchLibrary2Launch(launch_json))

	# success?
	print(f'✅ Parsed {len(launch_obj_set)} launches into launch_obj_set.')

	# update database with the launch objects
	update_launch_db(launch_set=launch_obj_set, db_path=data_dir)
	print('✅ DB update complete!')

	# schedule next API call
	api_call_scheduler(db_path=data_dir, scheduler=scheduler, ignore_30=True)

	# TODO update schedule for checking for pending notifications
	notification_send_scheduler()
	
	# success
	return True


def api_call_scheduler(db_path, scheduler, ignore_30):
	"""Summary
	Schedules upcoming API calls for when they'll be required.
	Calls are scheduled with the following logic:
	- every 20 minutes, unless any of the following has triggered an update:
		- 30 seconds before upcoming notification sends
		- the moment a launch is due to happen (postpone notification)

	The function returns the timestamp for when the next API call should be run.
	Whenever an API call is performed, the next call should be scheduled.
	"""
	# debug print
	print('⏲ Starting api_call_scheduler...')

	# load the next upcoming launch from the database
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# pull all launches with a net greater than or equal to current time
	select_fields = 'net_unix, notify_24h, notify_12h, notify_60min, notify_5min'
	cursor.execute(f'SELECT {select_fields} FROM launches WHERE net_unix >= ?', (int(time.time()),))

	query_return = cursor.fetchall()
	conn.close()

	if len(query_return) == 0:
		print('⚠️ No launches found for scheduling: running in 5 seconds...')
		return time.time() + 5

	# sort in-place by NET
	query_return.sort(key=lambda tup:tup[0])

	'''
	Create a list of notification send times, but also during launch to check for a postpone.
	- notification times, if not sent (30 seconds before)
	- as the launch is supposed to occur
	- now + 20 minutes
	'''
	notif_times, time_map = set(), {0: 24*3600+30, 1: 12*3600+30, 2: 3600+30, 3: 5*60+30}
	for launch_row in query_return:
		notif_times.add(launch_row[0])
		for enum, notif_bool in enumerate(launch_row[1::]):
			if not notif_bool:
				# time for check
				check_time = launch_row[0] - time_map[enum]

				# if less than 30 sec until next check, skip if ignore_30 flag is set
				if check_time - int(time.time()) < 30 and ignore_30:
					pass
				else:
					notif_times.add(check_time)


	# add scheduled +20 min check to comparison
	notif_times.add(int(time.time()) + 15) # 20*60

	# pick min
	next_api_update = min(notif_times)
	next_update_dt = datetime.datetime.fromtimestamp(next_api_update)

	# schedule next API update
	scheduler.add_job(ll2_api_call, 'date', run_date=next_update_dt, args=[db_path, scheduler])
	print('Next API update scheduled for', next_update_dt)


if __name__ == '__main__':
	DATA_DIR = 'data'

	# init and start scheduler
	scheduler = BackgroundScheduler()
	scheduler.start()

	print('➡️ Testing scheduling...')
	api_call_scheduler(db_path=DATA_DIR, ignore_30=False, scheduler=scheduler)

	while True:
		time.sleep(5)

