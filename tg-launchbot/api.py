import os
import time
import logging
import difflib
import datetime

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

		# webcast status and links
		self.webcast_islive = launch_json['webcast_live']
		self.webcast_url_list = ','.join(launch_json['vidURLs'])

		# rocket information
		self.rocket_name = launch_json['rocket']['configuration']['name']
		self.rocket_full_name = launch_json['rocket']['configuration']['full_name']
		self.rocket_variant = launch_json['rocket']['configuration']['variant']
		self.rocket_family = launch_json['rocket']['configuration']['family']
		
		# launcher stage information
		if launch_json['rocket']['launcher_stage'] is not None:
			# id, type, reuse status, flight number
			self.launcher_stage_id = launch_json['rocket']['launcher_stage']['id']
			self.launcher_stage_type = launch_json['rocket']['launcher_stage']['type']
			self.launcher_stage_is_reused = launch_json['rocket']['launcher_stage']['reused']
			self.launcher_stage_flight_number = launch_json['rocket']['launcher_stage']['flight_number']

			# flight proven and serial number
			self.launcher_is_flight_proven = launch_json['rocket']['launcher_stage']['launcher']['flight_proven']
			self.launcher_serial_number = launch_json['rocket']['launcher_stage']['launcher']['serial_number']

			# first flight and maiden flight
			self.launcher_maiden_flight = timestamp_to_unix(launch_json['rocket']['launcher_stage']['launcher']['first_launch_date'])
			self.launcher_last_flight = timestamp_to_unix(launch_json['rocket']['launcher_stage']['launcher']['last_Launch_date'])

			# landing attempt, landing location, landing type, landing count at location
			if launch_json['rocket']['launcher_stage']['landing'] is not None:
				self.launcher_landing_attempt = launch_json['rocket']['launcher_stage']['landing']['attempt']
				self.launcher_landing_location = launch_json['rocket']['launcher_stage']['landing']['location']['abbrev']
				self.landing_type = launch_json['rocket']['launcher_stage']['landing']['type']['abbrev']
				self.launcher_landing_location_nth_landing = launch_json['rocket']['launcher_stage']['landing']['location']['successful_landings']

		# ⚠️ TODO spacecraft stage information ⚠️
		if launch_json['rocket']['spacecraft_stage'] is not None:
			self.spacecraft_stage = None


		# mission (payload) information
		self.mission_name = launch_json['mission']['name']
		self.mission_type = launch_json['mission']['type']
		self.mission_orbit = launch_json['mission']['orbit']['name']
		self.mission_orbit_abbrev = launch_json['mission']['orbit']['abbrev']
		self.mission_description = launch_json['mission']['description']

		# launch location information
		self.pad_name = launch_json['pad']['name']
		self.location_name = launch_json['pad']['location']['name']
		self.location_country_code = launch_json['pad']['location']['country_code']

		# tidbits for fun facts etc.
		self.pad_nth_launch = launch_json['pad']['total_launch_count']
		self.location_nth_launch = launch_json['pad']['location']['total_launch_count']


	def timestamp_to_unix(timestamp):
		# convert to a datetime object
		utc_dt = datetime.datetime.strptime(timestamp, '%Y%m%dT%H%M%S%fZ')

		# convert UTC datetime to seconds since the Epoch, return
		return (utc_dt - datetime.datetime(1970, 1, 1)).total_seconds()


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

def parse_ll2_launch():
	pass


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
	PARAMS = {'mode': 'detailed', 'limit': 50}

	# construct the call URL
	API_CALL = f'{API_URL}/{API_VERSION}/{API_REQUEST}{construct_params(PARAMS)}' #&{fields}

	# set headers
	headers = {'user-agent': f'telegram-{BOT_USERNAME}/{VERSION}'}

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

	for launch in api_json:
		print(launch)

	print('––––––––––')
	print(f"count: {api_json['count']}")
	print(f'next: {len(api_json["next"])}')
	
	try:
		print(f'previous: {len(api_json["previous"])}')
	except:
		print('previous: None')

	print(f'results: {len(api_json["results"])}')

	print('-- next --')
	print(api_json['next'])

	print('-- results[0] --')
	print(api_json['results'][0])

def r_spx_api_call():
	pass


if __name__ == '__main__':
	print('Starting API calls...')
	ll2_api_call()
	print('Done!')

