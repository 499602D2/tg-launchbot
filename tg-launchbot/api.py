import os
import sys
import time
import logging
import difflib
import datetime
import sqlite3

import redis
import requests
import coloredlogs
import ujson as json

from apscheduler.schedulers.background import BackgroundScheduler

# local imports
from utils import timestamp_to_unix, time_delta_to_legible_eta
from db import update_launch_db, update_stats_db
from notifications import (
	notification_send_scheduler, postpone_notification,
	remove_previous_notification, store_notification_identifiers)

class LaunchLibrary2Launch:
	'''
	A class for simplifying the handling of launch objects. Contains all the properties needed
	by the bot.
	'''
	def __init__(self, launch_json: dict):
		# launch unique information
		self.name = launch_json['name']
		self.unique_id = launch_json['id']
		self.ll_id = launch_json['launch_library_id']

		# net and status
		self.net_unix = timestamp_to_unix(launch_json['net'])
		self.status_id = launch_json['status']['id']
		self.status_state = launch_json['status']['abbrev']

		status_map = {
			'Go': 'GO',
			'Hold': 'HOLD',
			'In Flight': 'FLYING',
			'Success': 'SUCCESS',
			'Partial Failure': 'PFAILURE',
			'Failure': 'FAILURE'
		}

		if self.status_state in status_map.keys():
			self.status_state = status_map[self.status_state]

		self.in_hold = launch_json['inhold']
		self.success = bool('Success' in launch_json['status']['name'])

		# pull probability
		self.probability = launch_json['probability']

		# WARNING: DEPRECATED IN LL ≥2.1.0: TODO remove in a future version
		# set tbd_time and tbd_date
		self.tbd_time = launch_json['tbdtime'] if 'tbdtime' in launch_json else True
		self.tbd_date = launch_json['tbddate'] if 'tbddate' in launch_json else True

		# set launched state based on status_state
		launch_bool = [status for status in ('success', 'failure') if status in self.status_state.lower()]
		self.launched = bool(any(launch_bool))

		# lsp/agency info
		try:
			self.lsp_id = launch_json['launch_service_provider']['id']
			self.lsp_name = launch_json['launch_service_provider']['name']
			self.lsp_short = launch_json['launch_service_provider']['abbrev']
			self.lsp_country_code = launch_json['launch_service_provider']['country_code']
		except TypeError:
			self.lsp_id = None
			self.lsp_name = None
			self.lsp_short = None
			self.lsp_country_code = None
			logging.exception(f'⚠️ Error parsing launch_service_provider! launch_json: {launch_json}')

		# webcast status and links
		self.webcast_islive = launch_json['webcast_live']
		self.webcast_url_list = None # preset to None

		# url_list is a list of dictionaries
		if len(launch_json['vidURLs']) >= 1:
			# parse by priority
			priority_map = {}
			for url_dict in launch_json['vidURLs']:
				priority = url_dict['priority']
				url = url_dict['url']

				if priority in priority_map.keys():
					priority_map[priority] = priority_map[priority] + ',' + url
				else:
					priority_map[priority] = url

			# pick highest priority string
			try:
				highest_prior = min(priority_map.keys())
			except ValueError:
				highest_prior = None

			if highest_prior is not None:
				self.webcast_url_list = priority_map[highest_prior]
			else:
				logging.warning(f'highest_prior is None but vidURLs ≥ 1. ID: {self.unique_id}')
				self.webcast_url_list = None
		else:
			self.webcast_url_list = None

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
		if 'Rocket Lab' in self.pad_name:
			# avoid super long pad name for "Rocket Lab Launch Complex"
			self.pad_name = self.pad_name.replace('Rocket Lab', 'RL')
		self.location_name = launch_json['pad']['location']['name']
		self.location_country_code = launch_json['pad']['location']['country_code']

		# tidbits for fun facts etc.
		try:
			self.pad_nth_launch = launch_json['pad']['total_launch_count']
			self.location_nth_launch = launch_json['pad']['location']['total_launch_count']
			self.agency_nth_launch = launch_json['agency_launch_attempt_count']
			self.agency_nth_launch_year = launch_json['agency_launch_attempt_count_year']
		except KeyError:
			self.pad_nth_launch = None
			self.location_nth_launch = None
			self.agency_nth_launch = None
			self.agency_nth_launch_year = None

		if 'orbital_launch_attempt_count_year' in launch_json:
			self.orbital_nth_launch_year = launch_json['orbital_launch_attempt_count_year']
		else:
			self.orbital_nth_launch_year = None


