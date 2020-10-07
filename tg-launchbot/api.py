import os
import time
import logging
import difflib
import datettime

import requests
import ujson as json


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


def ll2_api_call():
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

		print('✅ Success!')

		return




def r_spx_api_call():
	pass


if __name__ == '__main__':
	print('Starting API calls...')