def construct_params(PARAMS: dict) -> str:
	'''
	Constructs the params string for the url from the given key-vals.
	'''
	param_url = ''
	if PARAMS is not None:
		for enum, keyvals in enumerate(PARAMS.items()):
			key, val = keyvals[0], keyvals[1]
			param_url += f'?{key}={val}' if enum == 0 else f'&{key}={val}'

	return param_url


def clean_launch_db(last_update, db_path):
	'''
	Function cleans launches from the database that are effectively
	out of bounds of the 50 launch update request, thus representing
	false launches.

	last_update: the time the API was updated, for cleaning the launches
	'''

	# connect to db
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	conn.row_factory = sqlite3.Row
	cursor = conn.cursor()

	cursor.execute('SELECT name FROM sqlite_master WHERE type = ? AND name = ?', ('table', 'launches'))
	if len(cursor.fetchall()) == 0:
		return

	# select launches that have not happened and weren't updated in the last API update
	cursor.execute(
		'SELECT unique_id FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
		(last_update, int(time.time())))

	# this is the slow way, but let's do it this way for the sake of logging what's happening
	deleted_launches = set()
	for launch_row in cursor.fetchall():
		deleted_launches.add(launch_row[0])

	if len(deleted_launches) == 0:
		logging.debug('✨ Database already clean: nothing to do!')
		return

	logging.info(f'✨ Deleting {len(deleted_launches)} launches that have slipped out of range...')
	try:
		cursor.execute(
			'DELETE FROM launches WHERE launched = 0 AND last_updated < ? AND net_unix > ?',
			(last_update, int(time.time())))

		logging.info(f'⚠️ Deleted: {deleted_launches}')
	except Exception:
		logging.exception('⚠️ Error deleting slipped launches!')

	conn.commit()
	conn.close()


def ll2_api_call(
	data_dir: str, scheduler: BackgroundScheduler, bot_username: str, bot: 'telegram.bot.Bot'):
	# debug everything but the API: "true" simply loads the previous .json
	DEBUG_API = False

	# debug print
	logging.debug('🔄 Running API call...')

	# what we're throwing at the API
	API_URL = 'https://ll.thespacedevs.com'
	API_VERSION = '2.1.0'
	API_REQUEST = 'launch/upcoming'
	PARAMS = {'mode': 'detailed', 'limit': 50}

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}' #&{fields}

	# set headers
	headers = {'user-agent': f'telegram-{bot_username}'}

	# if debugging and the debug file exists, run this
	if DEBUG_API and os.path.isfile(os.path.join(data_dir, 'debug-json.json')):
		with open(os.path.join(data_dir, 'debug-json.json'), 'r') as json_file:
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
			return ll2_api_call(
				data_dir=data_dir, scheduler=scheduler,
				bot_username=bot_username, bot=bot)

		try:
			api_json = json.loads(API_RESPONSE.text)
			if DEBUG_API:
				with open(os.path.join(data_dir, 'debug-json.json'), 'w') as jsonf:
					json.dump(api_json, jsonf, indent=4)
		except Exception as json_parse_error:
			logging.exception(f'⚠️ Error parsing json: {json_parse_error}')

			# dump json for inspection / debugging use
			with open(os.path.join(data_dir, f'error-json-{int(time.time())}.json'), 'w') as jsonf:
				json.dump(api_json, jsonf, indent=4)

			logging.warning('⚠️ Trying again after 10 seconds...')
			time.sleep(10)

			return ll2_api_call(
				data_dir=data_dir, scheduler=scheduler,
				bot_username=bot_username, bot=bot)

	# store update time
	api_updated = int(time.time())

	# parse the result json into a set of launch objects
	launch_obj_set = set()
	for launch_json in api_json['results']:
		launch_obj_set.add(LaunchLibrary2Launch(launch_json))

	# success?
	logging.debug(f'✅ Parsed {len(launch_obj_set)} launches into launch_obj_set.')

	# update database with the launch objects
	postponed_launches = update_launch_db(
		launch_set=launch_obj_set, db_path=data_dir,
		bot_username=bot_username, api_update=api_updated)

	# clean launches that have yet to launch and that weren't updated
	clean_launch_db(last_update=api_updated, db_path=data_dir)

	logging.debug('✅ DB update & cleaning complete!')

	# if a launch (or multiple) has been postponed, handle it here
	if len(postponed_launches) > 0:
		logging.info(f'Found {len(postponed_launches)} postponed launches!')

		''' Handle each possibly postponed launch separately.
		tuple: (launch_object, postpone_message) '''
		for postpone_tuple in postponed_launches:
			# pull launch object from tuple
			launch_object = postpone_tuple[0]

			notify_list, sent_notification_ids = postpone_notification(
				db_path=data_dir, postpone_tuple=postpone_tuple, bot=bot)

			logging.info('Sent notifications!')
			logging.info(f'notify_list={notify_list}, sent_notification_ids={sent_notification_ids}')

			# remove previous notification
			remove_previous_notification(
				db_path=data_dir, launch_id=launch_object.unique_id,
				notify_set=notify_list, bot=bot)

			logging.info('✉️ Previous notifications removed!')

			# notifications sent: store identifiers
			msg_id_str = ','.join(sent_notification_ids)
			store_notification_identifiers(
				db_path=data_dir, launch_id=launch_object.unique_id, identifiers=msg_id_str)
			logging.info(f'📃 Notification identifiers stored! identifiers="{msg_id_str}"')

			# update stats
			update_stats_db(stats_update={ 'notifications': len(notify_list) }, db_path=data_dir)
			logging.info('📊 Stats updated!')

	# update statistics
	update_stats_db(
		stats_update={
			'api_requests': 1, 'db_updates': 1,
			'data': rec_data, 'last_api_update': api_updated},
		db_path=data_dir
	)

	# schedule next API call
	next_api_update = api_call_scheduler(
		db_path=data_dir, scheduler=scheduler, ignore_60=True,
		bot_username=bot_username, bot=bot)

	# schedule notifications
	notification_send_scheduler(
		db_path=data_dir, next_api_update_time=next_api_update, scheduler=scheduler,
		bot_username=bot_username, bot=bot)


def api_call_scheduler(
	db_path: str, scheduler: BackgroundScheduler, ignore_60: bool,
	bot_username: str, bot: 'telegram.bot.Bot') -> int:
	"""
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
		# verify time isn't in the past
		if unix_timestamp <= int(time.time()):
			logging.warning('schedule_call called with a timestamp in the past! Scheduling for t+3 seconds.')
			unix_timestamp = int(time.time()) + 3

		# delta
		until_update = unix_timestamp - int(time.time())

		# convert to a datetime object
		next_update_dt = datetime.datetime.fromtimestamp(unix_timestamp)

		# schedule next API update, and we're done: next update will be scheduled after the API update
		scheduler.add_job(
			ll2_api_call, 'date', run_date=next_update_dt,
			args=[db_path, scheduler, bot_username, bot], id=f'api-{unix_timestamp}')

		logging.debug('🔄 Next API update in %s (%s)',
			time_delta_to_legible_eta(time_delta=until_update, full_accuracy=False), next_update_dt)

		return unix_timestamp

	def require_immediate_update(cursor: sqlite3.Cursor) -> tuple:
		'''Summary
		Load previous time on startup to figure out if we need to update right now
		'''
		try:
			cursor.execute('SELECT last_api_update FROM stats')
		except sqlite3.OperationalError:
			return (True, None)

		last_update = cursor.fetchall()[0][0]
		if last_update in ('', None):
			return (True, None)

		return (True, None) if time.time() > last_update + UPDATE_PERIOD * 60 else (False, last_update)

	# debug print
	logging.debug('⏲ Starting api_call_scheduler...')

	# update period, in minutes
	UPDATE_PERIOD = 15

	# load the next upcoming launch from the database
	conn = sqlite3.connect(os.path.join(db_path, 'launchbot-data.db'))
	cursor = conn.cursor()

	# verify we don't need an immediate API update
	db_status = require_immediate_update(cursor)
	update_immediately, last_update = db_status[0], db_status[1]

	if update_immediately:
		logging.debug('⚠️ DB outdated: scheduling next API update 5 seconds from now...')
		return schedule_call(int(time.time()) + 5)

	# if we didn't return above, no need to update immediately
	update_delta = int(time.time()) - last_update
	last_updated_str = time_delta_to_legible_eta(update_delta, full_accuracy=False)

	logging.debug(f'🔀 DB up-to-date! Last updated {last_updated_str} ago.')

	# pull all launches with a net greater than or equal to notification window start
	select_fields = 'net_unix, launched, status_state'
	select_fields += ', notify_24h, notify_12h, notify_60min, notify_5min'
	notify_window = int(time.time()) - 60*5

	try:
		cursor.execute(f'SELECT {select_fields} FROM launches WHERE net_unix >= ?', (notify_window,))
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
	- 60 seconds after the launch is supposed to occur
	'''
	notif_times, time_map = set(), {0: 24*3600, 1: 12*3600, 2: 3600, 3: 5*60}
	for launch_row in query_return:
		# don't use unverified launches for scheduling (status_state == TBD)
		launch_status = launch_row[2]
		if launch_status == 'TBD':
			continue

		# shortly after launch time for possible postpone/abort, if not launched
		if not launch_row[1] and time.time() - launch_row[0] < 60:
			notif_times.add(launch_row[0] + 60)

		for enum, notif_bool in enumerate(launch_row[3::]):
			if not notif_bool:
				# time for check: launch time - notification time - 60 (60s before)
				check_time = launch_row[0] - time_map[enum] - 60

				# if less than 60 sec until next check, pass if ignore_60 flag is set
				if check_time - int(time.time()) < 60 and ignore_60:
					pass
				elif check_time < time.time():
					pass
				else:
					notif_times.add(check_time)

	# get time when next notification will be sent
	next_notif = min(notif_times)
	until_next_notif = next_notif - int(time.time())
	next_notif_send_time = time_delta_to_legible_eta(time_delta=until_next_notif, full_accuracy=False)

	# convert to a datetime object for scheduling
	next_notif = datetime.datetime.fromtimestamp(next_notif)

	# schedule next update more loosely if next notif is far away
	# times are 15 min * upd_period_mult, e.g. 4 == 1 hour
	if until_next_notif >= 3600 * 24:
		# if more than 24 hours until next notif, check once every 4 hours
		upd_period_mult = 16
	elif until_next_notif >= 3600 * 12:
		# if 12 - 24 hours until next notif, check every 2 hours
		upd_period_mult = 8
	elif until_next_notif >= 3600:
		# if 1 - 12, check once an hour
		upd_period_mult = 4
	else:
		# if less than an hour, check every 20 minutes
		upd_period_mult = 1.35

	# add next auto-update to notif_times
	to_next_update = int(UPDATE_PERIOD * upd_period_mult) * 60 - update_delta
	next_auto_update = int(time.time()) + to_next_update
	notif_times.add(next_auto_update)

	# pick minimum of all possible API updates
	next_api_update = min(notif_times)

	# push to redis so we can expire a bunch of keys just after next update
	rd = redis.Redis(host='localhost', port=6379, db=0, decode_responses=True)
	rd.flushdb()
	logging.debug('📕 Redis db flushed!')
	rd.set('next-api-update', next_api_update)

	# if next update is same as auto-update, log as information
	if next_api_update == next_auto_update:
		logging.debug('📭 Auto-updating: no notifications coming up before next API update.')
	else:
		logging.debug('📬 Notification coming up before next API update: not auto-updating!')

	# log time next notification is sent
	logging.debug(f'📮 Next notification in {next_notif_send_time} ({next_notif})')

	# schedule the call
	return schedule_call(next_api_update)


if __name__ == '__main__':
	'''
	api.py can be run directly for API and scheduling testing purposes.
	In this case, the bot will do everything but send a message, as the bot hasn't
	been defined/initialized.
	'''
	BOT_USERNAME = 'debug-tg-launchbot'
	DATA_DIR = 'launchbot'

	# verify data_dir exists
	if not os.path.isdir(DATA_DIR):
		os.makedirs(DATA_DIR)

	# init log
	logging.basicConfig(
		level=logging.DEBUG, format='%(asctime)s %(message)s', datefmt='%d/%m/%Y %H:%M:%S')

	# disable logging for urllib and requests because jesus fuck they make a lot of spam
	logging.getLogger('apscheduler').setLevel(logging.WARNING)
	logging.getLogger('requests').setLevel(logging.CRITICAL)
	logging.getLogger('urllib3').setLevel(logging.CRITICAL)
	logging.getLogger('chardet.charsetprober').setLevel(logging.CRITICAL)

	# add color
	coloredlogs.install(level='DEBUG')

	# init and start scheduler
	scheduler = BackgroundScheduler()
	scheduler.start()

	# start API and notification scheduler
	api_call_scheduler(
		db_path=DATA_DIR, ignore_60=False, scheduler=scheduler,
		bot_username=BOT_USERNAME, bot=None)

	while True:
		time.sleep(10)
